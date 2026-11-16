package engine

import (
	"os/exec"
	"testing"
)

func linkDirectory(t *testing.T, target, path string) {
	t.Helper()
	out, err := exec.Command("cmd.exe", "/d", "/c", "mklink", "/J", path, target).CombinedOutput()
	if err != nil {
		t.Skipf("filesystem cannot create Windows junction: %v (%s)", err, out)
	}
}
