package service_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
	"github.com/amemiya02/nimget/internal/store"
)

const (
	queueSize       = 2000
	activeDownloads = 4
	queueFileSize   = 16 << 20
)

type queueProgressRunner struct{ idleRunner }

func (queueProgressRunner) Download(ctx context.Context, _ download.Spec, report func(download.Progress)) error {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for done := int64(64 << 10); ; done += 64 << 10 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			report(download.Progress{Completed: done, Total: queueFileSize})
		}
	}
}

func TestLargePersistedQueueTracksProgress(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nimget.db")
	downloadRoot := filepath.Join(dir, "downloads")
	sealer := testSealer(t)
	seed := func() {
		db, err := store.Open(dbPath, sealer)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		prefs := settings.Default()
		prefs.MaxConcurrent = activeDownloads
		if err := db.SaveSettings(ctx, prefs); err != nil {
			t.Fatal(err)
		}
		created := time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC)
		for i := 1; i <= queueSize; i++ {
			id := fmt.Sprintf("task-%04d", i)
			status := download.StatusCompleted
			done := int64(queueFileSize)
			switch {
			case i <= activeDownloads:
				status, done = download.StatusQueued, 0
			case i%10 == 0:
				status, done = download.StatusPaused, queueFileSize/2
			case i%10 == 1:
				status, done = download.StatusFailed, 0
			}
			name := id + ".zip"
			when := created.Add(-time.Duration(i) * time.Second)
			if err := db.Create(ctx, download.Item{
				ID: id, URL: "https://example.test/files/" + name,
				Destination: filepath.Join(downloadRoot, name),
				Status:      status, Total: queueFileSize, Completed: done,
				CreatedAt: when, UpdatedAt: when, Connections: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	seed()

	progress := make(chan download.Event, 64)
	cfg := service.Config{
		Address: "127.0.0.1:0", DatabasePath: dbPath, TokenPath: filepath.Join(dir, "token"),
		DownloadRoots: []string{downloadRoot}, Sealer: sealer, Runner: queueProgressRunner{},
		Publish: func(event download.Event) {
			if event.Type == download.EventProgress {
				select {
				case progress <- event:
				default:
				}
			}
		},
	}
	svc, err := service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}

	observed := make(map[string]download.Event)
	for len(observed) < activeDownloads {
		select {
		case event := <-progress:
			observed[event.Item.ID] = event
		case <-ctx.Done():
			t.Fatalf("received progress from %d of %d persisted downloads", len(observed), activeDownloads)
		}
	}
	token, err := service.ReadToken(cfg.TokenPath)
	if err != nil {
		t.Fatal(err)
	}
	c := client.New("http://"+svc.Address(), token, http.DefaultClient)
	items, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != queueSize {
		t.Fatalf("reopened queue contains %d tasks, want %d", len(items), queueSize)
	}
	got := make(map[string]download.Item, queueSize)
	for _, item := range items {
		got[item.ID] = item
	}
	for i := 1; i <= queueSize; i++ {
		id := fmt.Sprintf("task-%04d", i)
		item, exists := got[id]
		if !exists {
			t.Fatalf("reopened queue is missing %s", id)
		}
		if i <= activeDownloads && (item.Status != download.StatusRunning || observed[id].Item.Completed == 0 || item.Completed < observed[id].Item.Completed) {
			t.Fatalf("%s = %s, %d bytes after a %d-byte progress event", id, item.Status, item.Completed, observed[id].Item.Completed)
		}
	}

	snapshot, sequence, err := svc.Manager().ListSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshotProgress := make(map[string]int64, activeDownloads)
	for _, item := range snapshot {
		if event, ok := observed[item.ID]; ok {
			if event.Sequence <= 0 || event.Sequence >= sequence || item.Completed < event.Item.Completed {
				t.Fatalf("snapshot %d has %d bytes for %s after event %d with %d bytes", sequence, item.Completed, item.ID, event.Sequence, event.Item.Completed)
			}
			snapshotProgress[item.ID] = item.Completed
		}
	}
	if len(snapshotProgress) != activeDownloads {
		t.Fatalf("snapshot contains progress for %d of %d tasks", len(snapshotProgress), activeDownloads)
	}
	for len(snapshotProgress) > 0 {
		select {
		case event := <-progress:
			if done, ok := snapshotProgress[event.Item.ID]; ok && event.Sequence > sequence {
				if event.Item.Completed < done {
					t.Fatalf("event %d has %d bytes for %s after snapshot %d with %d bytes", event.Sequence, event.Item.Completed, event.Item.ID, sequence, done)
				}
				delete(snapshotProgress, event.Item.ID)
			}
		case <-ctx.Done():
			t.Fatalf("missing post-snapshot progress for %d tasks", len(snapshotProgress))
		}
	}

	// Optionally give the browser benchmark the actual reopened service snapshot.
	if path := os.Getenv("NIMGET_QUEUE_FIXTURE"); path != "" {
		data, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
