package partialfile

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func openObject(parent *os.File, name string, access, disposition, share, opts uint32) (windows.Handle, error) {
	return openObjectWithSecurity(parent, name, access, disposition, share, opts, nil)
}

func openObjectWithSecurity(parent *os.File, name string, access, disposition, share, opts uint32, security *windows.SECURITY_DESCRIPTOR) (windows.Handle, error) {
	u, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return 0, err
	}
	attrs := windows.OBJECT_ATTRIBUTES{
		RootDirectory:      windows.Handle(parent.Fd()),
		ObjectName:         u,
		Attributes:         windows.OBJ_CASE_INSENSITIVE,
		SecurityDescriptor: security,
	}
	attrs.Length = uint32(unsafe.Sizeof(attrs))
	var handle windows.Handle
	err = windows.NtCreateFile(&handle, access|windows.SYNCHRONIZE, &attrs, &windows.IO_STATUS_BLOCK{}, nil,
		windows.FILE_ATTRIBUTE_NORMAL, share, disposition,
		opts|windows.FILE_OPEN_REPARSE_POINT|windows.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	switch {
	case errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND), errors.Is(err, windows.STATUS_OBJECT_PATH_NOT_FOUND), errors.Is(err, windows.STATUS_NO_SUCH_FILE):
		err = os.ErrNotExist
	case errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION):
		err = os.ErrExist
	}
	return handle, err
}

const readWriteShare = windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE
const deleteShare = readWriteShare | windows.FILE_SHARE_DELETE

func openRooted(parent *os.File, name string, writable, create bool) (*os.File, error) {
	access := uint32(windows.GENERIC_READ)
	disposition, share := uint32(windows.FILE_OPEN), uint32(deleteShare)
	if writable {
		access |= windows.GENERIC_WRITE
		share = readWriteShare
	}
	if create {
		disposition = windows.FILE_CREATE
	}
	handle, err := openObject(parent, name, access, disposition, share, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}

func inspectRooted(parent *os.File, name string) (Object, error) {
	handle, err := openObject(parent, name, windows.FILE_READ_ATTRIBUTES, windows.FILE_OPEN, deleteShare, 0)
	if err != nil {
		return Object{}, err
	}
	defer windows.CloseHandle(handle)
	return objectIdentity(handle)
}

func handleIdentity(file *os.File) (Object, error) {
	return objectIdentity(windows.Handle(file.Fd()))
}

func objectIdentity(handle windows.Handle) (Object, error) {
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return Object{}, err
	}
	dir := info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || !dir && info.NumberOfLinks != 1 {
		return Object{}, nil
	}
	var id struct {
		Volume uint64
		ID     [16]byte
	}
	if err := windows.GetFileInformationByHandleEx(handle, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id))); err != nil {
		return Object{}, err
	}
	return Object{ID: fmt.Sprintf("%x:%x:%x:%x", id.Volume, id.ID, info.CreationTime.HighDateTime, info.CreationTime.LowDateTime), Directory: dir}, nil
}

func moveRooted(parent *os.File, src, dest string, expected Object) error {
	return renameRooted(parent, src, parent, dest, expected)
}

func renameRooted(parent *os.File, src string, target *os.File, dest string, expected Object) error {
	source, err := openObject(parent, src, windows.FILE_READ_ATTRIBUTES|windows.DELETE, windows.FILE_OPEN, deleteShare, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(source)
	if expected.ID != "" {
		object, err := objectIdentity(source)
		if err != nil {
			return err
		}
		if object != expected {
			return Collision("publication source has changed")
		}
	}
	utf16, err := windows.UTF16FromString(dest)
	if err != nil {
		return err
	}
	type renameInfo struct {
		ReplaceIfExists uint32
		RootDirectory   windows.Handle
		FileNameLength  uint32
		FileName        [1]uint16
	}
	start := int(unsafe.Offsetof(renameInfo{}.FileName))
	buffer := make([]byte, max(start+2*len(utf16), int(unsafe.Sizeof(renameInfo{}))))
	ren := (*renameInfo)(unsafe.Pointer(&buffer[0]))
	ren.RootDirectory = windows.Handle(target.Fd())
	ren.FileNameLength = uint32(2 * (len(utf16) - 1))
	copy(unsafe.Slice(&ren.FileName[0], len(utf16)), utf16)
	err = windows.NtSetInformationFile(source, &windows.IO_STATUS_BLOCK{}, &buffer[0], uint32(len(buffer)), windows.FileRenameInformation)
	if errors.Is(err, windows.STATUS_OBJECT_NAME_COLLISION) {
		return os.ErrExist
	}
	return err
}

func mkdirPrivate(root *os.Root, name string) error {
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	// Use a protected DACL so a shared download directory cannot grant
	// other users access to objects temporarily held for inspection.
	sddl := "D:P(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)"
	security, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dir, err := openObjectWithSecurity(parent, name, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_CREATE, deleteShare, windows.FILE_DIRECTORY_FILE, security)
	if err != nil {
		return err
	}
	return windows.CloseHandle(dir)
}

func removePrivateDirectory(parent *os.File, name string, expected Object) error {
	dir, err := openObject(parent, name, windows.FILE_READ_ATTRIBUTES|windows.DELETE,
		windows.FILE_OPEN, deleteShare, windows.FILE_DIRECTORY_FILE)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(dir)
	actual, err := objectIdentity(dir)
	if err != nil {
		return err
	}
	if actual != expected {
		return Collision("private directory was replaced")
	}
	remove := byte(1)
	return windows.NtSetInformationFile(dir, &windows.IO_STATUS_BLOCK{}, &remove, 1, windows.FileDispositionInformation)
}

// Windows does not expose an equivalent per-directory flush for the
// handle-relative rename. Recovery still covers service/process crashes.
func syncRoot(*os.File) error { return nil }

// OpenRootedStream opens an alternate stream belonging to a file whose
// writable handle is held open by the caller.
func OpenRootedStream(root *os.Root, name, stream string) (*os.File, error) {
	if !leafOK(name) || !leafOK(stream) {
		return nil, Collision("invalid stream name")
	}
	parent, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	handle, err := openObject(parent, name+":"+stream, windows.GENERIC_WRITE, windows.FILE_OVERWRITE_IF, readWriteShare, windows.FILE_NON_DIRECTORY_FILE)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(handle), name), nil
}
