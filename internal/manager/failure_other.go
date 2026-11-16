//go:build !windows

package manager

import (
	"errors"
	"syscall"
)

func diskFull(err error) bool {
	return errors.Is(err, syscall.ENOSPC)
}
