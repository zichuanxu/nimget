package engine

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnownedOrdinaryPartialIsPreserved(t *testing.T) {
	for _, ranged := range []bool{false, true} {
		name := "single"
		if ranged {
			name = "ranged"
		}
		t.Run(name, func(t *testing.T) {
			server := (&fileServer{data: []byte("download"), etag: `"v1"`, noRanges: !ranged}).start(t)
			dest := filepath.Join(t.TempDir(), "out.bin")
			if err := os.WriteFile(dest+".part", []byte("user file"), 0o600); err != nil {
				t.Fatal(err)
			}
			runErr := run(t, testConfig(), server.URL, dest)
			got, err := os.ReadFile(dest + ".part")
			if runErr == nil || err != nil || string(got) != "user file" {
				t.Fatalf("unowned file modified: download error %v, sidecar contents %q, read error %v", runErr, got, err)
			}
		})
	}
}

func TestHTTPErrorOmitsPathCredential(t *testing.T) {
	const marker = "synthetic-path-credential"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Expired: "+r.URL.Path, http.StatusForbidden)
	}))
	defer server.Close()
	err := run(t, testConfig(), server.URL+"/"+marker+"/download.bin", filepath.Join(t.TempDir(), "out.bin"))
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("wanted HTTP failure with no credential, got %v", err)
	}
}
