package manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/amemiya02/nimget/internal/download"
)

var errMagnetDuplicate = errors.New("torrent already has a download task")

func isMagnetOwner(item download.Item) bool {
	return item.Magnet != nil && item.Status != download.StatusCompleted && item.DuplicateOf == ""
}

func matchesMagnetClaim(item download.Item, claim download.TorrentIdentity) bool {
	if !isMagnetOwner(item) {
		return false
	}
	source := item.Magnet
	if source.Verified != nil {
		return source.Verified.Contains(claim)
	}
	// An unverified second claim could be false. Only deduplicate identical
	// claims until the metadata has proven their relationship.
	return source.TorrentIdentity == claim
}

// reconcileMagnetIdentity runs before payload storage can be opened. The
// manager lock makes this decision indivisible from submission and scheduling.
func (m *Manager) reconcileMagnetIdentity(ctx context.Context, j *job, verified download.TorrentIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	items, err := m.repo.List(ctx)
	if err != nil {
		return err
	}
	owner := j.item
	var matching []download.Item
	for _, item := range items {
		if !isMagnetOwner(item) || !verified.Contains(item.Magnet.TorrentIdentity) {
			continue
		}
		matching = append(matching, item)
		// Keep a prior reservation or verified owner, including work saved by
		// older versions that did not persist verified aliases separately.
		if owner.Destination == "" && owner.Magnet.Verified == nil && (item.Destination != "" || item.Magnet.Verified != nil) {
			owner = item
		}
	}
	if owner.Magnet.Verified != nil && *owner.Magnet.Verified == verified && len(matching) == 1 {
		return nil
	}
	now := m.opts.Now().UTC()
	source := *owner.Magnet
	source.Verified = &verified
	owner.Magnet, owner.UpdatedAt = &source, now
	if running := m.active[owner.ID]; running != nil {
		owner.Completed, owner.Total = running.progress()
	}
	var duplicates []download.Item
	for _, item := range matching {
		if item.ID == owner.ID {
			continue
		}
		source := *item.Magnet
		source.DuplicateOf = owner.ID
		item.Magnet, item.DuplicateOf = &source, owner.ID
		item.Status, item.Phase = download.StatusFailed, ""
		item.Error = fmt.Sprintf("This torrent already has a download task (%s).", owner.ID)
		item.Failure, item.Recovery = "", ""
		item.UpdatedAt = now
		duplicates = append(duplicates, item)
	}
	if err := m.repo.UpdateMagnetOwnership(ctx, owner, duplicates); err != nil {
		return err
	}
	m.publish(download.EventUpdated, owner)
	if running := m.active[owner.ID]; running != nil {
		running.item = owner
	}
	for _, item := range duplicates {
		delete(m.retryAt, item.ID)
		if running := m.active[item.ID]; running != nil {
			running.item = item
			running.cancel()
		}
		m.publish(download.EventUpdated, item)
	}
	if owner.ID != j.item.ID {
		return errMagnetDuplicate
	}
	return nil
}
