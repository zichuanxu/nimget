//go:build unix || windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFailedSampleRemovesPartialOutput(t *testing.T) {
	root := t.TempDir()
	_, err := measure("test", source{}, root, func(out string) *exec.Cmd {
		for _, suffix := range []string{"", ".part", ".aria2"} {
			if err := os.WriteFile(out+suffix, []byte("partial data"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return exec.Command(filepath.Join(root, "missing-downloader"))
	})
	if err == nil {
		t.Fatal("failed client reported success")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("benchmark left %v in its output directory", entries)
	}
}
