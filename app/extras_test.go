package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
)

func TestDrained(t *testing.T) {
	items := []download.Item{{Status: download.StatusCompleted}, {Status: download.StatusPaused}, {Status: download.StatusFailed}}
	if !drained(items) {
		t.Fatal("completed, paused, and failed downloads block the queue")
	}
	for _, s := range []download.Status{download.StatusQueued, download.StatusRunning} {
		if drained(append(items, download.Item{Status: s})) {
			t.Fatalf("a %s download counted as done", s)
		}
	}
}

func TestWhenDoneFiresOnce(t *testing.T) {
	d := &Desktop{}
	if err := d.SetWhenDone("reboot"); err == nil {
		t.Fatal("unknown action accepted")
	}
	if err := d.SetWhenDone("sleep"); err != nil || d.WhenDone() != "sleep" {
		t.Fatalf("SetWhenDone: %v %q", err, d.WhenDone())
	}
	_ = d.SetWhenDone("")
	if err := d.PerformWhenDone("sleep"); err == nil {
		t.Fatal("a cancelled action ran")
	}
}

func TestOfferable(t *testing.T) {
	for text, want := range map[string]bool{
		"https://example.com/a.zip":   true,
		"  http://x.test/a  ":         true,
		"curl 'https://x.test/a'":     true,
		"ftp://x.test/a":              false,
		"see https://x.test/a":        false,
		"https://x.test/a\nhttps://b": false,
	} {
		if got := offerable(text); got != want {
			t.Errorf("offerable(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestOnlyPassiveFilesOpenThemselves(t *testing.T) {
	for path, want := range map[string]bool{"/d/a.MP4": true, "/d/b.pdf": true, "/d/c.command": false, "/d/d.exe": false, "/d/e.app": false, "/d/noext": false} {
		if got := passive(path); got != want {
			t.Errorf("passive(%q) = %v", path, got)
		}
	}
}

func TestDesktopQueueImportAcceptsWebLinks(t *testing.T) {
	d, _ := desktopWithBulkTasks(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "web download")
	}))
	defer server.Close()
	const magnet = "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"
	d.legacyQueueImport.Store(&pendingQueueImport{
		id:   "fixture",
		data: fmt.Appendf(nil, `{"version":1,"downloads":[{"url":%q},{"url":%q}]}`, magnet, server.URL+"/web.txt"),
	})
	result, err := d.FinishLegacyImport("fixture", true)
	if err != nil || result.Added != 1 || len(result.Failed) != 1 || result.Failed[0] != 1 {
		t.Fatalf("import queue result = %+v, %v", result, err)
	}
	items, err := d.List()
	if err != nil || len(items) != 1 || items[0].SourceKind == download.SourceMagnet {
		t.Fatalf("queue after importing: %+v, %v", items, err)
	}
}
