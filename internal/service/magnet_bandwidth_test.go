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
	"github.com/amemiya02/nimget/internal/settings"
	"github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func TestTorrentUploadLimitSurvivesServiceRestart(t *testing.T) {
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	ctx := t.Context()

	prefs, err := c.Settings(ctx)
	if err != nil || prefs.TorrentUploadLimit != 64<<10 {
		t.Fatalf("default torrent upload limit = %d, %v; want 64 KiB/s", prefs.TorrentUploadLimit, err)
	}
	prefs.TorrentUploadLimit = -1
	if _, err := c.PutSettings(ctx, prefs); err == nil || !strings.Contains(err.Error(), "torrentUploadLimit") {
		t.Fatalf("negative upload limit was not rejected: %v", err)
	}
	prefs.TorrentUploadLimit = 32 << 10
	if saved, err := c.PutSettings(ctx, prefs); err != nil || saved.TorrentUploadLimit != 32<<10 {
		t.Fatalf("saved upload limit = %d, %v; want 32 KiB/s", saved.TorrentUploadLimit, err)
	}
	// Older settings clients do not send an upload limit.
	prefs.TorrentUploadLimit = 0
	prefs.MaxConcurrent = 4
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	svc = startHandoffService(t, cfg)
	prefs, err = magnetClient(t, svc, cfg.TokenPath).Settings(ctx)
	if err != nil || prefs.TorrentUploadLimit != 32<<10 || prefs.MaxConcurrent != 4 {
		t.Fatalf("settings after legacy update and restart: %+v, %v", prefs, err)
	}
}

func TestMixedQueueSharesLiveScheduledDownloadBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 14*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte{0xb1}, 2<<20)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "http.bin", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(source.Close)
	link := trackedMagnetFixture(t, "torrent.bin", payload, false)
	cfg := handoffServiceConfig(t)
	cfg.Runner = nil // Use the same HTTP engine as the running service.
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	now := time.Now()
	const plannedRate = 256 << 10
	prefs.Schedule.SpeedRules = []settings.SpeedRule{{
		From: now.Add(-time.Hour).Format("15:04"), To: now.Add(time.Hour).Format("15:04"), Limit: plannedRate,
	}}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	magnet, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	// Start the web transfer after the torrent has connected and begun
	// consuming the shared bucket, so the test measures payload on both.
	waitForBandwidth(t, ctx, "initial torrent payload", func() bool {
		items, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(items) == 1 && items[0].Completed > 0
	})
	web, err := c.Add(ctx, download.Request{URL: source.URL + "/http.bin", Connections: 1})
	if err != nil {
		t.Fatal(err)
	}
	waitForBandwidth(t, ctx, "overlapping HTTP and BitTorrent payload", func() bool {
		items, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return len(items) == 2 && items[0].Completed > 0 && items[1].Completed > 0
	})
	sample := time.NewTimer(time.Until(start.Add(2 * time.Second)))
	defer sample.Stop()
	select {
	case <-sample.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	items, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var received int64
	for _, item := range items {
		if item.Status != download.StatusRunning || item.Completed <= 0 {
			t.Fatalf("expected concurrent payload under the scheduled limit: %+v", items)
		}
		received += item.Completed
	}
	const initialBurst = 256 << 10
	if budget := initialBurst + int64(plannedRate*time.Since(start).Seconds()); received > budget {
		t.Fatalf("both transfers received %d bytes in %v, exceeding their shared %d-byte budget", received, time.Since(start), budget)
	}

	prefs.Schedule.SpeedRules[0].Limit = 8 << 20
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	faster, cancelFaster := context.WithTimeout(ctx, 4*time.Second)
	defer cancelFaster()
	for _, id := range []string{web.ID, magnet.ID} {
		done := waitForMagnetTask(t, faster, func() ([]download.Item, error) { return c.List(faster) }, id)
		if done.Status != download.StatusCompleted || done.Completed != int64(len(payload)) {
			t.Fatalf("active transfer did not finish at the new scheduled rate: %+v", done)
		}
	}
}

