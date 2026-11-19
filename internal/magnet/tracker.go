package magnet

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/tracker"
)

// Own tracker request contexts per attempt. The torrent client's built-in
// announcer can keep an in-flight request alive after the torrent is dropped.
func discover(ctx context.Context, client *torrent.Client, tor *torrent.Torrent, hints []string) func() {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	for _, hint := range hints {
		workers.Go(func() { announce(ctx, client, tor, hint) })
	}
	return func() {
		cancel()
		workers.Wait()
	}
}

func announce(ctx context.Context, client *torrent.Client, tor *torrent.Torrent, hint string) {
	peerTracker, err := tracker.NewClient(hint, tracker.NewClientOpts{Slogger: slog.New(slog.DiscardHandler)})
	if err != nil {
		return
	}
	defer peerTracker.Close()
	req := tracker.AnnounceRequest{
		InfoHash: tor.InfoHash(), PeerId: client.PeerID(), Port: uint16(client.LocalPort()),
		NumWant: 100, Left: -1, Event: tracker.Started,
	}
	for ctx.Err() == nil {
		if info := tor.Info(); info != nil {
			req.Downloaded = verifiedBytes(tor, info)
			req.Left = info.Length - req.Downloaded
		}
		attempt, cancel := context.WithTimeout(ctx, tracker.DefaultTrackerAnnounceTimeout)
		reply, err := peerTracker.Announce(attempt, req, tracker.AnnounceOpt{})
		cancel()
		next := time.Minute
		if err == nil && ctx.Err() == nil {
			req.Event = tracker.None
			next = max(30*time.Second, time.Duration(reply.Interval)*time.Second)
			peers := make([]torrent.PeerInfo, 0, min(len(reply.Peers), 200))
			for _, peer := range reply.Peers {
				ip, ok := netip.AddrFromSlice(peer.IP)
				if !ok || ip.IsUnspecified() || ip.IsMulticast() || peer.Port < 1 || peer.Port > 65535 {
					continue
				}
				p := torrent.PeerInfo{Addr: &net.TCPAddr{IP: ip.AsSlice(), Port: peer.Port}, Source: torrent.PeerSourceTracker}
				copy(p.Id[:], peer.ID)
				peers = append(peers, p)
				if len(peers) == cap(peers) {
					break
				}
			}
			tor.AddPeers(peers)
		}
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
