package service_test

import (
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	peerstore "github.com/anacrolix/dht/v2/peer-store"
)

func TestTrackerlessMagnetUsesDHTAfterNetworkGateOpens(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	payload := bytes.Repeat([]byte("trackerless fixture"), 8000)
	hash, seeder, address := seedMagnetPeer(t, "trackerless.bin", payload, false, nil)
	var requests atomic.Int32
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	bootstrap, err := dht.NewServer(&dht.ServerConfig{
		Conn:      conn,
		PeerStore: dhtFixturePeerStore{hash, krpc.NodeAddr{IP: address.IP, Port: address.Port}},
		OnQuery: func(msg *krpc.Msg, _ net.Addr) bool {
			if msg.Q == "get_peers" && msg.A != nil && msg.A.InfoHash == krpc.ID(hash) {
				requests.Add(1)
			}
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bootstrap.Close)
	cfg := handoffServiceConfig(t)
	cfg.MagnetDHTBootstrap = []netip.AddrPort{conn.LocalAddr().(*net.UDPAddr).AddrPort()}
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	item, err := c.Add(ctx, download.Request{URL: "magnet:?xt=urn:btih:" + hash.HexString()})
	if err != nil {
		t.Fatal(err)
	}
	list := func() ([]download.Item, error) { return c.List(ctx) }
	waitForMagnetState(t, ctx, list, item.ID, func(item download.Item) bool {
		return item.Status == download.StatusQueued && item.Phase == download.PhaseProxyBlocked
	})
	select {
	case <-time.After(250 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if requests.Load() != 0 || seeder.Stats().ActivePeers != 0 {
		t.Fatal("proxy-blocked task sent discovery or peer traffic")
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, list, item.ID)
	if done.Status != download.StatusCompleted || requests.Load() == 0 {
		t.Fatalf("trackerless task used %d DHT lookups and reached %+v", requests.Load(), done)
	}
	got, err := os.ReadFile(done.Destination)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("trackerless output mismatch: %d bytes, %v", len(got), err)
	}
}

type dhtFixturePeerStore struct {
	hash peerstore.InfoHash
	peer krpc.NodeAddr
}

func (dhtFixturePeerStore) AddPeer(peerstore.InfoHash, krpc.NodeAddr) {}

func (s dhtFixturePeerStore) GetPeers(hash peerstore.InfoHash) []krpc.NodeAddr {
	if hash != s.hash {
		return nil
	}
	return []krpc.NodeAddr{s.peer}
}
