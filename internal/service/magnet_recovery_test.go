package service_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"golang.org/x/time/rate"
)

func TestMagnetResumesVerifiedPiecesAfterPauseAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	prefs.MaxConcurrent = 1
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	const pieceLength = 32 << 10
	payload := make([]byte, 32*pieceLength)
	for i := range payload {
		payload[i] = byte(i/257 + i/pieceLength)
	}
	limit := rate.NewLimiter(128<<10, 16<<10)
	link, seeder := trackedMagnetPeer(t, "resume.bin", payload, false, limit)
	queued, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	waitForMagnetState(t, ctx, c, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Completed >= 4*pieceLength && item.Completed < int64(len(payload))
	})
	if err := c.Pause(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(cfg.DownloadRoots[0], ".nimget-magnet-*.part"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("pause and shutdown should retain one staged payload: %v, %v", matches, err)
	}

	// Change one piece that had reached disk. The restarted service must
	// validate the saved data and request the damaged bytes again.
	staged, err := os.OpenFile(matches[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	stagedBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	damagedOffset := -1
	for i := 0; i+pieceLength <= len(stagedBytes); i += pieceLength {
		if bytes.Equal(stagedBytes[i:i+pieceLength], payload[i:i+pieceLength]) {
			damagedOffset = i
			break
		}
	}
	if damagedOffset < 0 {
		t.Fatal("pause did not keep any verified fixture pieces")
	}
	if _, err := staged.WriteAt([]byte{payload[damagedOffset] ^ 0xff}, int64(damagedOffset)); err != nil {
		t.Fatal(err)
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}

	stats := seeder.Stats()
	before := stats.BytesWrittenData.Int64()
	limit.SetLimit(rate.Inf)
	restarted := startHandoffService(t, cfg)
	c = magnetClient(t, restarted, cfg.TokenPath)
	paused := waitForMagnetState(t, ctx, c, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusPaused
	})
	if paused.Completed < 4*pieceLength || paused.Total != int64(len(payload)) {
		t.Fatalf("verify progress after pause and restart: %+v", paused)
	}
	if err := c.Resume(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, queued.ID)
	got, err := os.ReadFile(done.Destination)
	if done.Status != download.StatusCompleted || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed output mismatch: %+v, %d bytes, %v", done, len(got), err)
	}
	stats = seeder.Stats()
	if received := stats.BytesWrittenData.Int64() - before; received >= int64(len(payload))-pieceLength {
		t.Fatalf("download did not reuse good pieces: peer sent %d more bytes", received)
	}
}

func waitForMagnetState(t *testing.T, ctx context.Context, c *client.Client, id string, accept func(download.Item) bool) download.Item {
	t.Helper()
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	var last download.Item
	for {
		items, err := c.List(ctx)
		if err != nil {
			t.Fatalf("list magnet tasks (last state %+v): %v", last, err)
		}
		for _, item := range items {
			if item.ID == id {
				last = item
				if accept(item) {
					return item
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("magnet did not reach its expected state: %+v", last)
		case <-tick.C:
		}
	}
}
