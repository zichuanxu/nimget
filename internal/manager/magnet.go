package manager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/quarantine"
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
	item.Completed, item.Total = 0, -1
	if err := m.repo.Update(ctx, item); err != nil {
		return err
	}
	delete(m.retryAt, item.ID)
	workCtx, cancel := context.WithCancel(ctx)
	j := &job{cancel: cancel, item: item}
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
	var runErr error
	var name string
	if item.Magnet == nil {
		runErr = errors.New("saved magnet download is missing its source")
	} else {
		runErr = m.magnets.Run(ctx, *item.Magnet,
			func(verified download.TorrentIdentity) error {
				return m.reconcileMagnetIdentity(ctx, j, verified)
			},
			func(metadataName string, total int64) (*os.Root, error) {
				name = sanitizeFilename(metadataName)
				if name == "" {
					name = item.Magnet.PreferredHash()
				}
				return m.prepareMagnet(ctx, j, name, total)
			},
			func(phase download.Phase, progress download.Progress) {
				m.reportMagnet(ctx, j, phase, progress)
			},
			func(root *os.Root, staged string) error {
				return m.publishMagnet(ctx, j, root, staged, name)
			})
	}
	canceled := ctx.Err() != nil
	j.cancel()
	m.finish(j, runErr, canceled)
}

func (m *Manager) prepareMagnet(ctx context.Context, j *job, name string, total int64) (*os.Root, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	item := j.item
	dir, err := m.confineMagnetDirectory(item.Directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	item.Destination, err = m.reserveMagnetDestination(ctx, item.ID, dir, name)
	if err == nil {
		item.Name = filepath.Base(item.Destination)
		item.Total = total
		item.Phase = download.PhaseWaitingPeers
		item.UpdatedAt = m.opts.Now().UTC()
		err = m.repo.Update(ctx, item)
	}
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	j.item = item
	j.total.Store(total)
	m.publish(download.EventUpdated, item)
	return root, nil
}

// reserveMagnetDestination is called under the manager lock so a web download
// and a magnet cannot both reserve the same name.
func (m *Manager) reserveMagnetDestination(ctx context.Context, id, dir, name string) (string, error) {
	items, err := m.repo.List(ctx)
	if err != nil {
		return "", err
	}
	dest, err := freeName(dir, name, func(path string) bool {
		for _, item := range items {
			if item.ID != id && item.Destination != "" && clashes(item.Destination, path) {
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

func (m *Manager) publishMagnet(ctx context.Context, j *job, root *os.Root, staged, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	item := j.item
	dir, err := m.confineMagnetDirectory(item.Directory)
	if err != nil {
		return err
	}
	if !samePath(dir, root.Name()) {
		return errors.New("the magnet save directory changed during transfer")
	}
	originalDir, err := root.Stat(".")
	if err != nil {
		return err
	}
	currentDir, err := os.Stat(dir)
	if err != nil || !os.SameFile(originalDir, currentDir) {
		return errors.New("the magnet save directory changed during transfer")
	}
	if err := quarantine.Mark(filepath.Join(dir, staged), item.URL); err != nil {
		return fmt.Errorf("quarantine verified torrent file: %w", err)
	}
	for range 10000 {
		// The original path may have been taken by an external writer.
		dest, err := m.reserveMagnetDestination(ctx, item.ID, dir, name)
		if err != nil {
			return err
		}
		if item.Destination != dest {
			item.Destination, item.Name = dest, filepath.Base(dest)
			if err := m.repo.Update(ctx, item); err != nil {
				return err
			}
			j.item = item
		}
		if err := root.Link(staged, item.Name); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("publish verified torrent file: %w", err)
		}
		return nil
	}
	return errors.New("could not find a free destination for the magnet")
}
