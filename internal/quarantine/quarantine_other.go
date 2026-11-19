//go:build !darwin && !windows

package quarantine

import "os"

// Mark does nothing: other systems have no download quarantine.
func Mark(path, sourceURL string) error { return nil }

// MarkRooted does nothing on systems without download quarantine.
func MarkRooted(*os.Root, *os.File, string, string) error { return nil }
