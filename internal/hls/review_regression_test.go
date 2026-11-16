package hls

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestChangedQueriesNeverMixCachedAndNewSegments(t *testing.T) {
	s := newServer(t)
	playlist := func(version string) {
		s.set("/media.m3u8", []byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\none.ts?v="+version+"\n#EXTINF:4,\ntwo.ts?v="+version+"\n#EXT-X-ENDLIST\n"))
	}
	s.setHook(func(w http.ResponseWriter, r *http.Request, _ int) bool {
		if !strings.HasSuffix(r.URL.Path, ".ts") {
			return false
		}
		version := r.URL.Query().Get("v")
		if version == "old" && r.URL.Path == "/two.ts" {
			w.WriteHeader(http.StatusForbidden)
			return true
		}
		_, _ = w.Write([]byte(version + ":" + r.URL.Path + "\n"))
		return true
	})
	dest := destPath(t)
	opts := fastOpts()
	opts.Concurrency = 1
	playlist("old")
	if err := Download(context.Background(), s.URL+"/media.m3u8", dest, opts, nil); err == nil {
		t.Fatal("expected interrupted first run with one cached segment")
	}
	playlist("new")
	if err := Download(context.Background(), s.URL+"/media.m3u8", dest, opts, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new:/one.ts\nnew:/two.ts\n" {
		t.Fatalf("resume committed a mixture of representations: %q", got)
	}
}

func TestUnownedAssemblyPartialIsPreserved(t *testing.T) {
	s := newServer(t)
	clearStream(s, 2)
	dest := destPath(t)
	if err := os.WriteFile(dest+".part", []byte("user file"), 0o600); err != nil {
		t.Fatal(err)
	}
	runErr := Download(context.Background(), s.URL+"/media.m3u8", dest, fastOpts(), nil)
	got, err := os.ReadFile(dest + ".part")
	if runErr == nil || err != nil || string(got) != "user file" {
		t.Fatalf("unowned file modified: download error %v, sidecar contents %q, read error %v", runErr, got, err)
	}
}
