// Package testfixture builds controlled, loopback-only integration fixtures.
package testfixture

import (
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

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/merkle"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// TrackedV1Magnet serves a single verified file through a local tracker and peer.
func TrackedV1Magnet(t *testing.T, name string, payload []byte) string {
	t.Helper()
	hash, _, address := SeedV1Magnet(t, name, payload, false, nil)
	compact := binary.BigEndian.AppendUint16(append([]byte(nil), address.IP.To4()...), uint16(address.Port))
	announce, err := bencode.Marshal(map[string]any{"interval": 30, "peers": string(compact)})
	if err != nil {
		t.Fatal(err)
	}
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(announce)
	}))
	t.Cleanup(tracker.Close)
	return "magnet:?xt=urn:btih:" + hash.HexString() + "&tr=" + url.QueryEscape(tracker.URL)
}

// SeedV1Magnet runs a peer that verifies the fixture before returning.
func SeedV1Magnet(t *testing.T, name string, payload []byte, private bool, limit *rate.Limiter) (metainfo.Hash, *torrent.Torrent, *net.TCPAddr) {
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
	if limit != nil {
		seedConfig.UploadRateLimiter = limit
	}
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
	return ti.HashInfoBytes(), seeder, seed.ListenAddrs()[0].(*net.TCPAddr)
}

type V2MagnetFixture struct {
	V1      string
	V2      string
	tracker string
}

func (f V2MagnetFixture) Link(topics string) string {
	return "magnet:?xt=" + topics + "&tr=" + url.QueryEscape(f.tracker)
}

func ServeV2Magnet(t *testing.T, name string, payload []byte, hybrid bool, beforeReply func(*http.Request)) V2MagnetFixture {
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
	return V2MagnetFixture{V1: "urn:btih:" + meta.HashInfoBytes().HexString(), V2: "urn:btmh:1220" + hex.EncodeToString(v2[:]), tracker: tracker.URL}
}
