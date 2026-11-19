package quarantine

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// Mark sets com.apple.quarantine: flags;timestamp;agent;event.
func Mark(path, sourceURL string) error {
	return unix.Setxattr(path, "com.apple.quarantine", markValue(), 0)
}

// MarkRooted marks the exact opened file.
func MarkRooted(_ *os.Root, f *os.File, _, _ string) error {
	return unix.Fsetxattr(int(f.Fd()), "com.apple.quarantine", markValue(), 0)
}

func markValue() []byte {
	return fmt.Appendf(nil, "0081;%x;nimget;", time.Now().Unix())
}
