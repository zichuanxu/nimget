package service_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
)

func TestTrackedV2AndHybridMagnetDownloads(t *testing.T) {
	for _, kind := range []string{"v2-only", "hybrid"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cfg := handoffServiceConfig(t)
			c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
			prefs, err := c.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
			if _, err := c.PutSettings(ctx, prefs); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("verified torrent fixture\n"), 2316)
			name := "fixture.bin"
			fixture := serveV2MagnetFixture(t, name, payload, kind == "hybrid", nil)
			topics := fixture.v2
			if kind == "hybrid" {
				topics = fixture.v1 + "&xt=" + fixture.v2
			}
			link := fixture.link(topics)
			queue := func() ([]download.Item, error) { return c.List(ctx) }
			first, err := c.Add(ctx, download.Request{URL: link})
			if err != nil {
				t.Fatal(err)
			}
			finished := waitForMagnetTask(t, ctx, queue, first.ID)
			assertMagnetPayload(t, finished, filepath.Join(cfg.DownloadRoots[0], name), payload)
		})
	}
}

func TestHybridMagnetReconcilesRunningAliasesAndCanBeDownloadedAgain(t *testing.T) {
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
	prefs.MaxConcurrent = 3
	prefs.SpeedLimit = 128 << 10
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	requested, allowReply := make(chan struct{}), make(chan struct{})
	var gotRequest sync.Once
	payload := bytes.Repeat([]byte("controlled hybrid payload "), 24000)
	fixture := serveV2MagnetFixture(t, "hybrid.bin", payload, true, func(r *http.Request) {
		gotRequest.Do(func() { close(requested) })
		select {
		case <-allowReply:
		case <-r.Context().Done():
		}
	})
	defer func() {
		select {
		case <-allowReply:
		default:
			close(allowReply)
		}
	}()
	dir := filepath.Join(cfg.DownloadRoots[0], "owner")
	otherDir := filepath.Join(cfg.DownloadRoots[0], "duplicate")
	owner, err := c.Add(ctx, download.Request{URL: fixture.link(fixture.v1), Directory: dir})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-requested:
	case <-ctx.Done():
		t.Fatal("owner did not start tracker discovery")
	}
	duplicate, err := c.Add(ctx, download.Request{URL: fixture.link(fixture.v2), Directory: otherDir})
	if err != nil || duplicate.ID == owner.ID {
		t.Fatalf("unverified aliases should receive distinct IDs: %+v, %v", duplicate, err)
	}
	// Keep the 20 bytes used for discovery and change the final hexadecimal
	// digit. A short-hash match cannot prove the second claim.
	wrongDigit := "0"
	if strings.HasSuffix(fixture.v2, wrongDigit) {
		wrongDigit = "1"
	}
	falseV2 := fixture.v2[:len(fixture.v2)-1] + wrongDigit
	conflict, err := c.Add(ctx, download.Request{URL: fixture.link(fixture.v1 + "&xt=" + falseV2), Directory: otherDir})
	if err != nil || conflict.ID == owner.ID || conflict.ID == duplicate.ID {
		t.Fatalf("conflicting claims were merged before verification: %+v, %v", conflict, err)
	}
	queue := func() ([]download.Item, error) { return c.List(ctx) }
	waitForMagnetState(t, ctx, queue, duplicate.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning
	})
	close(allowReply)

	stopped := waitForMagnetTask(t, ctx, queue, duplicate.ID)
	if stopped.Status != download.StatusFailed || stopped.DuplicateOf != owner.ID || !strings.Contains(stopped.Error, owner.ID) || stopped.Destination != "" || stopped.Completed != 0 {
		t.Fatalf("expected duplicate to point to owner before writing: %+v", stopped)
	}
	mismatch := waitForMagnetTask(t, ctx, queue, conflict.ID)
	if mismatch.Status != download.StatusFailed || mismatch.DuplicateOf != "" || mismatch.Destination != "" || !strings.Contains(mismatch.Error, "info hashes") {
		t.Fatalf("conflicting full hash was not kept separate: %+v", mismatch)
	}
	if err := c.Retry(ctx, duplicate.ID); err == nil {
		t.Fatal("duplicate could retry as a separate download")
	}
	if _, err := os.Stat(otherDir); !os.IsNotExist(err) {
		t.Fatalf("duplicate touched its save directory: %v", err)
	}
	active := waitForMagnetState(t, ctx, queue, owner.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Completed > 0
	})
	for _, topic := range []string{fixture.v1, fixture.v2, fixture.v1 + "&xt=" + fixture.v2} {
		match, err := c.Add(ctx, download.Request{URL: fixture.link(topic) + "&dn=another", Directory: otherDir})
		if err != nil || match.ID != owner.ID || match.Directory != owner.Directory || match.Destination != active.Destination || match.Completed < active.Completed {
			t.Fatalf("repeat submission lost the active owner or its progress: %+v, %v", match, err)
		}
	}

	prefs.SpeedLimit = 0
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, queue, owner.ID)
	assertMagnetPayload(t, done, filepath.Join(dir, "hybrid.bin"), payload)
	again, err := c.Add(ctx, download.Request{URL: fixture.link(fixture.v2), Directory: dir})
	if err != nil || again.ID == owner.ID || again.ID == duplicate.ID {
		t.Fatalf("completed torrent did not receive a new task: %+v, %v", again, err)
	}
	done = waitForMagnetTask(t, ctx, queue, again.ID)
	assertMagnetPayload(t, done, filepath.Join(dir, "hybrid (1).bin"), payload)
}

