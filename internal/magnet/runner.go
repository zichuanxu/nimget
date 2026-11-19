package magnet

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"sync"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/time/rate"
)

// UploadBurst fits one standard BitTorrent request.
const UploadBurst = 16 << 10

type Runner struct {
	mu            sync.Mutex
	client        *torrent.Client
	active        int
	downloadLimit *rate.Limiter
	uploadLimit   *rate.Limiter
	loopbackOnly  bool
	dhtBootstrap  []netip.AddrPort
	dhtReady      <-chan struct{}
	cancelDHT     context.CancelFunc
	metadata      chan struct{}
	stallTimeout  time.Duration
}

type Options struct {
	LoopbackOnly bool
	DHTBootstrap []netip.AddrPort
	StallTimeout time.Duration
}

func NewRunner(downloadLimit, uploadLimit *rate.Limiter, opts Options) *Runner {
	if opts.StallTimeout <= 0 {
		opts.StallTimeout = 2 * time.Minute
	}
	return &Runner{
		downloadLimit: downloadLimit, uploadLimit: uploadLimit,
		loopbackOnly: opts.LoopbackOnly, dhtBootstrap: slices.Clone(opts.DHTBootstrap),
		metadata: make(chan struct{}, 1), stallTimeout: opts.StallTimeout,
	}
}

func (r *Runner) acquire() (*torrent.Client, <-chan struct{}, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.client == nil {
		cfg := torrent.NewDefaultClientConfig()
		cfg.ListenPort = 0
		if r.loopbackOnly {
			cfg.SetListenAddr("127.0.0.1:0")
			cfg.DisableIPv6 = true
		}
		ready, cancel := configureDHT(cfg, r.loopbackOnly, r.dhtBootstrap)
		cfg.DisableTrackers = true
		cfg.NoDefaultPortForwarding = true
		cfg.DisableWebtorrent = true
		cfg.DisableWebseeds = true
		cfg.Seed = false
		cfg.DefaultStorage = &fileStorage{}
		cfg.DownloadRateLimiter = r.downloadLimit
		cfg.UploadRateLimiter = r.uploadLimit
		// Tracker URLs and transport errors can contain private passkeys.
		cfg.Slogger = slog.New(slog.DiscardHandler)
		client, err := torrent.NewClient(cfg)
		if err != nil {
			cancel()
			return nil, nil, errors.New("could not start BitTorrent networking")
		}
		r.client, r.dhtReady, r.cancelDHT = client, ready, cancel
	}
	r.active++
	return r.client, r.dhtReady, nil
}

func (r *Runner) release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.active == 0 {
		r.cancelDHT()
		r.client.Close()
		r.client = nil
	}
}

// Run retrieves and verifies a torrent. prepare validates and reserves the
// output before storage is opened. publish runs with payload handles closed
// and the torrent disconnected.
func (r *Runner) Run(ctx context.Context, source download.Magnet,
	claimOwnership func(download.TorrentIdentity) error,
	prepare func(manifest Manifest, metadata []byte) (StagedPayload, error),
	report func(download.Phase, download.Progress),
	checkpoint func(metadata []byte) error,
	publish func() error,
) error {
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
	client, dhtReady, err := r.acquire()
	if err != nil {
		return err
	}
	defer r.release()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := loadMagnetMetadata(source, &opts); err != nil {
		return err
	}
	// On an existing stage, unknown completion triggers a hash check before
	// data can be requested, including when a v2 piece hash becomes available.
	opts.DisableInitialPieceCheck = source.Stage == nil
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
	stopDiscovery := discover(ctx, client, tor, source.Trackers, announceHashes, dhtReady)
	stop := sync.OnceFunc(func() {
		stopDiscovery()
		tor.Drop()
	})
	defer stop()
	if err := r.waitForMetadata(ctx, tor); err != nil {
		return err
	}
	info := tor.Info()
	if info.Private != nil && *info.Private {
		return errors.New("private BitTorrent downloads are unsupported; use a public magnet link")
	}
	meta := tor.Metainfo()
	verified, err := VerifyIdentity(source.TorrentIdentity, info, meta)
	if err != nil {
		return err
	}
	if err := validateManifestEncoding(info, meta.InfoBytes); err != nil {
		return err
	}
	manifest, err := readManifest(info)
	if err != nil {
		return err
	}
	if err := claimOwnership(verified); err != nil {
		return err
	}
	unlockMetadata()
	encoded, err := encodeVerifiedMetainfo(meta)
	if err != nil {
		return err
	}
	staged, err := prepare(manifest, encoded)
	if err != nil {
		return err
	}
	var transferErr error
	switch {
	case ctx.Err() != nil:
		transferErr = ctx.Err()
	default:
		transferErr = r.downloadPayload(ctx, tor, manifest, staged, report)
	}
	var finalMeta metainfo.MetaInfo
	if info.HasV2() {
		finalMeta = tor.Metainfo()
	}
	stop()
	if err := errors.Join(staged.Checkpoint(), staged.Close()); err != nil {
		return errors.Join(transferErr, err)
	}
	if !maps.Equal(meta.PieceLayers, finalMeta.PieceLayers) {
		encoded, err := encodeVerifiedMetainfo(finalMeta)
		if err == nil {
			err = checkpoint(encoded)
		}
		if err != nil {
			return errors.Join(transferErr, err)
		}
	}
	if transferErr != nil {
		return transferErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return publish()
}

func encodeVerifiedMetainfo(meta metainfo.MetaInfo) ([]byte, error) {
	var encoded bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: meta.InfoBytes, PieceLayers: meta.PieceLayers}).Write(&encoded); err != nil {
		return nil, errors.New("could not save verified torrent metadata")
	}
	return encoded.Bytes(), nil
}

