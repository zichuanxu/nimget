package partialfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func open(path string, flag int, _ fs.FileMode) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Add the extended-length prefix when the path could exceed MAX_PATH.
	if len(abs) >= 248 {
		switch {
		case strings.HasPrefix(abs, `\\?\`), strings.HasPrefix(abs, `\\.\`), strings.HasPrefix(abs, `\??\`):
		case strings.HasPrefix(abs, `\\`):
			abs = `\\?\UNC\` + abs[2:]
		default:
			abs = `\\?\` + abs
		}
	}
	name, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return nil, err
	}
	access := uint32(windows.GENERIC_READ)
	switch {
	case flag&os.O_RDWR != 0:
		access |= windows.GENERIC_WRITE
	case flag&os.O_WRONLY != 0:
		access = windows.GENERIC_WRITE
	}
	disposition := uint32(windows.OPEN_EXISTING)
	if flag&os.O_CREATE != 0 {
		disposition = windows.OPEN_ALWAYS
		if flag&os.O_EXCL != 0 {
			disposition = windows.CREATE_NEW
		}
	}
	handle, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		disposition, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func isReparsePoint(info fs.FileInfo) bool {
	return info.Sys().(*syscall.Win32FileAttributeData).FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

func validate(f *os.File) error {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(f.Fd()), &info); err != nil {
		return &os.PathError{Op: "stat", Path: f.Name(), Err: err}
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 ||
		info.NumberOfLinks != 1 {
		return unsafeCollision(f.Name(), "expected a regular file with one link and no reparse point")
	}
	return nil
}
