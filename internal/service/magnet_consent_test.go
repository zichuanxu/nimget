package service_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"golang.org/x/time/rate"
)

func TestMagnetDirectConsentIsPerTaskAndSurvivesRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.MaxConcurrent = 1
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	var blockedAnnounces atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		blockedAnnounces.Add(1)
	}))
	defer tracker.Close()
	blocked, err := c.Add(ctx, download.Request{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=" + url.QueryEscape(tracker.URL)})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Status != download.StatusQueued || blocked.Phase != download.PhaseProxyBlocked {
		t.Fatalf("task did not expose the block on submission: %+v", blocked)
	}
	link := trackedMagnetFixture(t, "direct-consent.bin", []byte("controlled torrent payload"), false)
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	allow := true
	if err := c.Update(ctx, item.ID, download.Patch{DirectConnectionAllowed: &allow}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	c = magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	paused := waitForConsentTask(t, ctx, c, item.ID, download.StatusPaused, "")
	if !paused.DirectConnectionAllowed {
		t.Fatalf("consent was lost on restart: %+v", paused)
	}
	if err := c.Resume(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	if done.Status != download.StatusCompleted {
		t.Fatalf("authorized task did not complete: %+v", done)
	}
	blocked = waitForConsentTask(t, ctx, c, blocked.ID, download.StatusQueued, download.PhaseProxyBlocked)
	if blocked.DirectConnectionAllowed || blockedAnnounces.Load() != 0 {
		t.Fatalf("the other task unexpectedly connected: %+v, announces=%d", blocked, blockedAnnounces.Load())
	}
}

func TestMagnetCheckpointsWhenDirectPermissionIsLost(t *testing.T) {
	proxy := netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	for _, loss := range []string{"revocation", "proxy settings change"} {
		t.Run(loss, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cfg := handoffServiceConfig(t)
			c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
			prefs, err := c.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if loss == "revocation" {
				prefs.Proxy = proxy
			} else {
				prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
			}
			if _, err := c.PutSettings(ctx, prefs); err != nil {
				t.Fatal(err)
			}

			const pieceLength = 32 << 10
			payload := bytes.Repeat([]byte("verifiable data"), pieceLength)
			limit := rate.NewLimiter(128<<10, 16<<10)
			link, seeder, _ := trackedMagnetPeer(t, "direct.bin", payload, false, limit)
			task, err := c.Add(ctx, download.Request{URL: link})
			if err != nil {
				t.Fatal(err)
			}
			allow := func(ctx context.Context, value bool) error {
				return c.Update(ctx, task.ID, download.Patch{DirectConnectionAllowed: &value})
			}
			if loss == "revocation" {
				if err := allow(ctx, true); err != nil {
					t.Fatal(err)
				}
			}
			list := func() ([]download.Item, error) { return c.List(ctx) }
			before := waitForMagnetState(t, ctx, list, task.ID, func(item download.Item) bool {
				return item.Status == download.StatusRunning && item.Completed >= 2*pieceLength
			})

			stopCtx, stopCancel := context.WithTimeout(ctx, 2*time.Second)
			defer stopCancel()
			if loss == "revocation" {
				err = allow(stopCtx, false)
			} else {
				prefs.Proxy = proxy
				_, err = c.PutSettings(stopCtx, prefs)
			}
			if err != nil {
				t.Fatal(err)
			}
			blocked := waitForConsentTask(t, stopCtx, c, task.ID, download.StatusQueued, download.PhaseProxyBlocked)
			if blocked.DirectConnectionAllowed || blocked.Completed < before.Completed || blocked.Total != int64(len(payload)) {
				t.Fatalf("permission loss did not checkpoint the task: %+v", blocked)
			}
			stats := seeder.Stats()
			sent := stats.BytesWrittenData.Int64()
			time.Sleep(200 * time.Millisecond)
			stats = seeder.Stats()
			if stats.BytesWrittenData.Int64() != sent {
				t.Fatal("blocked task continued receiving payload after checkpoint")
			}

			limit.SetLimit(rate.Inf)
			if err := allow(ctx, true); err != nil {
				t.Fatal(err)
			}
			done := waitForMagnetTask(t, ctx, list, task.ID)
			got, err := os.ReadFile(done.Destination)
			if done.Status != download.StatusCompleted || err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("restoring consent did not resume the download: %+v, %v", done, err)
			}
			stats = seeder.Stats()
			if received := stats.BytesWrittenData.Int64() - sent; received >= int64(len(payload)) {
				t.Fatalf("resume downloaded all payload again: %d bytes", received)
			}
		})
	}
}

func waitForConsentTask(t *testing.T, ctx context.Context, c *client.Client, id string, status download.Status, phase download.Phase) download.Item {
	t.Helper()
	return waitForMagnetState(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, id, func(item download.Item) bool {
		return item.Status == status && item.Phase == phase
	})
}
