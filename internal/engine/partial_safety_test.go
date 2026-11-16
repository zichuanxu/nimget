package engine

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSingleDownloadRejectsLinkedPartial(t *testing.T) {
	want := []byte("new download")
	server := (&fileServer{data: want, noRanges: true}).start(t)
	for name, link := range map[string]func(string, string) error{
		"hard link":     os.Link,
		"symbolic link": os.Symlink,
	} {
		t.Run(name, func(t *testing.T) {
			dest, victim, original := linkedPartial(t, link)
			err := run(t, testConfig(), server.URL, dest)
			verifyRejectedPartial(t, dest, victim, original, err)
			if err := os.Remove(dest + ".part"); err != nil {
				t.Fatal(err)
			}
			if err := run(t, testConfig(), server.URL, dest); err != nil {
				t.Fatal(err)
			}
			requireFile(t, dest, want)
		})
	}
}

func TestHLSAssemblyRejectsLinkedPartial(t *testing.T) {
	fixture, server := newHLSServer(t, 2)
	for name, link := range map[string]func(string, string) error{
		"hard link":     os.Link,
		"symbolic link": os.Symlink,
	} {
		t.Run(name, func(t *testing.T) {
			dest, victim, original := linkedPartial(t, link)
			url := server.URL + "/v/index.m3u8"
			err := run(t, testConfig(), url, dest)
			verifyRejectedPartial(t, dest, victim, original, err)
			if err := os.Remove(dest + ".part"); err != nil {
				t.Fatal(err)
			}
			if err := run(t, testConfig(), url, dest); err != nil {
				t.Fatal(err)
			}
			requireFile(t, dest, bytes.Join(fixture.segments, nil))
		})
	}
}

func TestRangedResumeRejectsLinkedPartial(t *testing.T) {
	const original = "unrelated file contents"
	want := bytes.Repeat([]byte{'x'}, len(original))
	var fail atomic.Bool
	fail.Store(true)
	fixture := &fileServer{data: want, etag: "\"version1\"", fault: func(w http.ResponseWriter, r *http.Request, start, end, _ int64) bool {
		if end > start && fail.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return true
		}
		return false
	}}
	server := fixture.start(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "download.bin")
	cfg := testConfig()
	cfg.MaxAttempts = 1
	if err := run(t, cfg, server.URL, dest); err == nil {
		t.Fatal("download unexpectedly completed during the simulated outage")
	}
	part := dest + ".part"
	saved := filepath.Join(dir, "saved.bin")
	if err := os.Rename(part, saved); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(dir, "unrelated.bin")
	if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(victim, part); err != nil {
		t.Skipf("filesystem cannot create hard links: %v", err)
	}
	fail.Store(false)
	err := run(t, cfg, server.URL, dest)
	verifyRejectedPartial(t, dest, victim, []byte(original), err)

	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, part); err != nil {
		t.Fatal(err)
	}
	if err := run(t, cfg, server.URL, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, want)
}

func TestHLSResumeRejectsLinkedWorkDirectory(t *testing.T) {
	fixture, server := newHLSServer(t, 2)
	fixture.failFrom.Store(1)
	url := server.URL + "/v/index.m3u8"
	dest := filepath.Join(t.TempDir(), "download.ts")
	cfg := testConfig()
	cfg.Connections = 1
	cfg.MaxAttempts = 1
	if err := run(t, cfg, url, dest); err == nil {
		t.Fatal("download unexpectedly completed with a missing segment")
	}

	work := dest + ".hls"
	victim := filepath.Join(t.TempDir(), "unrelated")
	if err := os.Rename(work, victim); err != nil {
		t.Fatal(err)
	}
	linkDirectory(t, victim, work)
	before, err := os.ReadDir(victim)
	if err != nil {
		t.Fatal(err)
	}

	fixture.failFrom.Store(-1)
	downloadErr := run(t, cfg, url, dest)
	after, err := os.ReadDir(victim)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len(after) {
		t.Errorf("download modified the unrelated work directory: %v became %v", before, after)
	}
	if downloadErr == nil || !strings.Contains(downloadErr.Error(), "unsafe partial") {
		t.Fatalf("download error = %v, want unsafe partial path error", downloadErr)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("download left a destination after rejection: %v", err)
	}

	if err := os.Remove(work); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(victim, work); err != nil {
		t.Fatal(err)
	}
	if err := run(t, cfg, url, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, bytes.Join(fixture.segments, nil))
}

func linkedPartial(t *testing.T, link func(string, string) error) (dest, victim string, original []byte) {
	t.Helper()
	dir := t.TempDir()
	dest = filepath.Join(dir, "download.bin")
	victim = filepath.Join(dir, "unrelated.bin")
	original = []byte("unrelated file contents")
	if err := os.WriteFile(victim, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := link(victim, dest+".part"); err != nil {
		t.Skipf("filesystem cannot create link for this fixture: %v", err)
	}
	return dest, victim, original
}

func verifyRejectedPartial(t *testing.T, dest, victim string, original []byte, err error) {
	t.Helper()
	requireFile(t, victim, original)
	if err == nil || !strings.Contains(err.Error(), "unsafe partial") || !strings.Contains(err.Error(), "remove or rename") {
		t.Fatalf("download error = %v, want actionable unsafe partial error", err)
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		t.Fatalf("download left a destination after rejection: %v", err)
	}
	if _, err := os.Lstat(dest + ".part"); err != nil {
		t.Fatalf("download removed the colliding partial: %v", err)
	}
}
