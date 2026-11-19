// Package manager owns the ordered service download queue.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/hls"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/settings"
	"golang.org/x/time/rate"
)

var (
	ErrInvalidTransition = download.ErrInvalidTransition
	ErrActive            = download.ErrActive
)

const (
	// busyRetryDelay is how long a download whose destination is locked by
	// another process waits before the scheduler tries it again.
	busyRetryDelay = 5 * time.Second
	// limiterBurst bounds one limiter wait; the engine reads in slices no
	// larger than this while a limit is in force.
	limiterBurst = 256 << 10
)

type Options struct {
	Publish func(download.Event)
	// MagnetPublicationCheckpoint observes the two durable publication boundaries.
	MagnetPublicationCheckpoint func(download.MagnetPublicationStep, download.Item)
	Now                         func() time.Time
	Logger                      *slog.Logger
	// DownloadRoots lists the directories destinations must resolve inside.
	DownloadRoots []string
	// FlushInterval is how often in-memory progress is persisted, orphaned
	// running rows are requeued, the schedule is applied, and the scheduler
	// retries after a store error. It defaults to 5s.
	FlushInterval time.Duration
	// Defaults are used until settings are saved; zero means settings.Default().
	Defaults settings.Settings
	// InspectTimeout bounds the probe that names a download; default 15s.
	InspectTimeout time.Duration
}

// job is the in-memory state of a started runner. item mirrors the stored row
// and is guarded by Manager.mu. The engine reports progress into the atomics
// without taking any manager lock, so a slow SQLite write can never stall the
// engine; the Run loop copies it into item and publishes it.
type job struct {
	cancel         context.CancelFunc
	checkpointDone chan struct{}
	item           download.Item
	completed      atomic.Int64
	total          atomic.Int64
	// host and conns are this job's share of the per-host connection cap.
	host    string
	conns   int
	limiter *rate.Limiter
}

func (j *job) progress() (completed, total int64) {
	return j.completed.Load(), j.total.Load()
}

type Manager struct {
	repo    download.Repository
	runner  download.Runner
	magnets *magnet.Runner
	opts    Options
	roots   []string
	log     *slog.Logger

	wake         chan struct{}
	progressWake chan struct{}
	wg           sync.WaitGroup

	// global caps all downloads together; its limit follows the schedule.
	global *rate.Limiter
	// settings is replaced under mu and read without it.
	settings atomic.Pointer[settings.Settings]

	// mu serializes status transitions, guards active, retryAt, hostConns,
	// and job items, and orders published events with the state they
	// describe. Publish must not block.
	mu        sync.Mutex
	sequence  int64
	active    map[string]*job
	retryAt   map[string]time.Time
	hostConns map[string]int
}

func New(repo download.Repository, runner download.Runner, opts Options) (*Manager, error) {
	if repo == nil {
		return nil, errors.New("nil download repository")
	}
	if runner == nil {
		return nil, errors.New("nil download runner")
	}
	if opts.Publish == nil {
		opts.Publish = func(download.Event) {}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 5 * time.Second
	}
	if opts.InspectTimeout <= 0 {
		opts.InspectTimeout = 15 * time.Second
	}
	if opts.Defaults.MaxConcurrent == 0 {
		opts.Defaults = settings.Default()
	}
	roots, err := resolveRoots(opts.DownloadRoots)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		repo: repo, runner: runner, opts: opts, roots: roots, log: opts.Logger,
		wake: make(chan struct{}, 1), progressWake: make(chan struct{}, 1),
		global: rate.NewLimiter(rate.Inf, limiterBurst),
		active: make(map[string]*job), retryAt: make(map[string]time.Time), hostConns: make(map[string]int),
	}
	m.magnets = magnet.NewRunner(m.global)
	stored, ok, err := repo.LoadSettings(context.Background())
	if err != nil {
		return nil, err
	}
	if !ok {
		stored = opts.Defaults
	}
	if err := stored.Validate(); err != nil {
		// A bad row must not keep the service from starting.
		m.log.Error("stored settings are invalid; using defaults", "err", err)
		stored = opts.Defaults
	}
	m.settings.Store(&stored)
	m.applySchedule(opts.Now())
	return m, nil
}