func loadMagnetMetadata(source download.Magnet, opts *torrent.AddTorrentOpts) error {
	if len(source.Metadata) == 0 {
		return nil
	}
	saved, err := metainfo.Load(bytes.NewReader(source.Metadata))
	if err != nil {
		return errors.New("saved torrent metadata is invalid")
	}
	info, err := saved.UnmarshalInfo()
	if err != nil {
		return errors.New("saved torrent info is invalid")
	}
	if _, err := VerifyIdentity(source.TorrentIdentity, &info, *saved); err != nil {
		return err
	}
	opts.InfoBytes = saved.InfoBytes
	if len(saved.PieceLayers) != 0 {
		opts.PieceLayers = saved.PieceLayers
	}
	return nil
}

func (r *Runner) downloadPayload(ctx context.Context, tor *torrent.Torrent, manifest Manifest, staged StagedPayload, report func(download.Phase, download.Progress)) error {
	if err := tor.OpenStorage(ctx, staged); err != nil {
		return errors.New("could not open the staged torrent payload")
	}
	tor.AllowDataDownload()
	tor.AllowDataUpload()
	tor.DownloadAll()
	return r.transfer(ctx, tor, manifest, staged.WriteErrors(), report)
}

func (r *Runner) transfer(ctx context.Context, tor *torrent.Torrent, manifest Manifest, writeError <-chan error, report func(download.Phase, download.Progress)) error {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	watch := r.newStallWatch()
	phase := download.PhaseWaitingPeers
	var verifiedHigh int64
	defer func() {
		report(phase, download.Progress{Completed: verifiedBytes(tor, manifest), Total: manifest.Total})
	}()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-writeError:
			return err
		case <-tor.Complete().On():
			phase = download.PhaseTransferring
			return nil
		case now := <-ticker.C:
			stats := tor.Stats()
			phase = download.PhaseWaitingPeers
			if stats.ActivePeers > 0 && (stats.ConnectedSeeders > 0 || stats.BytesReadUsefulData.Int64() > 0) {
				phase = download.PhaseTransferring
			}
			completed := verifiedBytes(tor, manifest)
			report(phase, download.Progress{Completed: completed, Total: manifest.Total})
			if completed > verifiedHigh {
				verifiedHigh, watch.lastProgress = completed, now
			}
			// Chunks advance even before a whole piece can be verified. Use a
			// high-water mark so re-reading corrupt chunks does not defer retry.
			if watch.expired(now, tor.BytesCompleted(), stats.BytesRead.Int64()) {
				select {
				case <-tor.Complete().On():
					phase = download.PhaseTransferring
					return nil
				default:
				}
				if phase == download.PhaseWaitingPeers {
					return magnetStall("while waiting for peers")
				}
				return magnetStall("during payload transfer")
			}
		}
	}
}

func verifiedBytes(tor *torrent.Torrent, manifest Manifest) int64 {
	var completed, begin int64
	for _, run := range tor.PieceStateRuns() {
		end := begin + int64(run.Length)
		if run.Ok && run.Complete {
			completed += manifest.payloadBefore(end*manifest.info.PieceLength) - manifest.payloadBefore(begin*manifest.info.PieceLength)
		}
		begin = end
	}
	return completed
}
