package service_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/service"
)

func TestRenewExpiredDownloadResumesExistingTask(t *testing.T) {
	testSignedURLRenewal(t, `"version-1"`, "", false, 1)
}

func TestRenewExpiredDownloadResumesAfterVerificationCancellation(t *testing.T) {
	testSignedURLRenewal(t, `"version-1"`, "", false, 2)
}

func TestRenewExpiredDownloadRequiresExplicitRestart(t *testing.T) {
	for _, tc := range []struct {
		name, etag, query string
		verifications     int32
	}{
		{name: "changed validator", etag: `"version-2"`},
		{name: "missing validator"},
		{name: "different resource query with same validator", etag: `"version-1"`, query: "&file=other", verifications: 1},
		{name: "different resource in a signing-shaped field", etag: `"version-1"`, query: "&token=other", verifications: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testSignedURLRenewal(t, tc.etag, tc.query, true, tc.verifications)
		})
	}
}

func testSignedURLRenewal(t *testing.T, freshETag, freshQuery string, wantRestart bool, wantVerifications int32) {
	t.Helper()
	payload := bytes.Repeat([]byte("signed download bytes\n"), 16<<10)
	prefix := len(payload) / 2
	pauseAt := prefix + (len(payload)-prefix)/2
	reachedPause := make(chan struct{}, 1)
	verificationStarted := make(chan struct{}, 1)
	cancelFirstVerification := wantVerifications == 2
	freshPayload := payload
	if wantRestart {
		freshPayload = bytes.Repeat([]byte{'x'}, len(payload))
	}
	var expired atomic.Bool
	var verifications atomic.Int32
	var renewedStart atomic.Int64
	renewedStart.Store(-1)
	var resumedStart atomic.Int64
	resumedStart.Store(-1)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		old := r.URL.Query().Get("signature") == "old"
		if old && expired.Load() || (!old && r.Header.Get("Cookie") != "session=fresh") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if old {
			w.Header().Set("ETag", `"version-1"`)
		} else if freshETag != "" {
			w.Header().Set("ETag", freshETag)
		}
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil ||
			start < 0 || end < start || end >= len(payload) {
			t.Errorf("unexpected Range %q", r.Header.Get("Range"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		if end == 0 {
			_, _ = w.Write(payload[:1])
			return
		}
		if old {
			expired.Store(true)
			_, _ = w.Write(payload[:prefix])
			return
		}
		if start == 0 && end < len(payload)-1 {
			if verifications.Add(1) == 1 && cancelFirstVerification {
				_, _ = w.Write(freshPayload[:1])
				w.(http.Flusher).Flush()
				verificationStarted <- struct{}{}
				<-r.Context().Done()
				return
			}
			_, _ = w.Write(freshPayload[:end+1])
			return
		}
		firstRenewed := renewedStart.CompareAndSwap(-1, int64(start))
		if !wantRestart && firstRenewed {
			if start != prefix {
				t.Errorf("renewal started at %d, want %d", start, prefix)
				return
			}
			_, _ = w.Write(freshPayload[start:pauseAt])
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		resumedStart.Store(int64(start))
		_, _ = w.Write(freshPayload[start : end+1])
	}))
	defer remote.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	dest := filepath.Join(dir, "file.bin")
	cfg := service.Config{
		Address: "127.0.0.1:0", TokenPath: filepath.Join(dir, "token"),
		DatabasePath: filepath.Join(dir, "nimget.db"), DownloadRoots: []string{dir},
		Sealer: testSealer(t),
		Publish: func(event download.Event) {
			if event.Item.Status == download.StatusRunning && event.Item.Completed >= int64(pauseAt) {
				select {
				case reachedPause <- struct{}{}:
				default:
				}
			}
		},
	}
	svc, api := startRenewalService(t, ctx, cfg)

	added, err := api.Add(ctx, download.Request{
		URL: remote.URL + "/asset?signature=old", Destination: dest, Connections: 1,
		Headers: map[string]string{"Cookie": "session=old"},
	})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitRenewalTask(t, ctx, api, download.StatusFailed)
	if failed.Completed != int64(prefix) || !strings.Contains(failed.Error, "HTTP 403") {
		t.Fatalf("expired task = %+v, want HTTP 403 after %d bytes", failed, prefix)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := os.ReadFile(dest + ".part.meta")
	if err != nil {
		t.Fatal(err)
	}

	svc, api = startRenewalService(t, ctx, cfg)
	fresh := remote.URL + "/asset?signature=fresh" + freshQuery
	headers := map[string]string{"Cookie": "session=fresh"}
	patch := download.Patch{URL: &fresh, Headers: &headers}
	unauthorized := client.New("http://"+svc.Address(), "", nil)
	if err := unauthorized.Update(ctx, added.ID, patch); err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("unauthorized renewal = %v, want 401 Unauthorized", err)
	}
	invalid := "file:///file.bin"
	if err := api.Update(ctx, added.ID, download.Patch{URL: &invalid}); err == nil || !strings.Contains(err.Error(), "INVALID_DOWNLOAD") {
		t.Fatalf("invalid renewal = %v, want invalid download", err)
	}
	if err := api.Update(ctx, added.ID, patch); err != nil {
		t.Fatal(err)
	}
	if cancelFirstVerification {
		select {
		case <-verificationStarted:
		case <-ctx.Done():
			t.Fatal("saved-byte verification did not begin")
		}
		if err := api.Pause(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
		if current, err := os.ReadFile(dest + ".part.meta"); err != nil || !bytes.Equal(current, checkpoint) {
			t.Fatalf("verification cancellation changed the checkpoint: %v", err)
		}
		svc, api = startRenewalService(t, ctx, cfg)
		paused := waitRenewalTask(t, ctx, api, download.StatusPaused)
		if paused.Completed != int64(prefix) || paused.Recovery != download.RecoveryValidateSource {
			t.Fatalf("paused while verifying = %+v, want original progress", paused)
		}
		if err := api.Resume(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
	}
	expectedOffset := int64(prefix)
	if wantRestart {
		rejected := waitRenewalTask(t, ctx, api, download.StatusFailed)
		if rejected.Recovery != download.RecoveryRestartRequired || rejected.Completed != int64(prefix) ||
			rejected.ID != added.ID || rejected.URL != fresh || rejected.Destination != added.Destination {
			t.Fatalf("incompatible renewal = %+v; want the same task requiring restart", rejected)
		}
		if got := renewedStart.Load(); got != -1 {
			t.Fatalf("renewal downloaded data at %d despite unproven continuity", got)
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("destination exists before restart: %v", err)
		}
		part, err := os.ReadFile(dest + ".part")
		if err != nil || !bytes.Equal(part[:min(len(part), prefix)], payload[:prefix]) {
			t.Fatalf("saved prefix was not preserved: %d bytes, %v", len(part), err)
		}
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
		_, api = startRenewalService(t, ctx, cfg)
		if err := api.Retry(ctx, added.ID); err == nil || !strings.Contains(err.Error(), "RESTART_REQUIRED") {
			t.Fatalf("ordinary retry = %v, want restart required", err)
		}
		if err := api.Restart(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
		expectedOffset = 0
	} else {
		select {
		case <-reachedPause:
		case <-ctx.Done():
			t.Fatal("renewal never made progress")
		}
		if err := api.Pause(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
		_, api = startRenewalService(t, ctx, cfg)
		paused := waitRenewalTask(t, ctx, api, download.StatusPaused)
		if paused.Completed != int64(pauseAt) || paused.URL != fresh || paused.Recovery != download.RecoveryValidateSource {
			t.Fatalf("paused recovery = %+v, want fresh source and %d bytes", paused, pauseAt)
		}
		if err := api.Resume(ctx, added.ID); err != nil {
			t.Fatal(err)
		}
	}
	completed := waitRenewalTask(t, ctx, api, download.StatusCompleted)
	if completed.ID != added.ID || completed.URL != fresh || completed.Destination != added.Destination {
		t.Fatalf("renewed task = %+v, originally %+v", completed, added)
	}
	if got := renewedStart.Load(); got != expectedOffset {
		t.Fatalf("renewed request started at %d, want %d", got, expectedOffset)
	}
	if !wantRestart && resumedStart.Load() != int64(pauseAt) {
		t.Fatalf("request after pause and service restart started at %d, want %d", resumedStart.Load(), pauseAt)
	}
	if got := verifications.Load(); got != wantVerifications {
		t.Fatalf("saved-byte verifications = %d, want %d", got, wantVerifications)
	}
	if data, err := os.ReadFile(dest); err != nil || !bytes.Equal(data, freshPayload) {
		t.Fatalf("completed file = %d bytes, %v", len(data), err)
	}
}

func startRenewalService(t *testing.T, ctx context.Context, cfg service.Config) (*service.Service, *client.Client) {
	t.Helper()
	svc, err := service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	token, err := service.ReadToken(cfg.TokenPath)
	if err != nil {
		t.Fatal(err)
	}
	return svc, client.New("http://"+svc.Address(), token, nil)
}

func waitRenewalTask(t *testing.T, ctx context.Context, api *client.Client, status download.Status) download.Item {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	var last download.Item
	for {
		items, err := api.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 {
			t.Fatalf("tasks = %d, want one", len(items))
		}
		last = items[0]
		if last.Status == download.StatusCompleted && status != download.StatusCompleted {
			t.Fatalf("task completed; want %s: %+v", status, last)
		}
		if last.Status == status {
			return last
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			t.Fatalf("waiting for %s; last task = %+v", status, last)
		}
	}
}

func TestDeleteKeepsRetainedPartFromEarlierTaskAtSameDestination(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("ETag", `"fixture"`)
		w.Header().Set("Content-Range", "bytes 0-0/8")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("a"))
	}))
	defer fixture.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir := t.TempDir()
	cfg := service.Config{
		Address: "127.0.0.1:0", TokenPath: filepath.Join(dir, "token"),
		DatabasePath: filepath.Join(dir, "nimget.db"), DownloadRoots: []string{dir},
		Sealer: testSealer(t),
	}
	_, api := startRenewalService(t, ctx, cfg)
	req := download.Request{URL: fixture.URL, Destination: filepath.Join(dir, "retained.bin")}
	first, err := api.Add(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitRenewalTask(t, ctx, api, download.StatusFailed)
	if err := api.Delete(ctx, first.ID, false); err != nil {
		t.Fatalf("delete original record only: %v", err)
	}
	checkpoint, err := os.ReadFile(req.Destination + ".part.meta")
	if err != nil {
		t.Fatal(err)
	}
	part, err := os.ReadFile(req.Destination + ".part")
	if err != nil {
		t.Fatal(err)
	}

	second, err := api.Add(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waitRenewalTask(t, ctx, api, download.StatusFailed)
	if err := api.Delete(ctx, second.ID, true); err == nil {
		t.Fatal("new task's deletion unexpectedly removed the earlier task's partial")
	}
	for name, want := range map[string][]byte{".part": part, ".part.meta": checkpoint} {
		got, err := os.ReadFile(req.Destination + name)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("retained %s changed: %v", name, err)
		}
	}
}