// allowedRoots are the service's download roots plus the directories picked
// in the GUI.
func (m *Manager) allowedRoots() []string {
	extra := m.Settings().ExtraRoots
	if len(extra) == 0 {
		return m.roots
	}
	roots := append([]string(nil), m.roots...)
	for _, dir := range extra {
		if resolved, err := resolveRoots([]string{dir}); err == nil {
			roots = append(roots, resolved...)
		}
	}
	return roots
}

// Settings returns the settings in force.
func (m *Manager) Settings() settings.Settings {
	return *m.settings.Load()
}

// UpdateSettings validates, stores, and applies new settings. Site passwords
// sent back as settings.Masked keep their stored value.
//
// Folders picked in the GUI (ExtraRoots) widen destination confinement, so
// clients may only remove them; AddExtraRoot adds one.
func (m *Manager) UpdateSettings(ctx context.Context, s settings.Settings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.Settings()
	s, err := s.Unmask(current)
	if err != nil {
		return fmt.Errorf("%w: %w", download.ErrInvalidSettings, err)
	}
	if err := s.ValidateHeaderEdits(current); err != nil {
		return fmt.Errorf("%w: %w", download.ErrInvalidSettings, err)
	}
	kept := []string{}
	for _, dir := range s.ExtraRoots {
		if slices.Contains(current.ExtraRoots, dir) {
			kept = append(kept, dir)
		}
	}
	s.ExtraRoots = kept
	return m.saveSettings(ctx, s)
}

// AddExtraRoot allows downloads under dir, a folder the user picked in the
// GUI.
func (m *Manager) AddExtraRoot(ctx context.Context, dir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.Settings()
	if slices.Contains(s.ExtraRoots, dir) {
		return nil
	}
	s.ExtraRoots = append(slices.Clone(s.ExtraRoots), dir)
	return m.saveSettings(ctx, s)
}

// saveSettings validates, stores, and applies s. The caller holds m.mu.
func (m *Manager) saveSettings(ctx context.Context, s settings.Settings) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%w: %w", download.ErrInvalidSettings, err)
	}
	if err := m.repo.SaveSettings(ctx, s); err != nil {
		return err
	}
	m.settings.Store(&s)
	m.applySchedule(m.opts.Now())
	m.log.Info("settings updated")
	m.signal()
	return nil
}

func (m *Manager) Add(ctx context.Context, req download.Request) (download.Item, error) {
	if magnet.IsLink(strings.TrimSpace(req.URL)) {
		return m.addMagnet(ctx, req)
	}
	return m.add(ctx, req, m.opts.InspectTimeout, "", func(ctx context.Context, item download.Item) (bool, error) {
		return true, m.repo.Create(ctx, item)
	})
}

