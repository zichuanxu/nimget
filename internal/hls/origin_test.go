package hls

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDownloadCredentialsStayWithPlaylistOrigin(t *testing.T) {
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"Authorization", "Cookie", "X-Api-Key"} {
			if r.Header.Get(name) != "" {
				t.Errorf("%s reached a different origin on %s", name, r.URL.Path)
			}
		}
		if r.UserAgent() != "hls-fixture" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("portable request headers were dropped")
		}
		if strings.Contains(r.Referer(), "fixture-signed-referrer") {
			t.Error("HLS sent a signed referrer to a different origin")
		}
		_, _ = w.Write([]byte(r.URL.Path[1:]))
	}))
	defer public.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-token" ||
			r.Header.Get("Cookie") != "session=fixture-cookie" || r.Header.Get("X-Api-Key") != "fixture-key" {
			t.Errorf("same-origin request for %s lost credentials", r.URL.Path)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/master.m3u8":
			_, _ = fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000\nmedia.m3u8\n")
		case "/media.m3u8":
			_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXTINF:1,\nlocal\n#EXTINF:1,\n%s/direct\n#EXTINF:1,\nredirect\n#EXT-X-ENDLIST\n", public.URL)
		case "/local":
			_, _ = w.Write([]byte("local"))
		case "/redirect":
			http.Redirect(w, r, public.URL+"/redirected", http.StatusFound)
		default:
			t.Errorf("unexpected resource %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()

	opts := fastOpts()
	opts.UserAgent = "hls-fixture"
	opts.Headers = http.Header{
		"Authorization": {"Bearer fixture-token"},
		"Cookie":        {"session=fixture-cookie"},
		"X-Api-Key":     {"fixture-key"},
		"Referer":       {origin.URL + "/page?token=fixture-signed-referrer"},
	}
	dest := destPath(t)
	if err := Download(context.Background(), origin.URL+"/master.m3u8", dest, opts, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dest, []byte("localdirectredirected"))
}
