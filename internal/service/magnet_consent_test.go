package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
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

func waitForConsentTask(t *testing.T, ctx context.Context, c *client.Client, id string, status download.Status, phase download.Phase) download.Item {
	t.Helper()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var last download.Item
	for {
		items, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == id {
				last = item
				if item.Status == status && item.Phase == phase {
					return item
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("task %s never reached %s/%s: %+v", id, status, phase, last)
		case <-tick.C:
		}
	}
}