func (m *Manager) add(ctx context.Context, req download.Request, inspectTimeout time.Duration, captureProof string, persist func(context.Context, download.Item) (bool, error)) (download.Item, error) {
	invalid := func(err error) (download.Item, error) {
		return download.Item{}, fmt.Errorf("%w: %w", download.ErrInvalidDownload, err)
	}
	rawURL := strings.TrimSpace(req.URL)
	if err := validateURL(rawURL); err != nil {
		return invalid(err)
	}
	if err := validateOptions(&req.Priority, &req.Connections, &req.SpeedLimit); err != nil {
		return invalid(err)
	}
	for name, value := range req.Headers {
		if err := settings.ValidateHeader(name, value); err != nil {
			return invalid(err)
		}
	}
	destination := strings.TrimSpace(req.Destination)
	named := destination == ""
	if named {
		// The name comes from the server; probe before taking the lock.
		dir := strings.TrimSpace(req.Directory)
		if dir != "" && !filepath.IsAbs(dir) {
			return invalid(errors.New("directory must be an absolute path"))
		}
		name := fileName("", rawURL)
		if req.WebsiteSession == nil || req.WebsiteSession.Usable(m.opts.Now()) {
			headers := m.headers(hostOf(rawURL), req.Headers)
			if req.WebsiteSession != nil {
				req.WebsiteSession.Apply(headers)
			}
			remote, err := m.inspect(ctx, rawURL, headers, inspectTimeout)
			if captureProof != "" && (err != nil || remote.AttachmentProof != captureProof) {
				return download.Item{}, download.ErrCaptureNotReplayable
			}
			if err == nil {
				name = remoteName(rawURL, remote)
			}
		} else if captureProof != "" {
			return download.Item{}, download.ErrCaptureNotReplayable
		}
		if dir == "" {
			dir = filepath.Join(m.roots[0], category(name))
		}
		destination = filepath.Join(dir, name)
	}
	destination, err := confine(m.allowedRoots(), destination)
	if err != nil {
		return invalid(err)
	}
	id, err := newID()
	if err != nil {
		return download.Item{}, err
	}
	now := m.opts.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return download.Item{}, err
	}
	// Two items sharing a destination would share .part files, and deleting
	// one with its files would destroy the other's data.
	items, err := m.repo.List(ctx)
	if err != nil {
		return download.Item{}, err
	}
	owner := func(path string) string {
		for _, existing := range items {
			if clashes(existing.Destination, path) {
				return existing.ID
			}
		}
		return ""
	}
	if named {
		destination, err = freeName(filepath.Dir(destination), filepath.Base(destination), func(p string) bool { return owner(p) != "" })
		if err != nil {
			return download.Item{}, err
		}
	} else if other := owner(destination); other != "" {
		return invalid(fmt.Errorf("destination %s is already used by download %s", destination, other))
	}
	item := download.Item{
		ID: id, URL: rawURL, Destination: destination, Status: download.StatusQueued,
		Priority: req.Priority, Connections: req.Connections, SpeedLimit: req.SpeedLimit,
		Checksum: strings.TrimSpace(req.Checksum), Headers: req.Headers,
		CreatedAt: now, UpdatedAt: now, WebsiteSession: req.WebsiteSession,
	}
	created, err := persist(ctx, item)
	if err != nil || !created {
		return download.Item{}, err
	}
	m.log.Info("download added", "id", id)
	m.publish(download.EventUpdated, item)
	m.signal()
	return item, nil
}

// Get returns a stored item with live progress applied.
func (m *Manager) Get(ctx context.Context, id string) (download.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.repo.Get(ctx, id)
	if job := m.active[id]; err == nil && job != nil {
		item.Completed, item.Total = job.progress()
	}
	return item, err
}

// List returns stored items with live progress applied.
func (m *Manager) List(ctx context.Context) ([]download.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.listLocked(ctx)
}

// ListSnapshot returns a list and its position in the same sequence as
// published events. Consumers can discard delayed events covered by the list.
func (m *Manager) ListSnapshot(ctx context.Context) ([]download.Item, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.listLocked(ctx)
	if err != nil {
		return nil, 0, err
	}
	m.sequence++
	return items, m.sequence, nil
}

func (m *Manager) listLocked(ctx context.Context) ([]download.Item, error) {
	items, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if j, ok := m.active[items[i].ID]; ok {
			items[i].Completed, items[i].Total = j.progress()
		}
	}
	return items, nil
}

