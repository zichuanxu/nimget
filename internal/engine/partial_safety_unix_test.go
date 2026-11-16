//go:build unix

package engine

import (
	"os"
	"testing"
)

func linkDirectory(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("filesystem cannot create directory symlink: %v", err)
	}
}
