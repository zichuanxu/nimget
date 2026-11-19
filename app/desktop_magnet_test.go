package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/testfixture"
)

func TestDesktopAddsV2AndHybridMagnets(t *testing.T) {
	d, root := desktopWithBulkTasks(t, nil)
	dir := filepath.Join(root, "torrents")
	const tracker = "&tr=http%3A%2F%2F127.0.0.1%3A1%2Fannounce"
	const v1 = "urn:btih:0123456789abcdef0123456789abcdef01234567"
	v2 := "urn:btmh:1220" + strings.Repeat("12", 32)
	hybridV2 := "urn:btmh:1220" + strings.Repeat("34", 32)
	for _, topic := range []string{v2, v1 + "&xt=" + hybridV2} {
		link := "magnet:?xt=" + topic + tracker
		first, err := d.Add(download.Request{URL: link, Directory: dir})
		if err != nil || first.ID == "" || first.Directory != dir || first.SourceKind != download.SourceMagnet {
			t.Fatalf("desktop add: %+v, %v", first, err)
		}
		second, err := d.Add(download.Request{URL: link + "&dn=another"})
		if err != nil || second.ID != first.ID || second.Directory != dir {
			t.Fatalf("desktop repeat add: %+v, %v", second, err)
		}
	}
	if _, err := d.Add(download.Request{URL: "magnet:?xt=urn:btmh:1220abcd" + tracker}); err == nil {
		t.Fatal("desktop accepted a truncated BitTorrent v2 hash")
	}
	if items, err := d.List(); err != nil || len(items) != 2 {
		t.Fatalf("desktop queue: %+v, %v", items, err)
	}
}

func TestDesktopAddCompletesMagnetDownload(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	root := t.TempDir()
	saveTo := filepath.Join(root, "chosen")
	payload := bytes.Repeat([]byte("controlled desktop download\n"), 3000)
	link := testfixture.TrackedV1Magnet(t, "desktop.txt", payload)
	sealer, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "nimget.db"),
		TokenPath: filepath.Join(root, "token"), DownloadRoots: []string{root},
		Sealer: sealer, MagnetLoopbackOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := svc.Close(); err != nil {
			t.Error(err)
		}
	})
	desktop := &Desktop{mgr: svc.Manager()}
	prefs, err := desktop.Settings()
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := desktop.SaveSettings(prefs); err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	task, err := desktop.Add(download.Request{URL: link, Directory: saveTo})
	if err != nil || task.ID == "" || task.SourceKind != download.SourceMagnet || task.Status != download.StatusQueued {
		t.Fatalf("New Download = %+v, %v", task, err)
	}

	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		queue, err := desktop.QueueSnapshot()
		if err != nil || len(queue.Items) != 1 || queue.Items[0].ID != task.ID {
			t.Fatalf("desktop queue = %+v, %v", queue, err)
		}
		item := queue.Items[0]
		if item.Status == download.StatusFailed {
			t.Fatalf("desktop magnet failed: %+v", item)
		}
		if item.Status == download.StatusCompleted {
			if item.Completed != int64(len(payload)) || item.Total != item.Completed {
				t.Fatalf("unexpected verified progress: %+v", item)
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("desktop magnet did not finish: %v, %+v", ctx.Err(), item)
		case <-tick.C:
		}
	}

	data, err := os.ReadFile(filepath.Join(saveTo, "desktop.txt"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("desktop save directory contains %d bytes, want %d: %v", len(data), len(payload), err)
	}
}