func (m *Manager) Pause(ctx context.Context, id string) error {
	var checkpointDone <-chan struct{}
	err := m.transition(ctx, id, false, func(item *download.Item) error {
		if item.Status != download.StatusQueued && item.Status != download.StatusRunning {
			return fmt.Errorf("%w: cannot pause a %s download", ErrInvalidTransition, item.Status)
		}
		if j, ok := m.active[item.ID]; ok {
			j.cancel()
			checkpointDone = j.checkpointDone
			j.item.Status = download.StatusPaused
			item.Completed, item.Total = j.progress()
		}
		item.Status = download.StatusPaused
		item.Phase = ""
		item.Error, item.Failure = "", ""
		return nil
	})
	if err != nil || checkpointDone == nil {
		return err
	}
	select {
	case <-checkpointDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (m *Manager) Resume(ctx context.Context, id string) error {
	return m.requeue(ctx, id, download.StatusPaused)
}

func (m *Manager) Retry(ctx context.Context, id string) error {
	return m.requeue(ctx, id, download.StatusFailed)
}

// Update changes download options or queues a stopped download with a
// refreshed source. A new speed limit applies to active downloads at once.
func (m *Manager) Update(ctx context.Context, id string, p download.Patch) error {
	var priority, conns int
	var limit int64
	if p.Priority != nil {
		priority = *p.Priority
	}
	if p.Connections != nil {
		conns = *p.Connections
	}
	if p.SpeedLimit != nil {
		limit = *p.SpeedLimit
	}
	if err := validateOptions(&priority, &conns, &limit); err != nil {
		return fmt.Errorf("%w: %w", download.ErrInvalidDownload, err)
	}
	if p.Headers != nil {
		for name, value := range *p.Headers {
			if err := settings.ValidateHeader(name, value); err != nil {
				return fmt.Errorf("%w: %w", download.ErrInvalidDownload, err)
			}
		}
	}
	var rawURL string
	if p.URL != nil {
		rawURL = strings.TrimSpace(*p.URL)
		if err := validateURL(rawURL); err != nil {
			return fmt.Errorf("%w: %w", download.ErrInvalidDownload, err)
		}
	}
	err := m.transition(ctx, id, p.URL != nil || p.Headers != nil, func(item *download.Item) error {
		if item.SourceKind == download.SourceMagnet && (p.URL != nil || p.Headers != nil || p.Connections != nil || p.SpeedLimit != nil) {
			return fmt.Errorf("%w: only priority can be changed on a magnet download", download.ErrInvalidDownload)
		}
		if p.URL != nil {
			if _, active := m.active[id]; active {
				return ErrActive
			}
			if item.Status != download.StatusFailed && item.Status != download.StatusPaused {
				return fmt.Errorf("%w: cannot refresh the source of a %s download", ErrInvalidTransition, item.Status)
			}
			item.URL = rawURL
			item.Headers = nil
			item.Recovery = download.RecoveryValidateSource
			item.Status = download.StatusQueued
			item.Error, item.Failure = "", ""
		}
		if p.Priority != nil {
			item.Priority = priority
		}
		if p.Connections != nil {
			item.Connections = conns
		}
		if p.SpeedLimit != nil {
			item.SpeedLimit = limit
		}
		if p.Headers != nil {
			item.Headers = *p.Headers
		}
		if p.URL != nil || p.Headers != nil {
			item.WebsiteSession = nil
			item.WebsiteSessionUnavailable = false
		}
		return nil
	})
	if err == nil {
		m.signal()
	}
	return err
}

func (m *Manager) requeue(ctx context.Context, id string, from download.Status) error {
	err := m.transition(ctx, id, false, func(item *download.Item) error {
		if item.Status != from {
			return fmt.Errorf("%w: expected a %s download, got %s", ErrInvalidTransition, from, item.Status)
		}
		if item.Recovery == download.RecoveryRestartRequired {
			return download.ErrRestartRequired
		}
		// A paused runner may still be shutting down; requeueing now would let
		// the scheduler start a second runner against the same destination.
		if _, ok := m.active[item.ID]; ok {
			return ErrActive
		}
		item.Status = download.StatusQueued
		item.Error, item.Failure = "", ""
		return nil
	})
	if err == nil {
		m.signal()
	}
	return err
}

// Restart discards partial data only when a refreshed source could not be
// resumed safely. It keeps the task's source and destination.
func (m *Manager) Restart(ctx context.Context, id string) error {
	err := m.transition(ctx, id, false, func(item *download.Item) error {
		if _, active := m.active[id]; active {
			return ErrActive
		}
		if item.Status != download.StatusFailed || item.Recovery != download.RecoveryRestartRequired {
			return fmt.Errorf("%w: this download does not require a restart", ErrInvalidTransition)
		}
		if _, err := confine(m.allowedRoots(), item.Destination); err != nil {
			return err
		}
		if err := m.discardPartial(*item); err != nil {
			return err
		}
		item.Completed, item.Total = 0, 0
		item.Status = download.StatusQueued
		item.Error, item.Failure = "", ""
		item.Recovery = ""
		return nil
	})
	if err == nil {
		m.signal()
	}
	return err
}

func (m *Manager) discardPartial(item download.Item) error {
	if item.SourceKind == download.SourceMagnet {
		return nil
	}
	return m.runner.Discard(download.Spec{URL: item.URL, Destination: item.Destination, ResumeKey: item.ID})
}

// transition applies change to the stored item under the manager lock, then
// persists and publishes it.
func (m *Manager) transition(ctx context.Context, id string, withHeaders bool, change func(*download.Item) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if j, ok := m.active[id]; ok {
		item.Completed, item.Total = j.progress()
	}
	if err := change(&item); err != nil {
		return err
	}
	item.UpdatedAt = m.opts.Now().UTC()
	save := m.repo.Update
	if withHeaders {
		save = m.repo.UpdateWithHeaders
	}
	if err := save(ctx, item); err != nil {
		return err
	}
	if j := m.active[id]; j != nil {
		if j.item.SpeedLimit != item.SpeedLimit {
			j.limiter.SetLimit(limitOf(item.SpeedLimit))
		}
		j.item.Priority, j.item.Connections, j.item.SpeedLimit = item.Priority, item.Connections, item.SpeedLimit
	}
	m.publish(download.EventUpdated, item)
	return nil
}

// Delete removes a download whose runner has stopped. With removeFiles it also
// discards partial state and, for completed downloads only, the destination
// file; a failed item's destination may be a pre-existing user file and is
// never touched.
func (m *Manager) Delete(ctx context.Context, id string, removeFiles bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item, err := m.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	// A row can read "running" with no runner after a failed store write;
	// only a live runner blocks deletion.
	if _, ok := m.active[id]; ok {
		return ErrActive
	}
	if hasMagnetPublication(item) {
		if err := m.recoverMagnetPublication(ctx, &item); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidTransition, err)
		}
	}
	if err := m.repo.Delete(ctx, id); err != nil {
		return err
	}
	delete(m.retryAt, id)
	m.publish(download.EventDeleted, item)
	m.log.Info("download deleted", "id", id, "removeFiles", removeFiles)
	if !removeFiles {
		return nil
	}
	if item.SourceKind == download.SourceMagnet {
		return m.removeMagnetFiles(ctx, item)
	}
	// Re-check confinement: a symlinked ancestor may have changed since Add.
	if _, err := confine(m.allowedRoots(), item.Destination); err != nil {
		return &fileCleanupError{context: "download deleted, but its files were kept", cause: err}
	}
	errs := []error{m.discardPartial(item)}
	if item.Status == download.StatusCompleted {
		if err := os.Remove(item.Destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return &fileCleanupError{context: "download deleted, but removing its files failed", cause: err}
	}
	return nil
}

// fileCleanupError occurs after the download record has been removed.
type fileCleanupError struct {
	context string
	cause   error
}

func (e *fileCleanupError) Error() string { return e.context + ": " + e.cause.Error() }
func (e *fileCleanupError) Unwrap() error { return e.cause }

// Run schedules downloads until ctx is canceled, then cancels every runner and
// waits for them to record their final state before returning.
func (m *Manager) Run(ctx context.Context) {
	m.requeueOrphans(ctx)
	ticker := time.NewTicker(m.opts.FlushInterval)
	defer ticker.Stop()
	m.signal()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			m.wg.Wait()
			return
		case <-ticker.C:
			m.flush(ctx)
			m.requeueOrphans(ctx)
			m.mu.Lock()
			m.applySchedule(m.opts.Now())
			m.mu.Unlock()
			m.schedule(ctx)
		case <-m.progressWake:
			m.publishProgress()
		case <-m.wake:
			m.schedule(ctx)
		}
	}
}

