package manager

import (
	"errors"

	"golang.org/x/sys/windows"
)

func diskFull(err error) bool {
	return errors.Is(err, windows.ERROR_DISK_FULL) || errors.Is(err, windows.ERROR_HANDLE_DISK_FULL)
}
