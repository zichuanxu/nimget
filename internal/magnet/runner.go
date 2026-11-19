package magnet

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

type Runner struct {
	mu     sync.Mutex
	client *torrent.Client
	active int
	limit  *rate.Limiter
}

func NewRunner(limit *rate.Limiter) *Runner {
	return &Runner{limit: limit}
}

func (r *Runner) acquire() (*torrent.Client, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		cfg := torrent.NewDefaultClientConfig()
		cfg.ListenPort = 0
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
	prepare func(info *metainfo.Info, metadata []byte) (StagedPayload, error),
	report func(download.Phase, download.Progress),
	publish func() error,
) error {
	if len(source.Trackers) == 0 && len(source.Metadata) == 0 {
		return errors.New("tracker information is unavailable; remove this task and add the magnet again")
	}
	decoded, err := hex.DecodeString(source.V1)
	if err != nil || len(decoded) != metainfo.HashSize {
		return errors.New("saved torrent identity is invalid")
	}
	hash := metainfo.Hash(decoded)
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
	spec, err := resumeSpec(hash, source.Metadata)
	if err != nil {
		return err
	}
	tor, added, err := client.AddTorrentSpec(spec)
	if err != nil {
		return errors.New("could not start peer discovery for the magnet")
	}
	if !added {
		return download.ErrDestinationBusy
	}
	tor.DisallowDataDownload()
	tor.DisallowDataUpload()
	stopDiscovery := discover(ctx, client, tor, source.Trackers)
	stop := sync.OnceFunc(func() {
		stopDiscovery()
		tor.Drop()
	})
	defer stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tor.GotInfo():
	}
	info := tor.Info()
	if info.Private != nil && *info.Private {
		return errors.New("private BitTorrent downloads are unsupported; use a public magnet link")
	}
	if !info.HasV1() || info.HasV2() || info.IsDir() || info.Length < 0 {
		return errors.New("this magnet requires a public single-file BitTorrent v1 torrent")
	}
	if info.SymlinkPath != nil || info.Attr != "" && info.Attr != "x" {
		return errors.New("torrent metadata does not describe a regular file")
	}
	meta := tor.Metainfo()
	var encoded bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: meta.InfoBytes, PieceLayers: meta.PieceLayers}).Write(&encoded); err != nil {
		return errors.New("could not save verified torrent metadata")
	}
	staged, err := prepare(info, encoded.Bytes())
	if err != nil {
		return err
	}
	var transferErr error
	switch {
	case ctx.Err() != nil:
		transferErr = ctx.Err()
	default:
		transferErr = downloadPayload(ctx, tor, info, staged, report)
	}
	stop()
	if err := errors.Join(staged.Checkpoint(), staged.Close()); err != nil {
		return errors.Join(transferErr, err)
	}
	if transferErr != nil {
		return transferErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish()
}

func resumeSpec(hash metainfo.Hash, metadata []byte) (*torrent.TorrentSpec, error) {
	spec := &torrent.TorrentSpec{}
	if len(metadata) != 0 {
		saved, err := metainfo.Load(bytes.NewReader(metadata))
		if err != nil || saved.HashInfoBytes() != hash {
			return nil, errors.New("saved torrent metadata does not match its identity")
		}
		if len(saved.PieceLayers) == 0 {
			saved.PieceLayers = nil
		}
		spec, err = torrent.TorrentSpecFromMetaInfoErr(saved)
		if err != nil {
			return nil, errors.New("saved torrent metadata is invalid")
		}
	}
	spec.InfoHash = hash
	spec.DeferStorageOpen = true
	spec.DisableInitialPieceCheck = true
	return spec, nil
}

func downloadPayload(ctx context.Context, tor *torrent.Torrent, info *metainfo.Info, staged StagedPayload, report func(download.Phase, download.Progress)) error {
	if err := tor.OpenStorage(ctx, staged); err != nil {
		return errors.New("could not open the staged torrent payload")
	}
	defer func() {
		report(download.PhaseTransferring, download.Progress{Completed: verifiedBytes(tor, info), Total: info.Length})
	}()
	if staged.Recheck() {
		if err := tor.VerifyDataContext(ctx); err != nil {
			return err
		}
		report(download.PhaseWaitingPeers, download.Progress{Completed: verifiedBytes(tor, info), Total: info.Length})
	}
	tor.AllowDataDownload()
	tor.DownloadAll()
	return transfer(ctx, tor, info, staged.WriteErrors(), report)
}

func transfer(ctx context.Context, tor *torrent.Torrent, info *metainfo.Info, writeError <-chan error, report func(download.Phase, download.Progress)) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writeError:
			return err
		case <-tor.Complete().On():
			report(download.PhaseTransferring, download.Progress{Completed: info.Length, Total: info.Length})
			return nil
		case <-ticker.C:
			phase := download.PhaseWaitingPeers
			stats := tor.Stats()
			if stats.ActivePeers > 0 && (stats.ConnectedSeeders > 0 || stats.BytesReadUsefulData.Int64() > 0) {
				phase = download.PhaseTransferring
			}
			report(phase, download.Progress{Completed: verifiedBytes(tor, info), Total: info.Length})
		}
	}
}

func verifiedBytes(tor *torrent.Torrent, info *metainfo.Info) int64 {
	var completed, begin int64
	for _, run := range tor.PieceStateRuns() {
		end := begin + int64(run.Length)
		if run.Ok && run.Complete {
			endOffset := info.Length
			if end < int64(info.NumPieces()) {
				endOffset = end * info.PieceLength
			}
			completed += endOffset - begin*info.PieceLength
		}
		begin = end
	}
	return completed
}