// requeueOrphans requeues rows marked running that have no runner: rows a
// crashed process left behind, and rows whose final store write failed.
func (m *Manager) requeueOrphans(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items, err := m.repo.List(ctx)
	if err != nil {
		m.log.Error("list downloads for recovery", "err", err)
		return
	}
	for _, item := range items {
		if _, ok := m.active[item.ID]; ok || item.Status != download.StatusRunning {
			continue
		}
		if hasMagnetPublication(item) {
			if err := m.retryMagnetPublication(ctx, item); err != nil {
				m.log.Error("recover magnet publication", "id", item.ID, "err", err)
			}
			continue
		}
		item.Status = download.StatusQueued
		item.Phase = ""
		item.UpdatedAt = m.opts.Now().UTC()
		if err := m.repo.Update(ctx, item); err != nil {
			m.log.Error("requeue interrupted download", "id", item.ID, "err", err)
			continue
		}
		m.log.Info("requeued interrupted download", "id", item.ID)
		m.publish(download.EventUpdated, item)
	}
}

// applySchedule sets the global speed limit in force at now and stops
// running downloads when the queue window closes; they return to the queue.
// The caller holds m.mu.
func (m *Manager) applySchedule(now time.Time) {
	s := m.Settings()
	m.global.SetLimit(limitOf(s.SpeedLimitAt(now)))
	queueOpen := s.QueueOpen(now)
	proxy := netproxy.Effective(s.Proxy)
	for id, j := range m.active {
		if j.item.Status != download.StatusRunning {
			continue
		}
		if !queueOpen {
			m.log.Info("queue window closed; stopping download", "id", id)
			j.cancel()
		} else if proxy && j.item.SourceKind == download.SourceMagnet {
			j.cancel()
		}
	}
}

