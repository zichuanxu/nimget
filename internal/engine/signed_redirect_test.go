package engine

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// signedRedirect sends /dl to a freshly signed URL on every request, as GitHub
// does for release assets requested with cookies.
func signedRedirect(t *testing.T, fixture *fileServer, path func(n int64) string) string {
	t.Helper()
	var issued atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/dl" {
			n := issued.Add(1)
			http.Redirect(w, r, fmt.Sprintf("%s?sig=%d", path(n), n), http.StatusFound)
			return
		}
		fixture.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/dl"
}

func TestRangedDownloadAcceptsResignedRedirects(t *testing.T) {
	payload := randomData(t, 4<<20)
	fixture := &fileServer{data: payload, etag: `"asset"`}
	url := signedRedirect(t, fixture, func(int64) string { return "/blob" })
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := run(t, testConfig(), url, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, payload)
}

func TestRangedDownloadRejectsRedirectToAnotherPath(t *testing.T) {
	payload := randomData(t, 4<<20)
	fixture := &fileServer{data: payload, etag: `"asset"`}
	url := signedRedirect(t, fixture, func(n int64) string { return fmt.Sprintf("/blob%d", n) })
	err := run(t, testConfig(), url, filepath.Join(t.TempDir(), "out.bin"))
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged", err)
	}
}

func TestRangedDownloadRejectsResignedRedirectWithoutETag(t *testing.T) {
	payload := randomData(t, 4<<20)
	fixture := &fileServer{data: payload, lastModified: "Tue, 17 Nov 2026 04:02:45 GMT"}
	url := signedRedirect(t, fixture, func(int64) string { return "/blob" })
	err := run(t, testConfig(), url, filepath.Join(t.TempDir(), "out.bin"))
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("err = %v, want ErrSourceChanged", err)
	}
}
