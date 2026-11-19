package partialfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Object identifies a real file or directory by its identity on the current
// volume. An occupied leaf that cannot be owned (such as a link) has no ID.
type Object struct {
	ID        string
	Directory bool
}

func leafOK(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.IsLocal(name) &&
		!strings.ContainsAny(name, "/\\:\x00")
}

// RootIdentity identifies the opened directory itself, even if its name changes.
func RootIdentity(root *os.Root) (string, error) {
	parent, err := root.Open(".")
	if err != nil {
		return "", err
	}
	defer parent.Close()
	object, err := handleIdentity(parent)
	if err == nil && (!object.Directory || object.ID == "") {
		return "", errors.New("download directory identity is unavailable")
	}
	return object.ID, err
}

// InspectRooted checks a single leaf without following links.
func InspectRooted(root *os.Root, name string) (Object, error) {
	if !leafOK(name) {
		return Object{}, Collision("invalid leaf name")
	}
	parent, err := root.Open(".")
	if err != nil {
		return Object{}, err
	}
	defer parent.Close()
	return inspectRooted(parent, name)
}

// OpenRootedDirectory opens a real child directory and checks that its handle
// matches both no-follow inspections of the leaf.
func OpenRootedDirectory(root *os.Root, name string) (*os.Root, Object, error) {
	object, err := InspectRooted(root, name)
	if err != nil {
		return nil, Object{}, err
	}
	if !object.Directory || object.ID == "" {
		return nil, Object{}, Collision("download directory was replaced")
	}
	child, err := root.OpenRoot(name)
	if err != nil {
		return nil, Object{}, err
	}
	id, err := RootIdentity(child)
	if err == nil {
		var current Object
		current, err = InspectRooted(root, name)
		if err == nil && (id != object.ID || current != object) {
			err = Collision("download directory was replaced")
		}
	}
	if err != nil {
		child.Close()
		return nil, Object{}, err
	}
	return child, object, nil
}

// OpenRooted opens a regular partial file without following links under the
// opened directory. The handle pins writes to the same object that was checked.
func OpenRooted(root *os.Root, name string, writable, create bool) (*os.File, Object, error) {
	if !leafOK(name) {
		return nil, Object{}, Collision("invalid leaf name")
	}
	parent, err := root.Open(".")
	if err != nil {
		return nil, Object{}, err
	}
	defer parent.Close()
	file, err := openRooted(parent, name, writable, create)
	if err != nil {
		return nil, Object{}, err
	}
	if err := validate(file); err != nil {
		file.Close()
		return nil, Object{}, err
	}
	object, err := handleIdentity(file)
	if err != nil || object.ID == "" || object.Directory {
		file.Close()
		return nil, Object{}, errors.Join(Collision("file identity is unavailable"), err)
	}
	return file, object, nil
}

// MoveRooted publishes a file or directory to an absent leaf on the same
// volume. Both leaf names are relative to the same opened directory.
func MoveRooted(root *os.Root, source, dest string, expected Object) error {
	if !leafOK(source) || !leafOK(dest) || expected.ID == "" {
		return Collision("invalid publication source or destination")
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return moveRooted(parent, source, dest, expected)
}

// RenameRooted moves an occupied leaf into an absent leaf on the same volume.
// Cleanup uses it to isolate the object before establishing ownership or unlinking.
func RenameRooted(from *os.Root, source string, to *os.Root, dest string) error {
	if !leafOK(source) || !leafOK(dest) {
		return Collision("invalid rooted rename source or destination")
	}
	parent, err := from.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	target, err := to.Open(".")
	if err != nil {
		return err
	}
	defer target.Close()
	return renameRooted(parent, source, target, dest, Object{})
}

// MkdirPrivate creates a directory confined to the current user.
func MkdirPrivate(root *os.Root, name string) error {
	if !leafOK(name) {
		return Collision("invalid private directory name")
	}
	return mkdirPrivate(root, name)
}

// RemovePrivateDirectory removes an empty cleanup directory and preserves an
// occupied slot when an entry could not be restored to its original name.
func RemovePrivateDirectory(root *os.Root, name string, expected Object) error {
	if !leafOK(name) || !expected.Directory || expected.ID == "" {
		return Collision("invalid private directory")
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return removePrivateDirectory(parent, name, expected)
}

func SyncRoot(root *os.Root) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	return syncRoot(parent)
}