// schedule starts queued downloads, highest priority first and then in the
// order they were added, while the queue window is open, capacity remains,
// and their host has connections to spare.
func (m *Manager) schedule(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	items, err := m.repo.List(ctx) // ordered by creation
	if err != nil {
		m.log.Error("list downloads for scheduling", "err", err)
		return
	}
	queued := items[:0]
	for _, item := range items {
		if item.Status == download.StatusQueued {
			queued = append(queued, item)
		}
	}
	sort.SliceStable(queued, func(a, b int) bool { return queued[a].Priority > queued[b].Priority })

	m.mu.Lock()
	defer m.mu.Unlock()
	s, now := m.Settings(), m.opts.Now()
	for _, item := range queued {
		if !hasMagnetPublication(item) {
			continue
		}
		if err := m.recoverQueuedMagnetPublication(ctx, item.ID); err != nil {
			m.log.Error("recover queued magnet publication", "id", item.ID, "err", err)
		}
	}
	if !s.QueueOpen(now) {
		return
	}
	for _, item := range queued {
		if len(m.active) >= s.MaxConcurrent {
			return
		}
		if err := m.start(ctx, item.ID, s, now); err != nil {
			// Retried on the next tick rather than in a hot loop.
			m.log.Error("start download", "id", item.ID, "err", err)
			return
		}
	}
}

