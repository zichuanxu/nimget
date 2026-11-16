package partialfile

import (
	"fmt"
	"io/fs"
	"os"
)

// Open opens a regular partial file without following a link at path and
// validates the opened file before returning it. Callers truncate explicitly
// on the returned handle so a collision cannot cause writes during open.
func Open(path string, flag int, mode fs.FileMode) (*os.File, error) {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() || isReparsePoint(info) {
			return nil, unsafeCollision(path, "expected a regular file")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	f, err := open(path, flag&^os.O_TRUNC, mode)
	if err != nil {
		return nil, err
	}
	if err := validate(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// CheckDirectory rejects a partial work directory that would redirect writes.
func CheckDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || isReparsePoint(info) {
		return unsafeCollision(path, "expected a directory without links")
	}
	return nil
}

func unsafeCollision(path, reason string) error {
	return fmt.Errorf("unsafe partial path %q (%s), remove or rename the partial path and retry", path, reason)
}
