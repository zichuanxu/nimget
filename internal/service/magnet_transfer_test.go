package service_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/testfixture"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"golang.org/x/time/rate"
)

func TestTrackedMagnetPublishesVerifiedBytesAtFreeDestination(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	settings, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	settings.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.DownloadRoots[0], "chosen")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("controlled BitTorrent fixture "), 5000)
	link := trackedMagnetFixture(t, "fixture.txt", payload, false)
	existing := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(existing, []byte("existing document"), 0o600); err != nil {
		t.Fatal(err)
	}
	queued, err := c.Add(ctx, download.Request{URL: link, Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	if queued.Status != download.StatusQueued || queued.Total > 0 || queued.Destination != "" {
		t.Fatalf("Add should return before metadata discovery: %+v", queued)
	}

	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, queued.ID)
	want := filepath.Join(dir, "fixture (1).txt")
	if done.Status != download.StatusCompleted || done.Completed != int64(len(payload)) || done.Total != done.Completed {
		t.Fatalf("expected verified completion at %s: %+v", want, done)
	}
	assertMagnetOutputPath(t, want, done.Destination)
	got, err := os.ReadFile(want)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("fixture output mismatch: %d bytes, %v", len(got), err)
	}
	got, err = os.ReadFile(existing)
	if err != nil || string(got) != "existing document" {
		t.Fatalf("existing file lost: %q, %v", got, err)
	}
	assertMagnetQuarantine(t, want)
}

func TestPrivateMagnetFailsWithoutOpeningPayloadStorage(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	var logs bytes.Buffer
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.DownloadRoots[0], "private")
	item, err := c.Add(ctx, download.Request{
		URL:       trackedMagnetFixture(t, "private.bin", []byte("private payload"), true),
		Directory: dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, item.ID)
	if done.Status != download.StatusFailed || !strings.Contains(done.Error, "public magnet") || done.Completed != 0 {
		t.Fatalf("private torrent was not rejected with actionable feedback: %+v", done)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatalf("private metadata opened payload storage: %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sensitive := range []string{"secret-path", "passkey=test-only"} {
		if strings.Contains(logs.String(), sensitive) || strings.Contains(done.Error, sensitive) {
			t.Fatal("tracker credentials escaped into service diagnostics")
		}
	}
}

func trackedMagnetFixture(t *testing.T, name string, payload []byte, private bool) string {
	t.Helper()
	link, _, _ := trackedMagnetPeer(t, name, payload, private, nil)
	return link
}

func trackedMagnetPeer(t *testing.T, name string, payload []byte, private bool, limit *rate.Limiter) (string, *torrent.Torrent, *atomic.Bool) {
	t.Helper()
	hash, seeder, address := testfixture.SeedV1Magnet(t, name, payload, private, limit)
	compact := binary.BigEndian.AppendUint16(append([]byte(nil), address.IP.To4()...), uint16(address.Port))
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	trackerOnline := new(atomic.Bool)
	trackerOnline.Store(true)
	noPeers, err := bencode.Marshal(map[string]any{"interval": 30, "peers": ""})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/announce/secret-path" {
			t.Errorf("unexpected tracker request path")
		}
		if trackerOnline.Load() {
			_, _ = w.Write(announce)
		} else {
			_, _ = w.Write(noPeers)
		}
	}))
	t.Cleanup(tracker.Close)
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=fallback&tr=%s", hash.HexString(), url.QueryEscape(tracker.URL+"/announce/secret-path?passkey=test-only")), seeder, trackerOnline
}

func waitForMagnetTask(t *testing.T, ctx context.Context, list func() ([]download.Item, error), id string) download.Item {
	t.Helper()
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	var last download.Item
	for {
		items, err := list()
		if err != nil {
			t.Fatalf("wait for torrent task %s (last state %+v): %v", id, last, err)
		}
		for _, item := range items {
			if item.ID == id {
				last = item
				if item.Status == download.StatusCompleted || item.Status == download.StatusFailed {
					return item
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("torrent task %s did not finish: %+v", id, last)
		case <-tick.C:
		}
	}
}

func assertMagnetOutputPath(t *testing.T, want, got string) {
	t.Helper()
	wantFile, wantErr := os.Stat(want)
	gotFile, gotErr := os.Stat(got)
	if wantErr != nil || gotErr != nil || !os.SameFile(wantFile, gotFile) || filepath.Base(got) != filepath.Base(want) {
		t.Fatalf("destination = %q; want %q (%v, %v)", got, want, gotErr, wantErr)
	}
}

func assertMagnetQuarantine(t *testing.T, path string) {
	t.Helper()
	// The quarantine package's platform tests verify the metadata format.
	// Exercise the supported readable state at the Windows filesystem seam.
	if os.PathSeparator != '\\' {
		return
	}
	marked, err := os.ReadFile(path + ":Zone.Identifier")
	if err != nil || !bytes.Contains(marked, []byte("ZoneId=3")) || bytes.Contains(marked, []byte("secret-path")) {
		t.Fatalf("quarantine missing or includes tracker credentials: %q, %v", marked, err)
	}
}