func TestConcurrentEquivalentMagnetSubmissionsShareOriginalDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	const v1 = "urn:btih:0123456789abcdef0123456789abcdef01234567"
	hash, err := hex.DecodeString(strings.TrimPrefix(v1, "urn:btih:"))
	if err != nil {
		t.Fatal(err)
	}
	base32Topic := "urn:btih:" + base32.StdEncoding.EncodeToString(hash)
	type result struct {
		item download.Item
		err  error
	}
	const count = 12
	start, results := make(chan struct{}), make(chan result, count)
	for i := range count {
		go func() {
			<-start
			topic := v1
			if i%2 != 0 {
				topic = base32Topic
			}
			dir := filepath.Join(cfg.DownloadRoots[0], string(rune('A'+i)))
			link := "magnet:?xt=" + topic + "&dn=" + filepath.Base(dir) + "&tr=https%3A%2F%2F" + filepath.Base(dir) + ".invalid%2Fannounce"
			item, err := c.Add(ctx, download.Request{URL: link, Directory: dir})
			results <- result{item, err}
		}()
	}
	close(start)
	first := <-results
	if first.err != nil || first.item.Directory == "" {
		t.Fatalf("first add: %+v", first)
	}
	for range count - 1 {
		next := <-results
		if next.err != nil || next.item.ID != first.item.ID || next.item.Directory != first.item.Directory || next.item.Name != first.item.Name {
			t.Fatalf("equivalent submissions disagreed: %+v vs %+v", first, next)
		}
	}
	items, err := c.List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != first.item.ID {
		t.Fatalf("expected one unfinished owner: %+v, %v", items, err)
	}
}

func waitForMagnetState(t *testing.T, ctx context.Context, list func() ([]download.Item, error), id string, ready func(download.Item) bool) download.Item {
	t.Helper()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	var last download.Item
	for {
		items, err := list()
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID == id {
				last = item
				if ready(item) {
					return item
				}
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("torrent task %s did not reach expected state: %+v", id, last)
		case <-ticker.C:
		}
	}
}

func assertMagnetPayload(t *testing.T, item download.Item, destination string, payload []byte) {
	t.Helper()
	if item.Status != download.StatusCompleted || item.Completed != int64(len(payload)) || item.Total != item.Completed {
		t.Fatalf("expected verified torrent at %q: %+v", destination, item)
	}
	assertMagnetOutputPath(t, destination, item.Destination)
	got, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("torrent output mismatch: %d bytes, %v", len(got), err)
	}
}

type v2MagnetFixture struct {
	v1      string
	v2      string
	tracker string
}

func (f v2MagnetFixture) link(topics string) string {
	return "magnet:?xt=" + topics + "&tr=" + url.QueryEscape(f.tracker)
}

func serveV2MagnetFixture(t *testing.T, name string, payload []byte, hybrid bool, beforeReply func(*http.Request)) v2MagnetFixture {
	t.Helper()
	const pieceSize = 16 << 10
	info := metainfo.Info{PieceLength: pieceSize, Name: name, MetaVersion: 2}
	var hashes [][32]byte
	var layer []byte
	for chunk := range slices.Chunk(payload, pieceSize) {
		sum := sha256.Sum256(chunk)
		hashes = append(hashes, sum)
		layer = append(layer, sum[:]...)
		if hybrid {
			v1 := sha1.Sum(chunk)
			info.Pieces = append(info.Pieces, v1[:]...)
		}
	}
	if hybrid {
		info.Length = int64(len(payload))
	}
	root := merkle.RootWithPadHash(hashes, metainfo.HashForPiecePad(pieceSize))
	info.FileTree.Dir = map[string]metainfo.FileTree{
		name: {File: metainfo.FileTreeFile{Length: int64(len(payload)), PiecesRoot: string(root[:])}},
	}
	raw, err := bencode.Marshal(&info)
	if err != nil {
		t.Fatal(err)
	}
	meta := &metainfo.MetaInfo{InfoBytes: raw, PieceLayers: map[string]string{string(root[:]): string(layer)}}

	dir := t.TempDir()
	folder := filepath.Join(dir, name)
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, name), payload, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig().SetListenAddr("127.0.0.1:0")
	cfg.DataDir, cfg.Seed = dir, true
	cfg.NoDHT, cfg.NoDefaultPortForwarding, cfg.DisableUTP, cfg.DisableIPv6 = true, true, true, true
	cfg.DisableTrackers = true
	cfg.Slogger = slog.New(slog.DiscardHandler)
	seed, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { seed.Close() })
	seeder, err := seed.AddTorrent(meta)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-seeder.Complete().On():
	case <-time.After(8 * time.Second):
		t.Fatal("fixture peer did not verify its seed file")
	}
	addr := seed.ListenAddrs()[0].(*net.TCPAddr)
	compact := binary.BigEndian.AppendUint16(append([]byte(nil), addr.IP.To4()...), uint16(addr.Port))
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if beforeReply != nil {
			beforeReply(r)
		}
		_, _ = w.Write(announce)
	}))
	t.Cleanup(tracker.Close)
	v2 := sha256.Sum256(raw)
	return v2MagnetFixture{v1: "urn:btih:" + meta.HashInfoBytes().HexString(), v2: "urn:btmh:1220" + hex.EncodeToString(v2[:]), tracker: tracker.URL}
}
