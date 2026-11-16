//go:build unix

package partialfile

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func open(path string, flag int, mode fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if errors.Is(err, unix.ELOOP) {
		return nil, unsafeCollision(path, "symbolic link")
	}
	return f, err
}

func isReparsePoint(fs.FileInfo) bool { return false }

func validate(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		return unsafeCollision(f.Name(), "expected a regular file with one link")
	}
	return nil
}
