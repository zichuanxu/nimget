package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/update"
)

func TestDesktopChecksManuallyAndQueuesVerifiedUpdateOnce(t *testing.T) {
	const name = "NimGet-v2.0.0-windows-x64-setup.exe"
	const checksum = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const packageURL = "https://github.com/amemiya02/nimget/releases/download/v2.0.0/" + name
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v2.0.0","assets":[{"name":"` + name + `","digest":"` + checksum + `","state":"uploaded"}]}]`))
	}))
	defer server.Close()
	dir := t.TempDir()
	sealer, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		DatabasePath: filepath.Join(dir, "nimget.db"), TokenPath: filepath.Join(dir, "token"),
		DownloadRoots: []string{dir}, Runner: updateTestRunner{}, Sealer: sealer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	d := &Desktop{
		mgr: svc.Manager(),
		updates: &desktopUpdates{ctx: t.Context(), current: "v1.4.1", checker: update.Checker{
			Client: server.Client(), URL: server.URL, Path: filepath.Join(dir, "update.json"), Platform: "windows/amd64",
		}},
	}
	prefs := d.mgr.Settings()
	prefs.Desktop.CheckUpdates = false
	if _, err := d.SaveSettings(prefs); err != nil {
		t.Fatal(err)
	}
	check, err := d.CheckForUpdate()
	rel := check.Update
	if err != nil || rel == nil || rel.Version != "v2.0.0" || rel.Package == nil {
		t.Fatalf("manual check = %+v, %v", rel, err)
	}
	if d.State().Update != nil {
		t.Fatal("manual check enabled the automatic banner")
	}
	if _, err := d.DownloadUpdate("v9.9.9"); err == nil {
		t.Fatal("accepted a version that was not offered")
	}
	item, err := d.DownloadUpdate(rel.Version)
	if err != nil || item.Status != download.StatusQueued || item.URL != packageURL || item.Checksum != checksum ||
		filepath.Base(item.Destination) != name {
		t.Fatalf("queued update = %+v, %v", item, err)
	}
	again, err := d.DownloadUpdate(rel.Version)
	if err != nil || again.ID != item.ID {
		t.Fatalf("repeated request = %+v, %v; want task %s", again, err, item.ID)
	}
	if items, err := d.List(); err != nil || len(items) != 1 {
		t.Fatalf("queue = %+v, %v", items, err)
	}
}

type updateTestRunner struct{}

func (updateTestRunner) Inspect(context.Context, string, http.Header) (download.Remote, error) {
	return download.Remote{}, errors.New("probe unavailable")
}

func (updateTestRunner) Download(context.Context, download.Spec, func(download.Progress)) error {
	return errors.New("test scheduler is stopped")
}

func (updateTestRunner) Discard(download.Spec) error { return nil }
