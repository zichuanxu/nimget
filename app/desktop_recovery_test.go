package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
)

func TestDesktopReauthenticateUsesTheNewURLAndHeaders(t *testing.T) {
	const payload = "download from the fresh signed URL"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sig") != "fresh" || r.Header.Get("Cookie") != "session=fresh" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(remote.Close)

	dir := t.TempDir()
	sealer, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		Address: "127.0.0.1:0", DatabasePath: filepath.Join(dir, "nimget.db"),
		TokenPath: filepath.Join(dir, "token"), DownloadRoots: []string{dir}, Sealer: sealer,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	d := &Desktop{mgr: svc.Manager()}
	wait := func(status download.Status) download.Item {
		t.Helper()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			items, err := d.List()
			if err != nil || len(items) != 1 {
				t.Fatalf("listed tasks = %+v, %v", items, err)
			}
			if items[0].Status == status {
				return items[0]
			}
			select {
			case <-tick.C:
			case <-ctx.Done():
				t.Fatalf("wanted %s, last task = %+v", status, items[0])
			}
		}
	}

	dest := filepath.Join(dir, "file.bin")
	added, err := d.Add(download.Request{URL: remote.URL + "/file?sig=old", Destination: dest})
	if err != nil {
		t.Fatal(err)
	}
	wait(download.StatusFailed)
	fresh := remote.URL + "/file?sig=fresh"
	if err := d.Reauthenticate(added.ID, "curl '"+fresh+"' -H 'Cookie: session=fresh'"); err != nil {
		t.Fatal(err)
	}
	done := wait(download.StatusCompleted)
	if done.ID != added.ID || done.URL != fresh || done.Destination != added.Destination {
		t.Fatalf("renewed task = %+v, originally %+v", done, added)
	}
	if got, err := os.ReadFile(dest); err != nil || string(got) != payload {
		t.Fatalf("downloaded file = %q, %v", got, err)
	}
}
