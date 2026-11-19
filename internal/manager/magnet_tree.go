package manager

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/magnet"
	"github.com/amemiya02/nimget/internal/partialfile"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func magnetDirectoryEntries(manifest magnet.Manifest) ([]download.MagnetEntry, error) {
	var entries []download.MagnetEntry
	known := make(map[string]download.MagnetEntry)
	invalid := errors.New("torrent metadata contains unsafe or conflicting output paths")
	for _, file := range manifest.Files {
		if file.Pad {
			continue
		}
		if len(file.Path) == 0 {
			return nil, invalid
		}
		for i, component := range file.Path {
			if !validMagnetComponent(component) {
				return nil, invalid
			}
			rel := strings.Join(file.Path[:i+1], "/")
			key := norm.NFC.String(cases.Fold().String(rel))
			dir := i < len(file.Path)-1
			if existing, ok := known[key]; ok {
				if !dir || !existing.Directory || existing.Path != rel {
					return nil, invalid
				}
				continue
			}
			entry := download.MagnetEntry{Path: rel, Directory: dir}
			if !dir {
				entry.Size = file.Size
			}
			known[key] = entry
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func validMagnetComponent(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || !utf8.ValidString(name) ||
		strings.ContainsAny(name, `/\<>:"|?*`) || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return false
	}
	stem, _, _ := strings.Cut(name, ".")
	return !reservedWindowsName(strings.TrimSpace(stem)) && strings.IndexFunc(name, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) < 0
}

func confineMagnetEntries(destination string, entries []download.MagnetEntry) error {
	// The save directory is already checked and the validated manifest has
	// no links or traversal. Check the paths the move would actually create.
	// An occupant at the final leaf is foreign and handled by publication.
	for _, dir := range autostartDirs() {
		resolved, err := resolveExisting(dir)
		if err != nil || (!samePath(destination, resolved) && !within(destination, resolved)) {
			continue
		}
		for _, entry := range entries {
			candidate := filepath.Join(destination, filepath.FromSlash(entry.Path))
			if samePath(resolved, candidate) || within(resolved, candidate) {
				return errors.New("a torrent file would be saved in an autostart location")
			}
		}
	}
	return nil
}

type magnetTree struct {
	root    *os.Root
	entries map[string]download.MagnetEntry
}

func newMagnetTree(root *os.Root, entries []download.MagnetEntry) magnetTree {
	tree := magnetTree{root: root, entries: make(map[string]download.MagnetEntry, len(entries))}
	for _, entry := range entries {
		tree.entries[entry.Path] = entry
	}
	return tree
}

// visit pins each ancestor while opening its child relative to its handle.
// Every ancestor must be a real directory with its recorded identity.
func (t magnetTree) visit(rel string, fn func(*os.Root, string) error) error {
	parent, leaf := path.Split(rel)
	if parent == "" {
		return fn(t.root, leaf)
	}
	return t.withDirectory(strings.TrimSuffix(parent, "/"), func(root *os.Root) error {
		return fn(root, leaf)
	})
}

func (t magnetTree) withDirectory(rel string, fn func(*os.Root) error) error {
	entry := t.entries[rel]
	if !entry.Directory || entry.ID == "" {
		return errors.New("download directory ownership is unavailable")
	}
	return t.visit(rel, func(parent *os.Root, leaf string) error {
		child, actual, err := partialfile.OpenRootedDirectory(parent, leaf)
		if err != nil {
			return err
		}
		defer child.Close()
		if actual.ID != entry.ID {
			return errors.New("an owned download directory was replaced")
		}
		return fn(child)
	})
}

func (t magnetTree) openFile(rel string, writable bool) (file *os.File, err error) {
	err = t.visit(rel, func(parent *os.Root, leaf string) error {
		file, err = t.openLeaf(parent, leaf, t.entries[rel], writable)
		return err
	})
	return file, err
}

func (t magnetTree) openLeaf(parent *os.Root, leaf string, entry download.MagnetEntry, writable bool) (*os.File, error) {
	file, actual, err := partialfile.OpenRooted(parent, leaf, writable, false)
	if err != nil {
		return nil, err
	}
	if entry.Directory || actual.ID != entry.ID {
		file.Close()
		return nil, errors.New("an owned download file was replaced")
	}
	return file, nil
}

func (t magnetTree) create(entries []download.MagnetEntry) error {
	for i, entry := range entries {
		err := t.visit(entry.Path, func(parent *os.Root, leaf string) error {
			if entry.Directory {
				if err := parent.Mkdir(leaf, 0o700); err != nil {
					return err
				}
				actual, err := partialfile.InspectRooted(parent, leaf)
				if err != nil {
					return err
				}
				if !actual.Directory || actual.ID == "" {
					return errors.New("could not identify a staged directory")
				}
				entry.ID = actual.ID
				return nil
			}
			file, actual, err := partialfile.OpenRooted(parent, leaf, true, true)
			if err != nil {
				return err
			}
			entry.ID = actual.ID
			return file.Close()
		})
		entries[i], t.entries[entry.Path] = entry, entry
		if err != nil {
			return err
		}
	}
	return t.sync(entries, true)
}

func (t magnetTree) sync(entries []download.MagnetEntry, files bool) error {
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Directory {
			if err := t.withDirectory(entry.Path, partialfile.SyncRoot); err != nil {
				return err
			}
		} else if files {
			file, err := t.openFile(entry.Path, true)
			if err != nil {
				return err
			}
			if err := errors.Join(file.Sync(), file.Close()); err != nil {
				return err
			}
		}
	}
	return partialfile.SyncRoot(t.root)
}
