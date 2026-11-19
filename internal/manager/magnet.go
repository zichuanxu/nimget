package manager

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/partialfile"

	"github.com/amemiya02/nimget/internal/settings"
)

func (m *Manager) addMagnet(ctx context.Context, req download.Request) (download.Item, error) {
	invalid := func(err error) (download.Item, error) {
		return download.Item{}, fmt.Errorf("%w: %w", download.ErrInvalidDownload, err)
	}
	source, name, publicURL, err := magnet.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return invalid(err)
	}
	if req.Destination != "" {
		return invalid(errors.New("choose a save directory for a magnet download; its file name comes from the torrent"))
	}
	if req.Connections != 0 || req.SpeedLimit != 0 || req.Checksum != "" || len(req.Headers) != 0 || req.WebsiteSession != nil {
		return invalid(errors.New("connections, per-download speed, checksums, and request headers apply only to web downloads"))
	}
	if err := validateOptions(&req.Priority, &req.Connections, &req.SpeedLimit); err != nil {
		return invalid(err)
	}
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		dir = m.roots[0]
	}
	if !filepath.IsAbs(dir) {
		return invalid(errors.New("directory must be an absolute path"))
	}
	dir = filepath.Clean(dir)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.confineMagnetDirectory(dir); err != nil {
		return invalid(err)
	}
	items, err := m.repo.List(ctx)
	if err != nil {
		return download.Item{}, err
	}
	for _, item := range items {
		if matchesMagnetClaim(item, source.TorrentIdentity) {
			if running := m.active[item.ID]; running != nil {
				item.Completed, item.Total = running.progress()
			}
			return item, nil
		}
	}
	id, err := newID()
	if err != nil {
		return download.Item{}, err
	}
	source.Directory = dir
	if name = sanitizeFilename(name); name == "" {
		name = source.PreferredHash()
	}
	now := m.opts.Now().UTC()
	item := download.Item{
		ID: id, URL: publicURL, Name: name, Directory: dir, SourceKind: download.SourceMagnet, Magnet: &source,
		Status: download.StatusQueued, Priority: req.Priority, Total: -1, CreatedAt: now, UpdatedAt: now,
	}
	if netproxy.Effective(m.Settings().Proxy) {
		item.Phase = download.PhaseProxyBlocked
	}
	if err := m.repo.Create(ctx, item); err != nil {
		return download.Item{}, err
	}
	m.log.Info("magnet download added", "id", id)
	m.publish(download.EventUpdated, item)
	m.signal()
	return item, nil
}

func (m *Manager) confineMagnetDirectory(dir string) (string, error) {
	probe, err := confine(m.allowedRoots(), filepath.Join(dir, ".nimget-directory-check"))
	if err != nil {
		return "", err
	}
	return resolveExisting(filepath.Dir(probe))
}

func (m *Manager) startMagnet(ctx context.Context, item download.Item, s settings.Settings, now time.Time) error {
	if netproxy.Effective(s.Proxy) {
		if item.Phase == download.PhaseProxyBlocked {
			return nil
		}
		item.Phase = download.PhaseProxyBlocked
		item.UpdatedAt = now.UTC()
		if err := m.repo.Update(ctx, item); err != nil {
			return err
		}
		m.publish(download.EventUpdated, item)
		return nil
	}
	item.Status, item.Phase = download.StatusRunning, download.PhaseMetadata
	item.UpdatedAt = now.UTC()
	item.Error, item.Failure = "", ""
	item.Completed = 0
	if err := m.repo.Update(ctx, item); err != nil {
		return err
	}
	delete(m.retryAt, item.ID)
	workCtx, cancel := context.WithCancel(ctx)
	j := &job{cancel: cancel, item: item, checkpointDone: make(chan struct{})}
	j.total.Store(item.Total)
	m.active[item.ID] = j
	m.wg.Add(1)
	m.log.Info("magnet download started", "id", item.ID)
	m.publish(download.EventUpdated, item)
	go m.runMagnet(workCtx, j, item)
	return nil
}

func (m *Manager) runMagnet(ctx context.Context, j *job, item download.Item) {
	defer m.wg.Done()
	defer close(j.checkpointDone)
	var runErr error
	var name string
	if item.Magnet == nil {
		runErr = errors.New("saved magnet download is missing its source")
	} else {
		var root *os.Root
		var manifest magnet.Manifest
		var stage download.MagnetObject
		runErr = m.magnets.Run(ctx, *item.Magnet,
			func(verified download.TorrentIdentity) error {
				return m.reconcileMagnetIdentity(ctx, j, verified)
			},
			func(verified magnet.Manifest, metadata []byte) (magnet.StagedPayload, error) {
				name = sanitizeFilename(verified.Name)
				if name == "" {
					name = item.Magnet.PreferredHash()
				}
				var entries []download.MagnetEntry
				if verified.Directory {
					if verified.Name != "" && !validMagnetComponent(verified.Name) {
						return nil, errors.New("torrent metadata contains an unsafe output directory name")
					}
					var err error
					entries, err = magnetDirectoryEntries(verified)
					if err != nil {
						return nil, err
					}
				}
				var payload magnet.StagedPayload
				var err error
				root, payload, stage, err = m.prepareMagnet(ctx, j, name, verified, entries, metadata)
				manifest = verified
				return payload, err
			},
			func(phase download.Phase, progress download.Progress) {
				m.reportMagnet(ctx, j, phase, progress)
			},
			func(metadata []byte) error {
				return m.checkpointMagnetMetadata(j, metadata)
			},
			func() error {
				return m.publishMagnet(ctx, j, root, stage, name, manifest, item.URL)
			})
		if root != nil {
			_ = root.Close()
		}
	}
	canceled := ctx.Err() != nil
	j.cancel()
	m.finish(j, runErr, canceled)
}