// start launches a queued download if its host has connections to spare.
// The caller holds m.mu.
func (m *Manager) start(ctx context.Context, id string, s settings.Settings, now time.Time) error {
	if _, ok := m.active[id]; ok {
		return nil
	}
	if at, ok := m.retryAt[id]; ok && now.Before(at) {
		return nil
	}
	item, err := m.repo.Get(ctx, id)
	if errors.Is(err, download.ErrNotFound) {
		return nil // deleted since the listing
	}
	if err != nil {
		return err
	}
	if item.Status != download.StatusQueued {
		return nil
	}
	if hasMagnetPublication(item) {
		return nil // The local move is handled ahead of network scheduling.
	}
	if item.SourceKind == download.SourceMagnet {
		return m.startMagnet(ctx, item, s, now)
	}
	// Four downloads at eight connections each must not become 32 sockets
	// against one server: each takes what is left of its host's cap.
	host := hostOf(item.URL)
	conns := min(s.ConnectionsFor(host, item.Connections), s.HostConnectionsFor(host)-m.hostConns[host])
	if conns < 1 {
		return nil
	}
	delete(m.retryAt, id)
	item.UpdatedAt = now.UTC()
	if item.WebsiteSessionUnavailable || (item.WebsiteSession != nil && !item.WebsiteSession.Usable(now)) {
		item.Status = download.StatusFailed
		item.Failure = download.FailureAuthorization
		item.Error = "The website session is no longer available. " + websiteSessionRecovery
		if err := m.repo.Update(ctx, item); err != nil {
			return err
		}
		m.publish(download.EventUpdated, item)
		return nil
	}
	// Re-check confinement: a symlinked ancestor may have changed since Add.
	if _, err := confine(m.allowedRoots(), item.Destination); err != nil {
		item.Status = download.StatusFailed
		item.Error = err.Error()
		item.Failure = classifyFailure(err)
		if err := m.repo.Update(ctx, item); err != nil {
			return err
		}
		m.publish(download.EventUpdated, item)
		return nil
	}
	item.Status = download.StatusRunning
	item.Error, item.Failure = "", ""
	if err := m.repo.Update(ctx, item); err != nil {
		return err
	}
	workCtx, cancel := context.WithCancel(ctx)
	j := &job{cancel: cancel, item: item, host: host, conns: conns, limiter: rate.NewLimiter(limitOf(item.SpeedLimit), limiterBurst)}
	j.completed.Store(item.Completed)
	j.total.Store(item.Total)
	headers := m.headers(host, item.Headers)
	if item.WebsiteSession != nil {
		item.WebsiteSession.Apply(headers)
	}
	spec := download.Spec{
		URL: item.URL, Destination: item.Destination, Connections: conns,
		Headers: headers, Checksum: item.Checksum,
		ResumeKey: item.ID, ValidateSource: item.Recovery == download.RecoveryValidateSource,
		Limiters: []*rate.Limiter{m.global, j.limiter},
	}
	m.active[id] = j
	m.hostConns[host] += conns
	m.wg.Add(1)
	m.log.Info("download started", "id", id, "connections", conns)
	m.publish(download.EventUpdated, item)
	go m.run(workCtx, j, spec)
	return nil
}

func (m *Manager) run(ctx context.Context, j *job, spec download.Spec) {
	defer m.wg.Done()
	err := m.runner.Download(ctx, spec, func(p download.Progress) {
		j.completed.Store(p.Completed)
		j.total.Store(p.Total)
		select {
		case m.progressWake <- struct{}{}:
		default:
		}
	})
	canceled := ctx.Err() != nil
	j.cancel()
	m.finish(j, err, canceled)
}

func (m *Manager) finish(j *job, runErr error, canceled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := j.item.ID
	delete(m.active, id)
	if j.conns > 0 {
		if m.hostConns[j.host] -= j.conns; m.hostConns[j.host] <= 0 {
			delete(m.hostConns, j.host)
		}
	}
	defer m.signal()

	item, err := m.repo.Get(context.Background(), id)
	if err != nil {
		m.log.Error("load finished download", "id", id, "err", err)
		return
	}
	if item.SourceKind == download.SourceMagnet && item.Status == download.StatusCompleted {
		return // Publication already committed and emitted completion.
	}
	item.Completed, item.Total = j.progress()
	item.UpdatedAt = m.opts.Now().UTC()
	item.Phase = ""
	switch {
	case runErr == nil:
		// Checked first: a Pause that raced the final rename must not hide a
		// finished file, which a later Resume would reject as already existing.
		item.Status = download.StatusCompleted
		item.Error, item.Failure = "", ""
		item.Recovery = ""
		item.WebsiteSession = nil
		item.WebsiteSessionUnavailable = false
		if item.Total > 0 {
			item.Completed = item.Total
		} else {
			item.Total = item.Completed
		}
	case item.Status != download.StatusRunning:
		// Paused while running: keep the user's status, record final bytes.
	case errors.Is(runErr, download.ErrDestinationBusy):
		item.Status = download.StatusQueued
		m.retryAt[id] = m.opts.Now().Add(busyRetryDelay)
	case canceled:
		// Service shutdown or a closing queue window: resume later.
		item.Status = download.StatusQueued
		if item.SourceKind == download.SourceMagnet && netproxy.Effective(m.Settings().Proxy) {
			item.Phase = download.PhaseProxyBlocked
		}
	default:
		item.Status = download.StatusFailed
		item.Error = taskDiagnostic(runErr)
		var outputErr *magnetOutputError
		if errors.As(runErr, &outputErr) {
			item.Error = outputErr.Error()
		}
		item.Failure = classifyFailure(runErr)
		if j.item.WebsiteSession != nil && item.Failure == download.FailureAuthorization {
			item.Error += ". The website session may have expired. " + websiteSessionRecovery
		}
		if errors.Is(runErr, download.ErrRestartRequired) {
			item.Recovery = download.RecoveryRestartRequired
		}
	}
	if err := m.repo.Update(context.Background(), item); err != nil {
		// The row stays "running"; requeueOrphans recovers it on the next tick.
		m.log.Error("record finished download", "id", id, "status", item.Status, "err", err)
		return
	}
	m.log.Info("download stopped", "id", id, "status", item.Status, "completed", item.Completed, "total", item.Total, "failure", item.Failure)
	m.publish(download.EventUpdated, item)
}

