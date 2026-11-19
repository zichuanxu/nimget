//go:build linux || darwin

package partialfile

import (
	"os"

	"golang.org/x/sys/unix"
)

func openRooted(parent *os.File, name string, writable, create bool) (*os.File, error) {
	flags := unix.O_NONBLOCK | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if writable {
		flags |= unix.O_RDWR
	}
	if create {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func moveRooted(parent *os.File, src, dest string, expected Object) error {
	object, err := inspectRooted(parent, src)
	if err != nil {
		return err
	}
	if object != expected {
		return Collision("publication source has changed")
	}
	return renameRooted(parent, src, parent, dest, expected)
}

func renameRooted(parent *os.File, src string, target *os.File, dest string, _ Object) error {
	return exclusiveRename(int(parent.Fd()), src, int(target.Fd()), dest)
}

func mkdirPrivate(root *os.Root, name string) error {
	return root.Mkdir(name, 0o700)
}

func removePrivateDirectory(parent *os.File, name string, expected Object) error {
	actual, err := inspectRooted(parent, name)
	if err != nil {
		return err
	}
	if actual != expected {
		return Collision("private directory was replaced")
	}
	return unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
}

func syncRoot(parent *os.File) error { return parent.Sync() }
