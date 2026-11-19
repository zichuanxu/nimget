// Package testfixture builds controlled, loopback-only integration fixtures.
package testfixture

import (
	"encoding/binary"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
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
