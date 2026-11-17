package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/store"
)

func TestDesktopBulkActionReportsMixedResultsAndKeepsSuccessfulChanges(t *testing.T) {
	d, _ := desktopWithBulkTasks(t, map[string]download.Item{
		"queued":  {Status: download.StatusQueued},
		"paused":  {Status: download.StatusPaused},
		"failed":  {Status: download.StatusFailed},
		"recover": {Status: download.StatusFailed, Recovery: download.RecoveryRestartRequired},
		"done":    {Status: download.StatusCompleted},
	})

	assertBulkResults(t, d, download.TaskActionPause,
		[]string{"done", "missing", "queued", "queued"},
		[]string{"cannot pause", "download not found", ""})
	assertBulkResults(t, d, download.TaskActionResume,
		[]string{"done", "paused", "queued"},
		[]string{"expected a paused download", "", ""})
	assertBulkResults(t, d, download.TaskActionRetry,
		[]string{"recover", "failed", "done"},
		[]string{"restart from the beginning", "", "expected a failed download"})
	assertBulkResults(t, d, download.TaskActionPause,
		[]string{"queued", "paused", "failed"},
		[]string{"", "", ""})

	items, err := d.List()
	if err != nil || len(items) != 5 {
		t.Fatalf("List = %+v, %v", items, err)
	}
	for _, item := range items {
		if item.ID == "done" && item.Status == download.StatusCompleted {
			continue
		}
		if item.ID == "recover" && item.Status == download.StatusFailed && item.Recovery == download.RecoveryRestartRequired {
			continue
		}
		if (item.ID == "paused" || item.ID == "queued" || item.ID == "failed") && item.Status == download.StatusPaused {
			continue
		}
		t.Errorf("unexpected task state after bulk actions: %+v", item)
	}
}

func TestDesktopBulkRemovalSeparatesRecordsAndFiles(t *testing.T) {
	d, dir := desktopWithBulkTasks(t, map[string]download.Item{
		"record":   {Status: download.StatusCompleted},
		"remove":   {Status: download.StatusCompleted},
		"leftover": {Status: download.StatusCompleted},
		"foreign":  {Status: download.StatusFailed},
	})
	for _, name := range []string{"record", "remove", "foreign"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A nonempty directory at the completed task's old file path ensures a
	// portable file-removal failure without relying on OS lock semantics.
	leftover := filepath.Join(dir, "leftover", "owned-by-user")
	if err := os.Mkdir(filepath.Dir(leftover), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leftover, []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}

	assertBulkResults(t, d, download.TaskActionRemoveRecords, []string{"record"}, []string{""})
	assertBulkResults(t, d, download.TaskActionRemoveFiles,
		[]string{"leftover", "remove", "foreign"},
		[]string{"download deleted, but removing its files failed", "", ""})
	if items, err := d.List(); err != nil || len(items) != 0 {
		t.Fatalf("List after removal = %+v, %v; want no records", items, err)
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "record"):  "record",
		filepath.Join(dir, "foreign"): "foreign",
		leftover:                      "user data",
	} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Errorf("remaining file %q = %q, %v; want %q", path, data, err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "remove")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed file should be removed, Stat = %v", err)
	}
}

func assertBulkResults(t *testing.T, d *Desktop, action download.TaskAction, ids, wantErrors []string) {
	t.Helper()
	results, err := d.ApplyTaskAction(action, ids)
	if err != nil || len(results) != len(wantErrors) {
		t.Fatalf("ApplyTaskAction(%q) = %+v, %v; want %d results", action, results, err, len(wantErrors))
	}
	for i, want := range wantErrors {
		got := results[i]
		if got.ID != ids[i] || (want == "" && got.Error != "") || (want != "" && !strings.Contains(got.Error, want)) {
			t.Errorf("ApplyTaskAction(%q) result %d = %+v; want ID %q and error %q", action, i, got, ids[i], want)
		}
	}
}

func desktopWithBulkTasks(t *testing.T, items map[string]download.Item) (*Desktop, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nimget.db")
	sealer, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(dbPath, sealer)
	if err != nil {
		t.Fatal(err)
	}
	for id, item := range items {
		item.ID = id
		item.URL = "https://example.test/" + id
		item.Destination = filepath.Join(dir, id)
		item.CreatedAt, item.UpdatedAt = time.Now(), time.Now()
		if err := db.Create(context.Background(), item); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Use the real service composition (SQLite, manager, engine). Keep the
	// scheduler stopped so requeued tasks do not reach the network.
	svc, err := service.New(service.Config{
		DatabasePath: dbPath, TokenPath: filepath.Join(dir, "token"), DownloadRoots: []string{dir}, Sealer: sealer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	return &Desktop{mgr: svc.Manager()}, dir
}
