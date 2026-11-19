package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/testfixture"
)

func TestCLICompletesMagnetAlongsideUpgradedHTTPTask(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	dir := t.TempDir()
	root := filepath.Join(dir, "downloads")
	webPath := filepath.Join(root, "existing.txt")
	webBytes := []byte("HTTP payload from before the upgrade")
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "existing.txt", time.Time{}, bytes.NewReader(webBytes))
	}))
	t.Cleanup(web.Close)

	magnetBytes := bytes.Repeat([]byte("NimGet CLI fixture\n"), 4000)
	link := testfixture.TrackedV1Magnet(t, "magnet.txt", magnetBytes)
	dbPath, tokenPath := filepath.Join(dir, "nimget.db"), filepath.Join(dir, "token")
	writeLegacyHTTPQueue(t, dbPath, web.URL+"/existing.txt", webPath)

	sealer, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		Address: "127.0.0.1:0", DatabasePath: dbPath, TokenPath: tokenPath,
		DownloadRoots: []string{root}, Sealer: sealer, MagnetLoopbackOnly: true,
	})
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
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + svc.Address()
	c := client.New(address, token, nil)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) []byte {
		t.Helper()
		cmd := nimget(append([]string{"--address", address, "--token-file", tokenPath}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("nimget %s: %v: %s", args[0], err, out)
		}
		return out
	}
	const legacyID = "legacy-http"
	magnetDir := filepath.Join(root, "torrents")
	var submitted download.Item
	if err := json.Unmarshal(run("add", link, "--dir", magnetDir), &submitted); err != nil {
		t.Fatal(err)
	}
	if submitted.ID == "" || submitted.SourceKind != download.SourceMagnet || submitted.Phase != download.PhaseProxyBlocked {
		t.Fatalf("magnet submission = %+v", submitted)
	}
	var mixed []download.Item
	if err := json.Unmarshal(run("list"), &mixed); err != nil {
		t.Fatal(err)
	}
	if len(mixed) != 2 || mixed[0].ID != legacyID || mixed[0].Status != download.StatusPaused || mixed[0].SourceKind != "" || mixed[1].ID != submitted.ID {
		t.Fatalf("upgraded mixed queue = %+v", mixed)
	}
	run("resume", legacyID)
	run("set", submitted.ID, "--allow-direct")

	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		items, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var completed int
		for _, item := range items {
			if item.Status == download.StatusFailed {
				t.Fatalf("mixed task failed: %+v", item)
			}
			if item.Status == download.StatusCompleted {
				completed++
			}
		}
		if len(items) == 2 && completed == 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("mixed queue did not finish: %v, %+v", ctx.Err(), items)
		case <-tick.C:
		}
	}

	for path, want := range map[string][]byte{
		webPath:                                webBytes,
		filepath.Join(magnetDir, "magnet.txt"): magnetBytes,
	} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, want) {
			t.Errorf("%s contains %d bytes, want %d: %v", path, len(data), len(want), err)
		}
	}
}

func writeLegacyHTTPQueue(t *testing.T, dbPath, source, destination string) {
	t.Helper()
	// Version zero is the oldest supported on-disk queue, with no source kind.
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(
		`CREATE TABLE downloads (
            id TEXT PRIMARY KEY, url TEXT NOT NULL, destination TEXT NOT NULL, status TEXT NOT NULL,
            completed INTEGER NOT NULL DEFAULT 0, total INTEGER NOT NULL DEFAULT 0,
            error TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL
        )`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		`INSERT INTO downloads (id, url, destination, status, created_at, updated_at)
          VALUES ('legacy-http', ?, ?, 'paused', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		source, destination,
	); err != nil {
		t.Fatal(err)
	}
}
