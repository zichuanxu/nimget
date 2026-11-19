package partialfile

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func inspectRooted(parent *os.File, name string) (Object, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return Object{}, err
	}
	return statIdentity(st), nil
}

func handleIdentity(file *os.File) (Object, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &st); err != nil {
		return Object{}, err
	}
	return statIdentity(st), nil
}

func statIdentity(st unix.Stat_t) Object {
	kind := st.Mode & unix.S_IFMT
	if kind != unix.S_IFDIR && (kind != unix.S_IFREG || st.Nlink != 1) {
		return Object{}
	}
	return Object{
		ID:        fmt.Sprintf("%x:%x:%x:%x", st.Dev, st.Ino, st.Btim.Sec, st.Btim.Nsec),
		Directory: kind == unix.S_IFDIR,
	}
}

func exclusiveRename(fd int, src string, target int, dest string) error {
	return unix.RenameatxNp(fd, src, target, dest, unix.RENAME_EXCL)
}
