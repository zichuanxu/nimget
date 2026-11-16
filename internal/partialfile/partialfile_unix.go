//go:build unix

package partialfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func open(path string, flag int, mode fs.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, flag|unix.O_NOFOLLOW|unix.O_NONBLOCK, mode)
	if errors.Is(err, unix.ELOOP) {
		return nil, Collision("symbolic link")
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
		return Collision("expected a regular file with one link")
	}
	return nil
}

// Identity binds a checkpoint to the file opened through its partial path.
func Identity(f *os.File) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	stat := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%x:%x", stat.Dev, stat.Ino), nil
}
