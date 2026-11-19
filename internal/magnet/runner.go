package magnet

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

type Runner struct {
	mu           sync.Mutex
	client       *torrent.Client
	active       int
	limit        *rate.Limiter
	loopbackOnly bool
	metadata     chan struct{}
}

func NewRunner(limit *rate.Limiter, loopbackOnly bool) *Runner {
	return &Runner{limit: limit, loopbackOnly: loopbackOnly, metadata: make(chan struct{}, 1)}
}

func (r *Runner) acquire() (*torrent.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		cfg := torrent.NewDefaultClientConfig()
		cfg.ListenPort = 0
		if r.loopbackOnly {
			cfg.SetListenAddr("127.0.0.1:0")
			cfg.DisableIPv6 = true
		}
		cfg.NoDHT = true
		cfg.DisableTrackers = true
		cfg.NoDefaultPortForwarding = true
		cfg.DisableWebtorrent = true
		cfg.DisableWebseeds = true
		cfg.NoUpload = true
		cfg.DefaultStorage = &fileStorage{}
		cfg.DownloadRateLimiter = r.limit
		// Tracker URLs and transport errors can contain private passkeys.
		cfg.Slogger = slog.New(slog.DiscardHandler)
		client, err := torrent.NewClient(cfg)
		if err != nil {
			return nil, errors.New("could not start BitTorrent networking")
		}
		r.client = client
	}
	r.active++
	return r.client, nil
}

func (r *Runner) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.active == 0 {
		r.client.Close()
		r.client = nil
	}
}

// Run retrieves and verifies a torrent. prepare validates and reserves the
// output before storage is opened. publish runs with payload handles closed
// and the torrent disconnected.
func (r *Runner) Run(ctx context.Context, source download.Magnet,
	claimOwnership func(download.TorrentIdentity) error,
	prepare func(name string, total int64) (*os.Root, error),
	report func(download.Phase, download.Progress),
	publish func(root *os.Root, staged string) error,
) error {
	if len(source.Trackers) == 0 {
		return errors.New("tracker information is unavailable; remove this task and add the magnet again")
	}
	opts, announceHashes, err := identityOptions(source.TorrentIdentity)
	if err != nil {
		return err
	}
	// The client's short-hash table can overwrite an existing torrent if two
	// unresolved claims later turn out to be aliases. Hold this gate until
	// the metadata and queue ownership are settled.
	select {
	case r.metadata <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	searching := true
	unlockMetadata := func() {
		if searching {
			<-r.metadata
			searching = false
		}
	}
	defer unlockMetadata()
	if err := ctx.Err(); err != nil {
		return err
	}
	client, err := r.acquire()
	if err != nil {
		return err
	}
	defer r.release()
	if err := ctx.Err(); err != nil {
		return err
	}
	tor, added := client.AddTorrentOpt(opts)
	if !added {
		if existingInfo := tor.Info(); existingInfo != nil {
			if _, err := VerifyIdentity(source.TorrentIdentity, existingInfo, tor.Metainfo()); err != nil {
				return err
			}
		}
		return download.ErrDestinationBusy
	}
	tor.DisallowDataDownload()
	tor.DisallowDataUpload()
	var root *os.Root
	var staged *fileStorage
	stopDiscovery := discover(ctx, client, tor, source.Trackers, announceHashes)
	defer func() {
		stopDiscovery()
		tor.Drop()
		if staged != nil {
			_ = staged.file.Close()
			_ = root.Remove(staged.name)
		}
		if root != nil {
			_ = root.Close()
		}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tor.GotInfo():
	}
	info := tor.Info()
	if info.Private != nil && *info.Private {
		return errors.New("private BitTorrent downloads are unsupported; use a public magnet link")
	}
	verified, err := VerifyIdentity(source.TorrentIdentity, info, tor.Metainfo())
	if err != nil {
		return err
	}
	files := info.UpvertedFiles()
	if len(files) != 1 || files[0].Length < 0 {
		return errors.New("this magnet requires a public single-file torrent")
	}
	if info.SymlinkPath != nil || info.Attr != "" && info.Attr != "x" {
		return errors.New("torrent metadata does not describe a regular file")
	}
	if err := claimOwnership(verified); err != nil {
		return err
	}
	unlockMetadata()
	name := info.BestName()
	if path := files[0].BestPath(); len(path) != 0 {
		name = path[len(path)-1]
	}
	total := files[0].Length
	root, err = prepare(name, total)
	if err != nil {
		return err
	}
	staged, err = newStagedFile(root)
	if err != nil {
		return err
	}
	if err := tor.OpenStorage(ctx, staged); err != nil {
		return errors.New("could not open the staged torrent file")
	}
	tor.AllowDataDownload()
	tor.DownloadAll()
	if err := transfer(ctx, tor, info, total, staged.writeError, report); err != nil {
		return err
	}
	stopDiscovery()
	tor.Drop()
	if err := errors.Join(staged.file.Sync(), staged.file.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish(root, staged.name)
}

func transfer(ctx context.Context, tor *torrent.Torrent, info *metainfo.Info, total int64, writeError <-chan error, report func(download.Phase, download.Progress)) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writeError:
			return err
		case <-tor.Complete().On():
			report(download.PhaseTransferring, download.Progress{Completed: total, Total: total})
			return nil
		case <-ticker.C:
			phase := download.PhaseWaitingPeers
			stats := tor.Stats()
			if stats.ActivePeers > 0 && (stats.ConnectedSeeders > 0 || stats.BytesReadUsefulData.Int64() > 0) {
				phase = download.PhaseTransferring
			}
			report(phase, download.Progress{Completed: verifiedBytes(tor, info), Total: total})
		}
	}
}

func verifiedBytes(tor *torrent.Torrent, info *metainfo.Info) int64 {
	var completed, begin int64
	for _, run := range tor.PieceStateRuns() {
		end := begin + int64(run.Length)
		if run.Ok && run.Complete {
			endOffset := info.TotalLength()
			if end < int64(info.NumPieces()) {
				endOffset = end * info.PieceLength
			}
			completed += endOffset - begin*info.PieceLength
		}
		begin = end
	}
	return completed
}
