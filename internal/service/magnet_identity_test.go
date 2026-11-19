package service_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
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

func assertMagnetPayload(t *testing.T, item download.Item, destination string, payload []byte) {
	t.Helper()
	if item.Status != download.StatusCompleted || item.Destination != destination || item.Completed != int64(len(payload)) || item.Total != item.Completed {
		t.Fatalf("expected verified torrent at %q: %+v", destination, item)
	}
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
