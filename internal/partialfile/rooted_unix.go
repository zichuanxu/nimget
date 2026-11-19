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
	return exclusiveRename(int(parent.Fd()), src, dest)
}

func syncRoot(parent *os.File) error { return parent.Sync() }
