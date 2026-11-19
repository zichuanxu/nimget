package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/partialfile"
	"github.com/amemiya02/nimget/internal/quarantine"
)

func openMagnetDirectoryStage(root *os.Root, stage *download.MagnetObject, manifest magnet.Manifest, entries []download.MagnetEntry) (magnet.StagedPayload, *download.MagnetObject, bool, error) {
	rootID, err := partialfile.RootIdentity(root)
	if err != nil {
		return nil, nil, false, err
	}
	if stage != nil {
		if rootID != stage.RootID || !stage.Directory {
			return nil, nil, false, errors.New("the magnet save directory or staging record changed")
		}
		tree, err := openMagnetTree(root, stage.Name, stage)
		if err == nil {
			if err := tree.checkLayout(); err != nil || !sameMagnetEntries(entries, stage.Entries) {
				tree.root.Close()
				return nil, nil, false, errors.Join(errors.New("the staged magnet directory changed"), err)
			}
			return directoryStorage(manifest, tree, stage.Entries, false), stage, false, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, false, err
		}
	}
	name := ".nimget-magnet-" + rand.Text() + ".part"
	if err := root.Mkdir(name, 0o700); err != nil {
		return nil, nil, false, err
	}
	child, actual, err := partialfile.OpenRootedDirectory(root, name)
	if err != nil {
		return nil, nil, false, err
	}
	stage = &download.MagnetObject{RootID: rootID, Name: name, ID: actual.ID, Directory: true, Entries: entries}
	tree := newMagnetTree(child, entries)
	if err := errors.Join(tree.create(entries), partialfile.SyncRoot(root)); err != nil {
		child.Close()
		_ = removeMagnetObject(context.Background(), root, stage)
		return nil, nil, false, err
	}
	return directoryStorage(manifest, tree, entries, true), stage, true, nil
}

func directoryStorage(manifest magnet.Manifest, tree magnetTree, entries []download.MagnetEntry, fresh bool) magnet.StagedPayload {
	return magnet.NewDirectoryStorage(manifest, tree.openFile, func() error {
		return tree.sync(entries, true)
	}, tree.root.Close, !fresh)
}

func sameMagnetEntries(a, b []download.MagnetEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i, entry := range a {
		if other := b[i]; entry.Path != other.Path || entry.Size != other.Size || entry.Directory != other.Directory {
			return false
		}
	}
	return true
}

func openMagnetTree(root *os.Root, name string, expected *download.MagnetObject) (magnetTree, error) {
	child, actual, err := partialfile.OpenRootedDirectory(root, name)
	if err != nil {
		return magnetTree{}, err
	}
	if actual != stagedObject(expected) {
		child.Close()
		return magnetTree{}, errors.New("the download directory was replaced")
	}
	return newMagnetTree(child, expected.Entries), nil
}

func (t magnetTree) checkLayout() error {
	if len(t.entries) == 0 {
		return errors.New("download directory ownership is unavailable")
	}
	// A mismatch in either direction prevents publishing a tree containing
	// files or directories added to the staging area by another process.
	var seen int
	check := func(root *os.Root, prefix string) error {
		listing, err := root.Open(".")
		if err != nil {
			return err
		}
		defer listing.Close()
		files, err := listing.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, file := range files {
			entry := t.entries[prefix+file.Name()]
			actual, err := partialfile.InspectRooted(root, file.Name())
			if err != nil {
				return err
			}
			if entry.ID == "" || actual.ID != entry.ID || actual.Directory != entry.Directory {
				return errors.New("the download directory layout changed")
			}
			seen++
		}
		return nil
	}
	if err := check(t.root, ""); err != nil {
		return err
	}
	for rel, entry := range t.entries {
		if entry.Directory {
			if err := t.withDirectory(rel, func(root *os.Root) error { return check(root, rel+"/") }); err != nil {
				return err
			}
		}
	}
	if seen != len(t.entries) {
		return errors.New("the download directory layout changed")
	}
	return nil
}

