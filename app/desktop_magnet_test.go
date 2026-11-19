package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/amemiya02/nimget/internal/download"
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