func (m *Manager) prepareMagnet(ctx context.Context, j *job, name string, manifest magnet.Manifest, entries []download.MagnetEntry, metadata []byte) (*os.Root, magnet.StagedPayload, download.MagnetObject, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, download.MagnetObject{}, err
	}
	item := j.item
	source := *item.Magnet
	item.Magnet = &source
	dir, err := m.confineMagnetDirectory(item.Directory)
	if err != nil {
		return nil, nil, download.MagnetObject{}, err
	}
	if item.Destination == "" {
		item.Destination, err = m.reserveMagnetDestination(ctx, item.ID, dir, name)
		if err != nil {
			return nil, nil, download.MagnetObject{}, err
		}
	}
	if err := confineMagnetEntries(item.Destination, entries); err != nil {
		return nil, nil, download.MagnetObject{}, err
	}
	if source.Stage == nil {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, download.MagnetObject{}, err
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, nil, download.MagnetObject{}, err
	}
	var payload magnet.StagedPayload
	var stage *download.MagnetObject
	var fresh bool
	if manifest.Directory {
		payload, stage, fresh, err = openMagnetDirectoryStage(root, source.Stage, manifest, entries)
	} else {
		var file *os.File
		file, stage, fresh, err = openMagnetStage(root, source.Stage)
		if err == nil {
			payload = magnet.NewFileStorage(file, !fresh)
		}
	}
	if err != nil {
		root.Close()
		return nil, nil, download.MagnetObject{}, err
	}
	source.Stage, source.Metadata = stage, metadata
	item.Name = filepath.Base(item.Destination)
	item.Total = manifest.Total
	item.Phase = download.PhaseWaitingPeers
	item.UpdatedAt = m.opts.Now().UTC()
	if err := m.saveMagnetCheckpoint(ctx, item); err != nil {
		payload.Close()
		if fresh {
			_ = removeMagnetObject(context.WithoutCancel(ctx), root, stage)
		}
		root.Close()
		return nil, nil, download.MagnetObject{}, err
	}
	source.Metadata = nil
	j.item = item
	j.total.Store(manifest.Total)
	m.publish(download.EventUpdated, item)
	return root, payload, *stage, nil
}

func openMagnetStage(root *os.Root, stage *download.MagnetObject) (*os.File, *download.MagnetObject, bool, error) {
	rootID, err := partialfile.RootIdentity(root)
	if err != nil {
		return nil, nil, false, err
	}
	if stage != nil {
		if rootID != stage.RootID {
			return nil, nil, false, errors.New("the magnet save directory changed; restore it and retry")
		}
		file, object, err := partialfile.OpenRooted(root, stage.Name, true, false)
		if err == nil {
			if object.ID != stage.ID {
				file.Close()
				return nil, nil, false, errors.New("the staged magnet file was replaced; move the replacement aside and retry")
			}
			return file, stage, false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, false, err
		}
	}
	name := ".nimget-magnet-" + rand.Text() + ".part"
	file, object, err := partialfile.OpenRooted(root, name, true, true)
	if err != nil {
		return nil, nil, false, err
	}
	if err := partialfile.SyncRoot(root); err != nil {
		file.Close()
		_ = root.Remove(name)
		return nil, nil, false, err
	}
	return file, &download.MagnetObject{RootID: rootID, Name: name, ID: object.ID}, true, nil
}

func (m *Manager) checkpointMagnetMetadata(j *job, metadata []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ctx := context.Background()
	item, err := m.repo.Get(ctx, j.item.ID)
	if err != nil {
		return err
	}
	source := *item.Magnet
	source.Metadata = metadata
	item.Magnet = &source
	return m.saveMagnetCheckpoint(ctx, item)
}

func (m *Manager) saveMagnetCheckpoint(ctx context.Context, item download.Item) error {
	if repo, ok := m.repo.(interface {
		UpdateMagnetCheckpoint(context.Context, download.Item) error
	}); ok {
		return repo.UpdateMagnetCheckpoint(ctx, item)
	}
	return m.repo.Update(ctx, item)
}

// reserveMagnetDestination is called under the manager lock so a web download
// and a magnet cannot both reserve the same name.
func (m *Manager) reserveMagnetDestination(ctx context.Context, id, dir, name string) (string, error) {
	items, err := m.repo.List(ctx)
	if err != nil {
		return "", err
	}
	for _, item := range items {
		if item.ID == id || item.Destination == "" {
			continue
		}
		reserved := reservationPath(item.Destination)
		if samePath(reserved, dir) || within(reserved, dir) {
			return "", download.ErrDestinationBusy
		}
	}
	dest, err := freeName(dir, name, func(path string) bool {
		for _, item := range items {
			if item.ID != id && reservationsClash(item.Destination, path) {
				return true
			}
		}
		return false
	})
	if err != nil {
		return "", err
	}
	return confine(m.allowedRoots(), dest)
}

func (m *Manager) reportMagnet(ctx context.Context, j *job, phase download.Phase, progress download.Progress) {
	j.completed.Store(progress.Completed)
	j.total.Store(progress.Total)
	m.mu.Lock()
	if j.item.Status == download.StatusRunning && j.item.Phase != phase {
		item := j.item
		item.Phase = phase
		item.Completed, item.Total = progress.Completed, progress.Total
		item.UpdatedAt = m.opts.Now().UTC()
		if err := m.repo.Update(ctx, item); err == nil {
			j.item = item
			m.publish(download.EventUpdated, item)
		}
	}
	m.mu.Unlock()
	select {
	case m.progressWake <- struct{}{}:
	default:
	}
}