func magnetManifestDigest(entries []download.MagnetEntry) string {
	encoded, _ := json.Marshal(entries) // Entries contain only primitive values.
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func sealMagnetDirectory(ctx context.Context, root *os.Root, stage *download.MagnetObject, manifest magnet.Manifest, source string) error {
	tree, err := openMagnetTree(root, stage.Name, stage)
	if err != nil {
		return err
	}
	defer tree.root.Close()
	if err := tree.checkLayout(); err != nil {
		return err
	}
	verified := make(map[string]string, len(stage.Entries))
	if err := manifest.VerifyDirectory(ctx, func(file magnet.PayloadFile, verify func(*os.File) (string, error)) error {
		rel := strings.Join(file.Path, "/")
		return tree.visit(rel, func(parent *os.Root, leaf string) (err error) {
			f, err := tree.openLeaf(parent, leaf, tree.entries[rel], true)
			if err != nil {
				return err
			}
			defer func() { err = errors.Join(err, f.Close()) }()
			digest, err := verify(f)
			if err != nil {
				return err
			}
			if err := quarantine.MarkRooted(parent, f, leaf, source); err != nil {
				return err
			}
			verified[rel] = digest
			return f.Sync()
		})
	}); err != nil {
		return err
	}
	stage.Entries = append([]download.MagnetEntry(nil), stage.Entries...)
	for i := range stage.Entries {
		stage.Entries[i].Digest = verified[stage.Entries[i].Path]
	}
	stage.Size, stage.Digest = manifest.Total, magnetManifestDigest(stage.Entries)
	return errors.Join(tree.sync(stage.Entries, false), partialfile.SyncRoot(root))
}

func verifyMagnetDirectory(ctx context.Context, root *os.Root, name string, expected *download.MagnetObject) error {
	if expected.Digest != magnetManifestDigest(expected.Entries) {
		return errors.New("the saved download directory manifest is inconsistent")
	}
	tree, err := openMagnetTree(root, name, expected)
	if err != nil {
		return err
	}
	defer tree.root.Close()
	if err := tree.checkLayout(); err != nil {
		return err
	}
	for _, entry := range expected.Entries {
		if entry.Directory {
			continue
		}
		if err := tree.visit(entry.Path, func(parent *os.Root, leaf string) error {
			return tree.verifyLeaf(ctx, parent, leaf, entry)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (t magnetTree) verifyLeaf(ctx context.Context, parent *os.Root, leaf string, entry download.MagnetEntry) error {
	file, err := t.openLeaf(parent, leaf, entry, false)
	if err != nil {
		return err
	}
	defer file.Close()
	digest, err := magnet.FileDigest(ctx, file, entry.Size)
	if err != nil {
		return err
	}
	if digest != entry.Digest {
		return errors.New("the download file contents changed")
	}
	return nil
}

func removeMagnetDirectoryEntries(ctx context.Context, slot *os.Root, name string, object *download.MagnetObject) error {
	tree, err := openMagnetTree(slot, name, object)
	if err != nil {
		return err
	}
	var cleanupErr error
	for i := len(object.Entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			cleanupErr = errors.Join(cleanupErr, err)
			break
		}
		entry := object.Entries[i]
		if entry.ID == "" {
			continue
		}
		err := tree.visit(entry.Path, func(parent *os.Root, leaf string) error {
			held := "entry-" + strconv.Itoa(i)
			expected := partialfile.Object{ID: entry.ID, Directory: entry.Directory}
			return removeMagnetLeaf(parent, leaf, slot, held, expected, func() error {
				if entry.Digest != "" {
					return tree.verifyLeaf(ctx, slot, held, entry)
				}
				return nil
			})
		})
		if !errors.Is(err, os.ErrNotExist) && cleanupErr == nil {
			cleanupErr = err
		}
	}
	return errors.Join(cleanupErr, tree.root.Close())
}