// publishProgress coalesces engine progress reports into one event per
// running download whose byte counts changed.
func (m *Manager) publishProgress() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.active {
		completed, total := j.progress()
		if j.item.Status != download.StatusRunning || (completed == j.item.Completed && total == j.item.Total) {
			continue
		}
		j.item.Completed, j.item.Total = completed, total
		m.publish(download.EventProgress, j.item)
	}
}

// flush persists live progress of running downloads. UpdateProgress is
// conditional on the running status, so it may run outside the lock.
// ponytail: a flush racing Pause→Resume can briefly store older bytes; the
// next flush corrects them.
func (m *Manager) flush(ctx context.Context) {
	type pending struct {
		id               string
		completed, total int64
	}
	m.mu.Lock()
	batch := make([]pending, 0, len(m.active))
	for id, j := range m.active {
		if j.item.Status == download.StatusRunning {
			completed, total := j.progress()
			batch = append(batch, pending{id, completed, total})
		}
	}
	m.mu.Unlock()
	for _, p := range batch {
		if err := m.repo.UpdateProgress(ctx, p.id, p.completed, p.total); err != nil {
			m.log.Error("flush download progress", "id", p.id, "err", err)
		}
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.active {
		j.cancel()
	}
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// The caller holds m.mu, so snapshots and events share one order.
func (m *Manager) publish(eventType download.EventType, item download.Item) {
	m.sequence++
	m.opts.Publish(download.Event{Sequence: m.sequence, Type: eventType, Item: item})
}

func validateURL(rawURL string) error {
	if rawURL == "" {
		return errors.New("download URL is required")
	}
	u, err := url.ParseRequestURI(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("download URL must be an absolute http or https URL")
	}
	return nil
}

// validateOptions checks per-download options, clamping nothing.
func validateOptions(priority, conns *int, speedLimit *int64) error {
	switch {
	case *priority < download.PriorityLow || *priority > download.PriorityHigh:
		return fmt.Errorf("priority must be %d, %d, or %d", download.PriorityLow, download.PriorityNormal, download.PriorityHigh)
	case *conns < 0 || *conns > settings.MaxConnections:
		return fmt.Errorf("connections must be in [1, %d], or 0 for the default", settings.MaxConnections)
	case *speedLimit < 0:
		return errors.New("speed limit cannot be negative")
	}
	return nil
}

func (m *Manager) inspect(ctx context.Context, rawURL string, headers http.Header, timeout time.Duration) (download.Remote, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return m.runner.Inspect(ctx, rawURL, headers)
}

func remoteName(rawURL string, remote download.Remote) string {
	if remote.URL == "" {
		remote.URL = rawURL
	}
	name := fileName(remote.Filename, remote.URL)
	// An HLS playlist is saved as the MPEG-TS stream it describes.
	if hls.Detect(remote.ContentType, remote.URL, nil) {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".ts"
	}
	return name
}

// headers merges the site's headers and credentials with the download's own.
func (m *Manager) headers(host string, own map[string]string) http.Header {
	h := m.Settings().HeadersFor(host)
	for name, value := range own {
		h.Set(name, value)
	}
	return h
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// limitOf converts bytes per second to a limiter rate; 0 is unlimited.
func limitOf(bytesPerSecond int64) rate.Limit {
	if bytesPerSecond <= 0 {
		return rate.Inf
	}
	return rate.Limit(bytesPerSecond)
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("create download ID: %w", err)
	}
	return hex.EncodeToString(data[:]), nil
}
