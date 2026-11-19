package magnet

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/iplist"
	"github.com/anacrolix/torrent/metainfo"
)

func configureDHT(cfg *torrent.ClientConfig, loopbackOnly bool, bootstrap []netip.AddrPort) (<-chan struct{}, context.CancelFunc) {
	cfg.NoDHT = loopbackOnly && len(bootstrap) == 0
	// Run torrent lookups under the individual attempt's cancellation context.
	cfg.PeriodicallyAnnounceTorrentsToDht = false
	if loopbackOnly {
		cfg.IPBlocklist = nonLoopbackIPs{}
	}
	cfg.ConfigureAnacrolixDhtServer = func(server *dht.ServerConfig) {
		server.Passive = true
		server.Conn = discoveryPackets{server.Conn}
	}
	ready := make(chan struct{})
	var nodes []dht.Addr
	cfg.DhtStartingNodes = func(string) dht.StartingNodesGetter {
		return func() ([]dht.Addr, error) {
			select {
			case <-ready:
				return nodes, nil
			default:
				return nil, nil
			}
		}
	}
	if cfg.NoDHT || len(bootstrap) != 0 {
		for _, addr := range bootstrap {
			if addr.IsValid() && addr.Port() != 0 && (!loopbackOnly || addr.Addr().IsLoopback()) {
				nodes = append(nodes, dht.NewAddr(net.UDPAddrFromAddrPort(addr)))
			}
		}
		close(ready)
		return ready, func() {}
	}
	// Resolve off the traversal path so stopping one task need not wait for DNS.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	go func() {
		defer close(ready)
		defer cancel()
		nodes = resolveDHTBootstrap(ctx)
	}()
	return ready, cancel
}

func resolveDHTBootstrap(ctx context.Context) []dht.Addr {
	var workers sync.WaitGroup
	resolved := make(chan []dht.Addr, len(dht.DefaultGlobalBootstrapHostPorts))
	for _, endpoint := range dht.DefaultGlobalBootstrapHostPorts {
		workers.Go(func() {
			host, portText, err := net.SplitHostPort(endpoint)
			if err != nil {
				return
			}
			port, err := strconv.ParseUint(portText, 10, 16)
			if err != nil {
				return
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return
			}
			var addrs []dht.Addr
			for _, ip := range ips {
				addr := netip.AddrPortFrom(ip.Unmap(), uint16(port))
				addrs = append(addrs, dht.NewAddr(net.UDPAddrFromAddrPort(addr)))
			}
			resolved <- addrs
		})
	}
	workers.Wait()
	close(resolved)
	var nodes []dht.Addr
	for group := range resolved {
		nodes = append(nodes, group...)
	}
	return nodes
}

type nonLoopbackIPs struct{}

func (nonLoopbackIPs) Lookup(ip net.IP) (iplist.Range, bool) {
	return iplist.Range{Description: "loopback addresses only"}, !ip.IsLoopback()
}

func (nonLoopbackIPs) NumRanges() int { return 1 }

type discoveryPackets struct{ net.PacketConn }

func (conn discoveryPackets) ReadFrom(packet []byte) (int, net.Addr, error) {
	for {
		n, from, err := conn.PacketConn.ReadFrom(packet)
		if err != nil || validDHTReply(packet[:n]) {
			return n, from, err
		}
	}
}

// Inspect compact addresses before the DHT library decodes them. Its address
// decoder assumes the wire fields contain a complete IP address and port.
func validDHTReply(packet []byte) bool {
	var msg struct {
		Type  string            `bencode:"y"`
		IP    compactDHTAddress `bencode:"ip"`
		Reply struct {
			Peers []compactDHTAddress `bencode:"values"`
		} `bencode:"r"`
	}
	if err := bencode.Unmarshal(packet, &msg); err != nil {
		if _, trailing := err.(bencode.ErrUnusedTrailingBytes); !trailing {
			return false
		}
	}
	return msg.Type == "r" || msg.Type == "e"
}

type compactDHTAddress struct{}

func (*compactDHTAddress) UnmarshalBencode(encoded []byte) error {
	var addr string
	if err := bencode.Unmarshal(encoded, &addr); err != nil {
		return err
	}
	if len(addr) != net.IPv4len+2 && len(addr) != net.IPv6len+2 {
		return errors.New("invalid compact DHT address")
	}
	return nil
}

func lookupDHT(ctx context.Context, server *dht.Server, tor *torrent.Torrent, hash metainfo.Hash, ready <-chan struct{}) {
	select {
	case <-ready:
	case <-ctx.Done():
		return
	}
	for ctx.Err() == nil {
		if info := tor.Info(); info != nil && info.Private != nil && *info.Private {
			return
		}
		// get_peers can discover metadata before the private flag is known.
		// Do not publish our address via announce_peer.
		lookup, err := server.AnnounceTraversal(hash)
		if err == nil {
			consumeDHTPeers(ctx, tor, lookup)
		}
		timer := time.NewTimer(30 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func consumeDHTPeers(ctx context.Context, tor *torrent.Torrent, lookup *dht.Announce) {
	defer func() {
		lookup.Close()
		for range lookup.Peers {
			// Wait for traversal to finish before dropping the torrent.
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case result, ok := <-lookup.Peers:
			if !ok || ctx.Err() != nil {
				return
			}
			if info := tor.Info(); info != nil && info.Private != nil && *info.Private {
				return
			}
			peers := make([]torrent.PeerInfo, 0, min(len(result.Peers), 200))
			for _, peer := range result.Peers {
				addr := validPeerAddress(peer.IP, peer.Port)
				if addr == nil {
					continue
				}
				peers = append(peers, torrent.PeerInfo{Addr: addr, Source: torrent.PeerSourceDhtGetPeers})
				if len(peers) == cap(peers) {
					break
				}
			}
			tor.AddPeers(peers)
		}
	}
}
