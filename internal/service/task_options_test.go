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
	"github.com/amemiya02/nimget/internal/engine"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
)

func TestEditTaskChangesQueueOrderAndConnectionsOnNextAttempt(t *testing.T) {
	payload := bytes.Repeat([]byte("abcd"), 1<<20)
	entered := make(chan string, 10)
	finishBlocker := make(chan struct{})
	failFirstAttempt := make(chan struct{})
	finishSecondAttempt := make(chan struct{})
	var secondAttempt atomic.Bool
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			select {
			case entered <- r.URL.Path:
			case <-r.Context().Done():
				return
			}
			switch r.URL.Path {
			case "/blocker":
				select {
				case <-r.Context().Done():
					return
				case <-finishBlocker:
				}
			case "/edited":
				if secondAttempt.Load() {
					select {
					case <-r.Context().Done():
						return
					case <-finishSecondAttempt:
					}
					break
				}
				select {
				case <-r.Context().Done():
				case <-failFirstAttempt:
					w.WriteHeader(http.StatusGone)
				}
				return
			}
		}
		w.Header().Set("ETag", `"task-test"`)
		http.ServeContent(w, r, "task.bin", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(source.Close)
	ctx, api, dir := taskOptionsService(t)
	add := func(name string, connections int) download.Item {
		t.Helper()
		item, err := api.Add(ctx, download.Request{
			URL: source.URL + name, Connections: connections, Destination: filepath.Join(dir, filepath.Base(name)+".bin"),
		})
		if err != nil {
			t.Fatal(err)
		}
		return item
	}
	wantRequest := func(path string) {
		t.Helper()
		select {
		case got := <-entered:
			if got != path {
				t.Fatalf("next download request = %s, want %s", got, path)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for request to %s", path)
		}
	}
	add("/blocker", 1)
	wantRequest("/blocker")
	older := add("/older", 1)
	edited := add("/edited", 1)
	priority, two := download.PriorityHigh, 2
	if err := api.Update(ctx, edited.ID, download.Patch{Priority: &priority, Connections: &two}); err != nil {
		t.Fatal(err)
	}
	badCount, lowPriority := 33, download.PriorityLow
	if err := api.Update(ctx, edited.ID, download.Patch{Priority: &lowPriority, Connections: &badCount}); err == nil || !strings.Contains(err.Error(), "INVALID_DOWNLOAD") {
		t.Fatalf("invalid combined edit = %v", err)
	}
	close(finishBlocker)
	wantRequest("/edited")
	wantRequest("/edited")

	one := 1
	if err := api.Update(ctx, edited.ID, download.Patch{Connections: &one}); err != nil {
		t.Fatal(err)
	}
	close(failFirstAttempt)
	failed := waitTaskStatus(t, ctx, api, edited.ID, download.StatusFailed)
	if failed.Priority != priority || failed.Connections != one {
		t.Fatalf("settings after failed attempt = priority %d, connections %d", failed.Priority, failed.Connections)
	}
	wantRequest("/older")
	waitTaskStatus(t, ctx, api, older.ID, download.StatusCompleted)
	secondAttempt.Store(true)
	if err := api.Retry(ctx, edited.ID); err != nil {
		t.Fatal(err)
	}
	wantRequest("/edited")
	// The first range is still held by the fixture. Any other range received
	// before releasing it would be running concurrently with the first.
	select {
	case second := <-entered:
		t.Fatalf("next attempt started another request to %s with the only connection in use", second)
	case <-time.After(200 * time.Millisecond):
	}
	close(finishSecondAttempt)
	completed := waitTaskStatus(t, ctx, api, edited.ID, download.StatusCompleted)
	if data, err := os.ReadFile(completed.Destination); err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("retried download has wrong bytes: %v", err)
	}
}

func TestEditTaskSpeedLimitAppliesToRunningDownload(t *testing.T) {
	payload := bytes.Repeat([]byte{'b'}, 2<<20)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if r.Header.Get("Range") == "bytes=0-0" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write(payload[:128<<10])
		w.(http.Flusher).Flush()
		select {
		case started <- struct{}{}:
		case <-r.Context().Done():
			return
		}
		select {
		case <-r.Context().Done():
		case <-release:
			_, _ = w.Write(payload[128<<10:])
		}
	}))
	t.Cleanup(source.Close)
	ctx, api, dir := taskOptionsService(t)
	item, err := api.Add(ctx, download.Request{URL: source.URL + "/capped", Destination: filepath.Join(dir, "capped.bin")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("download did not start")
	}
	waitTaskStatus(t, ctx, api, item.ID, download.StatusRunning)
	cap := int64(1 << 20)
	if err := api.Update(ctx, item.ID, download.Patch{SpeedLimit: &cap}); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	close(release)
	completed := waitTaskStatus(t, ctx, api, item.ID, download.StatusCompleted)
	if completed.SpeedLimit != cap || time.Since(begin) < time.Second {
		t.Fatalf("updated speed cap did not persist or throttle the ongoing transfer: cap=%d, elapsed=%s", completed.SpeedLimit, time.Since(begin))
	}
}

func taskOptionsService(t *testing.T) (context.Context, *client.Client, string) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	runner, err := engine.NewRunner(engine.Config{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, api := startRenewalService(t, ctx, service.Config{
		Address: "127.0.0.1:0", TokenPath: filepath.Join(dir, "token"),
		DatabasePath: filepath.Join(dir, "queue.db"), DownloadRoots: []string{dir},
		Runner: runner, Sealer: testSealer(t),
	})
	prefs := settings.Default()
	prefs.MaxConcurrent = 1
	if _, err := api.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	return ctx, api, dir
}

func waitTaskStatus(t *testing.T, ctx context.Context, api *client.Client, id string, status download.Status) download.Item {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var last download.Item
	for {
		items, err := api.List(ctx)
		if err != nil {
			t.Fatalf("list while waiting for %s (last status %s, error %q): %v", status, last.Status, last.Error, err)
		}
		for _, item := range items {
			if item.ID == id {
				last = item
				if item.Status == status {
					return item
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("task %s did not reach %s (last status %s, error %q)", id, status, last.Status, last.Error)
		case <-ticker.C:
		}
	}
}
