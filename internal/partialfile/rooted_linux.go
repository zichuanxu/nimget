package partialfile

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func inspectRooted(parent *os.File, name string) (Object, error) {
	return statxIdentity(int(parent.Fd()), name, unix.AT_SYMLINK_NOFOLLOW)
}

func handleIdentity(file *os.File) (Object, error) {
	return statxIdentity(int(file.Fd()), "", unix.AT_EMPTY_PATH)
}

func statxIdentity(fd int, name string, flags int) (Object, error) {
	var st unix.Statx_t
	if err := unix.Statx(fd, name, flags, unix.STATX_TYPE|unix.STATX_INO|unix.STATX_BTIME|unix.STATX_NLINK, &st); err != nil {
		return Object{}, err
	}
	kind := st.Mode & unix.S_IFMT
	if st.Mask&(unix.STATX_TYPE|unix.STATX_INO|unix.STATX_BTIME) != unix.STATX_TYPE|unix.STATX_INO|unix.STATX_BTIME ||
		kind != unix.S_IFDIR && (kind != unix.S_IFREG || st.Nlink != 1) {
		return Object{}, nil
	}
	return Object{
		ID:        fmt.Sprintf("%x:%x:%x:%x:%x", st.Dev_major, st.Dev_minor, st.Ino, st.Btime.Sec, st.Btime.Nsec),
		Directory: kind == unix.S_IFDIR,
	}, nil
}

func exclusiveRename(fd int, src string, target int, dest string) error {
	return unix.Renameat2(fd, src, target, dest, unix.RENAME_NOREPLACE)
}
