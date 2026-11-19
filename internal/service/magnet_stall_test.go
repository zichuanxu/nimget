package service_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/settings"
	"golang.org/x/time/rate"
)

func TestMagnetMetadataStallReleasesQueueSlotAndCanRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 7*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	cfg.MagnetStallTimeout = 1500 * time.Millisecond
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	prefs.MaxConcurrent = 1
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	link, _, trackerOnline := trackedMagnetPeer(t, "missing-metadata.bin", []byte("metadata stall fixture"), false, nil)
	nextLink := trackedMagnetFixture(t, "queue-follower.bin", []byte("next in the queue"), false)
	trackerOnline.Store(false)
	stalled, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	list := func() ([]download.Item, error) { return c.List(ctx) }
	waitForMagnetState(t, ctx, list, stalled.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Phase == download.PhaseMetadata && item.Total == -1
	})
	next, err := c.Add(ctx, download.Request{URL: nextLink})
	if err != nil || next.Status != download.StatusQueued {
		t.Fatalf("next task should enter the queue: %+v, %v", next, err)
	}
	failed := waitForMagnetTask(t, ctx, list, stalled.ID)
	assertMagnetStall(t, failed, "metadata")
	if done := waitForMagnetTask(t, ctx, list, next.ID); done.Status != download.StatusCompleted {
		t.Fatalf("next task did not complete after metadata stalled: %+v", done)
	}
	trackerOnline.Store(true)
	if err := c.Retry(ctx, stalled.ID); err != nil {
		t.Fatal(err)
	}
	if done := waitForMagnetTask(t, ctx, list, stalled.ID); done.Status != download.StatusCompleted {
		t.Fatalf("metadata retry did not complete: %+v", done)
	}
}

func TestMagnetPayloadStallRetainsVerifiedPiecesAndCanRetry(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 13*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	cfg.MagnetStallTimeout = 1500 * time.Millisecond
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	const pieceLength = 32 << 10
	payload := make([]byte, 32*pieceLength)
	for i := range payload {
		payload[i] = byte(i/257 + i/pieceLength)
	}
	limit := rate.NewLimiter(128<<10, 16<<10)
	link, seeder, trackerOnline := trackedMagnetPeer(t, "interrupted.bin", payload, false, limit)
	queued, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	list := func() ([]download.Item, error) { return c.List(ctx) }
	waitForMagnetState(t, ctx, list, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Phase == download.PhaseTransferring && item.Completed >= 4*pieceLength
	})
	seeder.DisallowDataUpload()
	failed := waitForMagnetTask(t, ctx, list, queued.ID)
	assertMagnetStall(t, failed, "transfer")
	if failed.Completed < 4*pieceLength || failed.Completed >= failed.Total || failed.Total != int64(len(payload)) {
		t.Fatalf("stalled transfer should retain its verified progress: %+v", failed)
	}
	if _, err := os.Stat(failed.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stalled transfer should leave its final path unpublished: %v", err)
	}

	trackerOnline.Store(false)
	if err := c.Retry(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	waitForMagnetState(t, ctx, list, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Phase == download.PhaseWaitingPeers && item.Completed >= 4*pieceLength
	})
	assertMagnetStall(t, waitForMagnetTask(t, ctx, list, queued.ID), "peers")

	stats := seeder.Stats()
	before := stats.BytesWrittenData.Int64()
	limit.SetLimit(rate.Inf)
	seeder.AllowDataUpload()
	trackerOnline.Store(true)
	if err := c.Retry(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, list, queued.ID)
	got, err := os.ReadFile(done.Destination)
	if done.Status != download.StatusCompleted || done.ID != queued.ID || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("retried transfer output mismatch: %+v, %d bytes, %v", done, len(got), err)
	}
	stats = seeder.Stats()
	if sent := stats.BytesWrittenData.Int64() - before; sent >= int64(len(payload))-2*pieceLength {
		t.Fatalf("retry downloaded %d bytes and did not reuse the verified payload", sent)
	}
}

func assertMagnetStall(t *testing.T, item download.Item, reason string) {
	t.Helper()
	wantFailure := map[string]download.Failure{
		"metadata": download.FailureMagnetMetadataStalled,
		"peers":    download.FailureMagnetPeersStalled,
		"transfer": download.FailureMagnetTransferStalled,
	}[reason]
	if item.Status != download.StatusFailed || item.Failure != wantFailure ||
		!strings.Contains(strings.ToLower(item.Error), reason) || !strings.Contains(strings.ToLower(item.Error), "retry") {
		t.Fatalf("expected retryable magnet failure mentioning %s: %+v", reason, item)
	}
}

func TestMagnetSlowScheduledPayloadDoesNotStall(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	cfg.MagnetStallTimeout = time.Second
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	now := time.Now()
	prefs.Schedule.SpeedRules = []settings.SpeedRule{{
		From: now.Add(-time.Hour).Format("15:04"), To: now.Add(time.Hour).Format("15:04"), Limit: 8 << 10,
	}}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("scheduled fixture data "), 13000)
	item, err := c.Add(ctx, download.Request{URL: trackedMagnetFixture(t, "scheduled.bin", payload, false)})
	if err != nil {
		t.Fatal(err)
	}
	list := func() ([]download.Item, error) { return c.List(ctx) }
	waitForMagnetState(t, ctx, list, item.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Completed >= 224<<10
	})
	select {
	case <-time.After(2500 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	items, err := list()
	if err != nil || len(items) != 1 || items[0].Status != download.StatusRunning {
		t.Fatalf("scheduled throttling interrupted the active transfer: %+v, %v", items, err)
	}
	prefs.Schedule.SpeedRules[0].Limit = 8 << 20
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	if done := waitForMagnetTask(t, ctx, list, item.ID); done.Status != download.StatusCompleted {
		t.Fatalf("active transfer failed after raising the scheduled limit: %+v", done)
	}
}

func TestReaddingMagnetRestoresTrackerHintsAfterKeyLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	cfg.MagnetStallTimeout = time.Second
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	payload := []byte("tracker restored to the existing task")
	link := trackedMagnetFixture(t, "restored.bin", payload, false)
	original, err := c.Add(ctx, download.Request{URL: link})
	if err != nil || original.Phase != download.PhaseProxyBlocked {
		t.Fatalf("initial magnet = %+v, %v", original, err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	// A process without a keyring starts again with a different memory-only key.
	cfg.Sealer, err = secrets.New(bytes.Repeat([]byte{31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	c = magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	list := func() ([]download.Item, error) { return c.List(ctx) }
	if err := c.Update(ctx, original.ID, download.Patch{DirectConnectionAllowed: new(true)}); err != nil {
		t.Fatal(err)
	}
	assertMagnetStall(t, waitForMagnetTask(t, ctx, list, original.ID), "metadata")

	repeated, err := c.Add(ctx, download.Request{URL: link + "&dn=different"})
	if err != nil || repeated.ID != original.ID || repeated.Directory != original.Directory ||
		repeated.Name != original.Name || repeated.Status != download.StatusFailed || !repeated.DirectConnectionAllowed {
		t.Fatalf("re-add did not preserve the task: %+v, %v", repeated, err)
	}
	if err := c.Retry(ctx, original.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, list, original.ID)
	got, err := os.ReadFile(done.Destination)
	if done.Status != download.StatusCompleted || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("retry after restoring tracker hints: %+v, %q, %v", done, got, err)
	}
}
