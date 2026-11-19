package service_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
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
	seedDir := t.TempDir()
	source := filepath.Join(seedDir, name)
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 32 << 10, Private: &private}
	if err := info.BuildFromFilePath(source); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	ti := &metainfo.MetaInfo{InfoBytes: encoded}
	seedConfig := torrent.NewDefaultClientConfig().SetListenAddr("127.0.0.1:0")
	seedConfig.DataDir = seedDir
	seedConfig.NoDHT, seedConfig.NoDefaultPortForwarding, seedConfig.DisableUTP, seedConfig.DisableIPv6 = true, true, true, true
	seedConfig.DisableTrackers = true
	seedConfig.Seed = true
	seedConfig.Slogger = slog.New(slog.DiscardHandler)
	seed, err := torrent.NewClient(seedConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seed.Close() })
	seeder, err := seed.AddTorrent(ti)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-seeder.Complete().On():
	case <-time.After(8 * time.Second):
		t.Fatal("fixture peer did not verify its seed file")
	}
	address := seed.ListenAddrs()[0].(*net.TCPAddr)
	compact := binary.BigEndian.AppendUint16(append([]byte(nil), address.IP.To4()...), uint16(address.Port))
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/announce/secret-path" {
			t.Errorf("unexpected tracker request path")
		}
		_, _ = w.Write(announce)
	}))
	t.Cleanup(tracker.Close)
	return fmt.Sprintf("magnet:?xt=urn:btih:%s&dn=fallback&tr=%s", ti.HashInfoBytes().HexString(), url.QueryEscape(tracker.URL+"/announce/secret-path?passkey=test-only"))
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