func TestActiveMagnetsShareUploadBudgetAndDisconnect(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 14*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	prefs.TorrentUploadLimit = 48 << 10
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	var links []string
	var observers []*torrent.Torrent
	for _, name := range []string{"pause.bin", "complete.bin", "exit.bin"} {
		link, observer := exchangeFixture(t, ctx, name)
		links = append(links, link)
		observers = append(observers, observer)
	}
	start := time.Now()
	var tasks []download.Item
	for _, link := range links {
		item, err := c.Add(ctx, download.Request{URL: link})
		if err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, item)
	}
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		check, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if items, err := c.List(check); err == nil {
			t.Logf("task states: %+v", items)
		}
		for i, observer := range observers {
			stats := observer.Stats()
			t.Logf("observer %d received %d payload bytes with %d active peers", i, stats.BytesReadUsefulData.Int64(), stats.ActivePeers)
		}
	})
	const initialPerPeer = 64 << 10
	for i, observer := range observers {
		waitForBandwidth(t, ctx, fmt.Sprintf("magnet upload to peer %d", i), func() bool {
			stats := observer.Stats()
			return stats.BytesReadUsefulData.Int64() >= initialPerPeer
		})
	}
	if elapsed := time.Since(start); elapsed < 3400*time.Millisecond {
		t.Fatalf("three peers each received 64 KiB in %v with a shared 48 KiB/s cap", elapsed)
	}

	prefs.TorrentUploadLimit = 1
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	for _, observer := range observers {
		observer.DownloadAll()
	}
	// Each observer now has more to request. The previous rate would allow
	// more than one burst in this interval, even with a fresh empty bucket.
	lowRateSample := time.NewTimer(750 * time.Millisecond)
	defer lowRateSample.Stop()
	select {
	case <-lowRateSample.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var uploaded int64
	for _, observer := range observers {
		stats := observer.Stats()
		uploaded += stats.BytesReadUsefulData.Int64()
	}
	if uploaded > 3*initialPerPeer+16<<10 {
		t.Fatalf("peers received %d additional bytes at the new 1 B/s cap", uploaded-3*initialPerPeer)
	}

	pauseCtx, stopPause := context.WithTimeout(ctx, 2*time.Second)
	defer stopPause()
	if err := c.Pause(pauseCtx, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	waitForBandwidth(t, pauseCtx, "peer disconnect after pause", func() bool {
		return observers[0].Stats().ActivePeers == 0
	})

	observers[1].AllowDataUpload()
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, tasks[1].ID)
	if done.Status != download.StatusCompleted {
		t.Fatalf("task did not complete after the peer shared its last piece: %+v", done)
	}
	waitForBandwidth(t, ctx, "peer disconnect after completion", func() bool {
		return observers[1].Stats().ActivePeers == 0
	})

	if observers[2].Stats().ActivePeers != 1 {
		t.Fatal("shutdown fixture lost its active peer")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- svc.Close() }()
	exitCtx, stopExit := context.WithTimeout(ctx, 2*time.Second)
	defer stopExit()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-exitCtx.Done():
		t.Fatal("service shutdown stalled with a low upload limit")
	}
	waitForBandwidth(t, exitCtx, "peer disconnect after shutdown", func() bool {
		return observers[2].Stats().ActivePeers == 0
	})
}

// Two local peers hold disjoint pieces. The observer advertises the last one
// but withholds it until the test wants the magnet download to finish.
func exchangeFixture(t *testing.T, ctx context.Context, name string) (string, *torrent.Torrent) {
	t.Helper()
	const pieceSize = 32 << 10
	const pieces = 5
	payload := bytes.Repeat([]byte("ab"), pieces*pieceSize/2)
	full := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(full, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: pieceSize}
	if err := info.BuildFromFilePath(full); err != nil {
		t.Fatal(err)
	}
	encoded, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	meta := &metainfo.MetaInfo{InfoBytes: encoded}

	compact := make([]byte, 0, 12)
	var observer *torrent.Torrent
	for _, lastPieceOnly := range []bool{false, true} {
		seedDir := t.TempDir()
		first, end := 0, pieces-1
		if lastPieceOnly {
			first, end = pieces-1, pieces
		}
		partial := make([]byte, len(payload))
		copy(partial[first*pieceSize:end*pieceSize], payload[first*pieceSize:end*pieceSize])
		if err := os.WriteFile(filepath.Join(seedDir, name), partial, 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := torrent.NewDefaultClientConfig().SetListenAddr("127.0.0.1:0")
		cfg.DataDir, cfg.Seed = seedDir, true
		cfg.NoDHT, cfg.NoDefaultPortForwarding, cfg.DisableUTP, cfg.DisableIPv6 = true, true, true, true
		cfg.DisableTrackers, cfg.DisablePEX = true, true
		cfg.Slogger = slog.New(slog.DiscardHandler)
		store := storage.NewFileOpts(storage.NewFileClientOpts{
			ClientBaseDir: seedDir, PieceCompletion: storage.NewMapPieceCompletion(),
			UsePartFiles: generics.Some(false), DisableMmap: true,
		})
		t.Cleanup(func() { _ = store.Close() })
		cfg.DefaultStorage = store
		peer, err := torrent.NewClient(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { peer.Close() })
		spec, err := torrent.TorrentSpecFromMetaInfoErr(meta)
		if err != nil {
			t.Fatal(err)
		}
		spec.DisableInitialPieceCheck = true
		tor, _, err := peer.AddTorrentSpec(spec)
		if err != nil {
			t.Fatal(err)
		}
		for i := range pieces {
			if err := tor.Piece(i).VerifyDataContext(ctx); err != nil {
				t.Fatal(err)
			}
			if want := i >= first && i < end; tor.PieceState(i).Complete != want {
				t.Fatalf("fixture %s piece %d completion differs from %t", name, i, want)
			}
		}
		if lastPieceOnly {
			observer = tor
			observer.DisallowDataUpload()
			observer.DownloadPieces(0, 2)
		}
		address := peer.ListenAddrs()[0].(*net.TCPAddr)
		compact = binary.BigEndian.AppendUint16(append(compact, address.IP.To4()...), uint16(address.Port))
	}
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(announce)
	}))
	t.Cleanup(tracker.Close)
	link := fmt.Sprintf("magnet:?xt=urn:btih:%s&tr=%s", meta.HashInfoBytes().HexString(), url.QueryEscape(tracker.URL))
	return link, observer
}

func waitForBandwidth(t *testing.T, ctx context.Context, description string, reached func() bool) {
	t.Helper()
	tick := time.NewTicker(15 * time.Millisecond)
	defer tick.Stop()
	for !reached() {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %s: %v", description, ctx.Err())
		case <-tick.C:
		}
	}
}
