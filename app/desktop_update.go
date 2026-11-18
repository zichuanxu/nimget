package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/update"
)

type desktopUpdates struct {
	ctx       context.Context
	current   string
	checker   update.Checker
	wake      chan struct{}
	checkMu   sync.Mutex
	queueMu   sync.Mutex
	available atomic.Pointer[update.Release]
}

func (d *Desktop) startReleaseChecks(ctx context.Context) {
	if !update.Versioned(version) {
		return
	}
	dir, err := service.DataDir()
	if err != nil {
		d.log.Warn("initialize release checks", "err", err)
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = netproxy.Func(func() netproxy.Config { return d.mgr.Settings().Proxy })
	d.updates = &desktopUpdates{
		ctx: ctx, current: version, wake: make(chan struct{}, 1),
		checker: update.Checker{
			Client: &http.Client{
				Transport: transport, Timeout: 20 * time.Second,
				CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			},
			URL: update.ReleasesURL, Path: update.StatePath(dir), Platform: update.CurrentPlatform(),
		},
	}
	go d.watchReleases()
}

// checkReleases serializes background and manual checks, including publishing
// the result, so an older response cannot overwrite a newer one.
func (d *Desktop) checkReleases(force bool) (*update.Release, error) {
	u := d.updates
	u.checkMu.Lock()
	defer u.checkMu.Unlock()
	if err := u.ctx.Err(); err != nil {
		return nil, err
	}
	check := u.checker.Check
	if force {
		check = u.checker.CheckNow
	} else if !d.mgr.Settings().Desktop.CheckUpdates {
		return nil, nil
	}
	rel, ok, err := check(u.ctx, u.current, time.Now())
	if err != nil {
		return nil, err
	}
	var available *update.Release
	if ok {
		available = &rel
	}
	previous := u.available.Swap(available)
	if d.app != nil && (previous != nil || available != nil) {
		d.app.Event.Emit(eventUpdate, rel)
	}
	return available, nil
}

func (d *Desktop) watchReleases() {
	u := d.updates
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-u.ctx.Done():
			return
		case <-timer.C:
		case <-u.wake:
		}
		if _, err := d.checkReleases(false); err != nil && u.ctx.Err() == nil {
			d.log.Warn("update check", "err", err)
		}
		// The persistent cache enforces the daily cadence across restarts.
		timer.Reset(time.Hour)
	}
}

type UpdateCheck struct {
	Update *update.Release `json:"update,omitempty"`
}

// CheckForUpdate checks immediately, even if automatic checks are off.
func (d *Desktop) CheckForUpdate() (UpdateCheck, error) {
	if _, err := d.manager(); err != nil {
		return UpdateCheck{}, err
	}
	if d.updates == nil {
		return UpdateCheck{}, errors.New("this build cannot check for updates")
	}
	release, err := d.checkReleases(true)
	return UpdateCheck{Update: release}, err
}

// DownloadUpdate queues the offered package through the regular download engine,
// which verifies the published digest before completing the task.
func (d *Desktop) DownloadUpdate(expectedVersion string) (download.Item, error) {
	m, err := d.manager()
	if err != nil {
		return download.Item{}, err
	}
	u := d.updates
	if u == nil {
		return download.Item{}, errors.New("there is no update to download")
	}
	u.queueMu.Lock()
	defer u.queueMu.Unlock()
	release := u.available.Load()
	if release == nil || release.Version != expectedVersion {
		return download.Item{}, errors.New("the offered update has changed")
	}
	req, err := release.DownloadRequest(u.checker.Platform)
	if err != nil {
		return download.Item{}, err
	}
	items, err := m.List(u.ctx)
	if err != nil {
		return download.Item{}, err
	}
	for _, item := range items {
		if item.URL != req.URL || !strings.EqualFold(item.Checksum, req.Checksum) {
			continue
		}
		if item.Status == download.StatusCompleted {
			if exists, _ := downloadedFileExists(item.Destination); !exists {
				continue
			}
		}
		return item, nil
	}
	return m.Add(u.ctx, req)
}
