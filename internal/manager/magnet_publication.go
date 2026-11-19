package manager

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/partialfile"
	"github.com/amemiya02/nimget/internal/quarantine"
)

type magnetOutputError struct {
	reason string
	cause  error
}

func (e *magnetOutputError) Error() string {
	return e.reason + " " + e.cause.Error() + ". Retry after checking the save folder."
}
func (e *magnetOutputError) Unwrap() error { return e.cause }

func publicationError(err error) error {
	return &magnetOutputError{"Could not publish the verified magnet output.", err}
}

// publishMagnet seals the staged bytes before taking the manager lock. After
// writing intent, only reconciliation may choose a final path or complete it.
func (m *Manager) publishMagnet(ctx context.Context, j *job, root *os.Root, stage download.MagnetObject, name string, manifest magnet.Manifest, sourceURL string) error {
	var err error
	if stage.Directory {
		err = sealMagnetDirectory(ctx, root, &stage, manifest, sourceURL)
	} else {
		err = sealMagnetFile(ctx, root, &stage, manifest, sourceURL)
	}
	if err != nil {
		return publicationError(err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	item := j.item
	source := *item.Magnet
	source.Stage = &stage
	source.Publication = &download.MagnetPublication{BaseName: name}
	item.Magnet = &source
	item.Completed, item.Total = j.progress()
	item.UpdatedAt = m.opts.Now().UTC()
	if err := m.checkMagnetRoot(item, root, stage.RootID); err != nil {
		return publicationError(err)
	}
	if err := m.saveMagnetCheckpoint(ctx, item); err != nil {
		return publicationError(err)
	}
	j.item = item
	m.publicationCheckpoint(download.MagnetPublicationRecorded, item)

	// Once the intent exists, finish the local move independently of a
	// shutdown or network-policy cancellation.
	if err := m.reconcileMagnetPublication(context.WithoutCancel(ctx), &item, root, true); err != nil {
		return publicationError(err)
	}
	j.item = item
	return nil
}

func sealMagnetFile(ctx context.Context, root *os.Root, stage *download.MagnetObject, manifest magnet.Manifest, source string) error {
	file, actual, err := partialfile.OpenRooted(root, stage.Name, true, false)
	if err != nil {
		return err
	}
	defer file.Close()
	if actual != stagedObject(stage) {
		return errors.New("the staged file changed")
	}
	digest, err := manifest.VerifySingleFile(ctx, file)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := quarantine.MarkRooted(root, file, stage.Name, source); err != nil {
		return err
	}
	stage.Size, stage.Digest = manifest.Total, digest
	return errors.Join(file.Sync(), partialfile.SyncRoot(root))
}

func stagedObject(stage *download.MagnetObject) partialfile.Object {
	return partialfile.Object{ID: stage.ID, Directory: stage.Directory}
}

func (m *Manager) checkMagnetRoot(item download.Item, root *os.Root, expected string) error {
	dir, err := m.confineMagnetDirectory(item.Directory)
	if err != nil {
		return err
	}
	if !samePath(root.Name(), dir) {
		return errors.New("the magnet save directory changed")
	}
	opened, err := partialfile.RootIdentity(root)
	if err != nil {
		return err
	}
	current, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer current.Close()
	id, err := partialfile.RootIdentity(current)
	if err != nil {
		return err
	}
	if opened != expected || id != expected {
		return errors.New("the magnet save directory changed")
	}
	return nil
}

// recoverMagnetPublication is called under the manager lock before a task
// with a pending move can start network activity or be removed.
func (m *Manager) recoverMagnetPublication(ctx context.Context, item *download.Item) error {
	dir, err := m.confineMagnetDirectory(item.Directory)
	if err != nil {
		return publicationError(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return publicationError(err)
	}
	defer root.Close()
	if err := m.reconcileMagnetPublication(ctx, item, root, false); err != nil {
		return publicationError(err)
	}
	return nil
}

func hasMagnetPublication(item download.Item) bool {
	return item.Magnet != nil && item.Magnet.Publication != nil
}

// reconcileMagnetPublication handles both sides of the move. A recorded
// identity can match exactly one location and the saved bytes must agree.
func (m *Manager) reconcileMagnetPublication(ctx context.Context, item *download.Item, root *os.Root, sealed bool) error {
	stage := item.Magnet.Stage
	if stage == nil || stage.ID == "" || stage.Digest == "" ||
		!samePath(filepath.Dir(item.Destination), root.Name()) || samePath(stage.Name, filepath.Base(item.Destination)) {
		return errors.New("the saved publication record is inconsistent")
	}
	if err := m.checkMagnetRoot(*item, root, stage.RootID); err != nil {
		return err
	}
	conflict := errors.New("the staged and final paths do not establish ownership of the verified output")
	expected := stagedObject(stage)
	for range 10000 {
		destName := filepath.Base(item.Destination)
		staged, stageMissing, err := inspectPublicationLeaf(root, stage.Name)
		if err != nil {
			return err
		}
		dest, destMissing, err := inspectPublicationLeaf(root, destName)
		if err != nil {
			return err
		}
		switch {
		case stageMissing && dest == expected:
			if err := verifyMagnetObject(ctx, root, destName, stage); err != nil {
				return err
			}
			return m.completeMagnetPublication(ctx, item, root, destName)

		case staged != expected || dest == expected:
			return conflict
		}

		if !sealed {
			if err := verifyMagnetObject(ctx, root, stage.Name, stage); err != nil {
				return err
			}
			sealed = true
		}
		if !destMissing {
			if err := m.changePublicationReservation(ctx, item, root.Name()); err != nil {
				return err
			}
			continue
		}
		if err := partialfile.MoveRooted(root, stage.Name, destName, expected); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("move verified output: %w", err)
		}
		m.publicationCheckpoint(download.MagnetPublicationMoved, *item)
		dest, err = partialfile.InspectRooted(root, destName)
		if err != nil {
			return err
		}
		if dest != expected {
			return conflict
		}
		if stage.Directory {
			if err := verifyMagnetDirectory(ctx, root, destName, stage); err != nil {
				return err
			}
		}
		return m.completeMagnetPublication(ctx, item, root, destName)
	}
	return errors.New("no free destination for the magnet")
}

func inspectPublicationLeaf(root *os.Root, name string) (partialfile.Object, bool, error) {
	object, err := partialfile.InspectRooted(root, name)
	if errors.Is(err, os.ErrNotExist) {
		return object, true, nil
	}
	return object, false, err
}

func verifyMagnetObject(ctx context.Context, root *os.Root, name string, expected *download.MagnetObject) error {
	if expected.Directory {
		return verifyMagnetDirectory(ctx, root, name, expected)
	}
	f, actual, err := partialfile.OpenRooted(root, name, false, false)
	if err != nil {
		return err
	}
	defer f.Close()
	if actual != stagedObject(expected) {
		return errors.New("the download file was replaced")
	}
	digest, err := magnet.FileDigest(ctx, f, expected.Size)
	if err != nil {
		return err
	}
	if digest != expected.Digest {
		return errors.New("the download file contents changed")
	}
	return nil
}

func (m *Manager) changePublicationReservation(ctx context.Context, item *download.Item, dir string) error {
	dest, err := m.reserveMagnetDestination(ctx, item.ID, dir, item.Magnet.Publication.BaseName)
	if err != nil {
		return err
	}
	reserved := *item
	reserved.Destination, reserved.Name = dest, filepath.Base(dest)
	reserved.UpdatedAt = m.opts.Now().UTC()
	if err := m.saveMagnetCheckpoint(ctx, reserved); err != nil {
		return err
	}
	*item = reserved
	return nil
}

func (m *Manager) completeMagnetPublication(ctx context.Context, item *download.Item, root *os.Root, name string) error {
	if err := m.checkMagnetRoot(*item, root, item.Magnet.Stage.RootID); err != nil {
		return err
	}
	if err := partialfile.SyncRoot(root); err != nil {
		return err
	}
	completed := *item
	source := *item.Magnet
	output := *source.Stage
	output.Name = name
	source.Output, source.Stage, source.Publication = &output, nil, nil
	source.Metadata = []byte{}
	completed.Magnet = &source
	completed.Status, completed.Phase = download.StatusCompleted, ""
	completed.Error, completed.Failure = "", ""
	completed.Completed = completed.Total
	completed.UpdatedAt = m.opts.Now().UTC()
	if err := m.saveMagnetCheckpoint(ctx, completed); err != nil {
		return err
	}
	*item = completed
	m.publishEvent(download.Event{Type: download.EventUpdated, Item: completed, CompletedMagnetPublication: true})
	return nil
}

func (m *Manager) recordPublicationFailure(ctx context.Context, item download.Item, err error) error {
	item.Status, item.Phase = download.StatusFailed, ""
	item.Error = err.Error()
	item.Failure = classifyFailure(err)
	item.UpdatedAt = m.opts.Now().UTC()
	if err := m.repo.Update(ctx, item); err != nil {
		return err
	}
	m.publish(download.EventUpdated, item)
	return nil
}

func (m *Manager) publicationCheckpoint(step download.MagnetPublicationStep, item download.Item) {
	if checkpoint := m.opts.MagnetPublicationCheckpoint; checkpoint != nil {
		checkpoint(step, item)
	}
}

func (m *Manager) recoverQueuedMagnetPublication(ctx context.Context, id string) error {
	item, err := m.repo.Get(ctx, id)
	if errors.Is(err, download.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if item.Status != download.StatusQueued || !hasMagnetPublication(item) {
		return nil
	}
	return m.retryMagnetPublication(ctx, item)
}

func (m *Manager) retryMagnetPublication(ctx context.Context, item download.Item) error {
	if err := m.recoverMagnetPublication(ctx, &item); err != nil {
		return m.recordPublicationFailure(ctx, item, err)
	}
	return nil
}

func (m *Manager) removeMagnetFiles(ctx context.Context, item download.Item) error {
	if item.Magnet == nil {
		return nil
	}
	object := item.Magnet.Stage
	if item.Status == download.StatusCompleted {
		object = item.Magnet.Output
		if object == nil {
			return &fileCleanupError{
				context: "download deleted, but its files were kept",
				cause:   errors.New("the saved record does not establish ownership"),
			}
		}
	}
	if object == nil {
		return nil
	}
	remove := func() error {
		dir, err := m.confineMagnetDirectory(item.Directory)
		if err != nil {
			return err
		}
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		defer root.Close()
		if err := m.checkMagnetRoot(item, root, object.RootID); err != nil {
			return err
		}
		return removeMagnetObject(ctx, root, object)
	}
	if err := remove(); err != nil {
		return &fileCleanupError{context: "download deleted, but its files were kept", cause: err}
	}
	return nil
}

func removeMagnetObject(ctx context.Context, root *os.Root, object *download.MagnetObject) error {
	slotName := ".nimget-cleanup-" + rand.Text()
	if err := partialfile.MkdirPrivate(root, slotName); err != nil {
		return err
	}
	defer root.Remove(slotName) // Remove only the empty slot; it may hold preserved data.
	slot, err := root.OpenRoot(slotName)
	if err != nil {
		return err
	}
	defer slot.Close()

	var entryErr error
	err = removeMagnetLeaf(root, object.Name, slot, "output", stagedObject(object), func() error {
		if object.Directory {
			entryErr = removeMagnetDirectoryEntries(ctx, slot, "output", object)
			// A nonempty directory will be restored even when its only
			// remaining entries were unknown to the original manifest.
			return nil
		}
		if object.Digest != "" {
			return verifyMagnetObject(ctx, slot, "output", object)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return errors.Join(entryErr, err)
}

// Never unlink through the original name after a separate ownership check.
// The exclusive move may pick up a replacement, so check the moved leaf and
// restore it by exclusive move if it does not belong to the task.
func removeMagnetLeaf(parent *os.Root, name string, slot *os.Root, held string, expected partialfile.Object, verify func() error) error {
	check := func(root *os.Root, leaf string) error {
		actual, err := partialfile.InspectRooted(root, leaf)
		if err != nil {
			return err
		}
		if expected.ID == "" || actual != expected {
			return errors.New("a download entry was replaced")
		}
		return nil
	}
	if err := check(parent, name); err != nil {
		return err
	}
	if err := partialfile.RenameRooted(parent, name, slot, held); err != nil {
		return err
	}
	err := check(slot, held)
	if err == nil {
		err = verify()
	}
	if err == nil {
		err = slot.Remove(held)
	}
	if err == nil {
		return nil
	}
	if restore := partialfile.RenameRooted(slot, held, parent, name); restore != nil {
		return errors.Join(err, fmt.Errorf("entry kept at %q: %w", filepath.Join(slot.Name(), held), restore))
	}
	return err
}
