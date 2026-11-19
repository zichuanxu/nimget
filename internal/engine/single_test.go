package engine

import (
	"net/http"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSingleKeepsStreamingProbeResponse(t *testing.T) {
	data := randomData(t, 96<<10)
	srv := &fileServer{data: data, noRanges: true, piece: 8 << 10, pause: 60 * time.Millisecond}
	server := srv.start(t)
	cfg := testConfig()
	cfg.StallTimeout = 300 * time.Millisecond
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := run(t, cfg, server.URL, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
	if got := srv.requests.Load(); got != 1 {
		t.Fatalf("sent %d requests for a complete probe response; want 1", got)
	}
}

func TestSingleRetriesWhenProbeBodyStalls(t *testing.T) {
	data := randomData(t, 32<<10)
	srv := &fileServer{data: data, noRanges: true, etag: `"stable"`}
	srv.fault = func(w http.ResponseWriter, r *http.Request, _, _, request int64) bool {
		if request != 1 {
			if got := r.Header.Get("If-Match"); got != srv.etag {
				t.Errorf("retry If-Match = %q; want %s", got, srv.etag)
			}
			return false
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:1024])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		return true
	}
	server := srv.start(t)
	cfg := testConfig()
	cfg.StallTimeout = 200 * time.Millisecond
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := run(t, cfg, server.URL, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("sent %d requests after one body stalled; want 2", got)
	}
}

func TestSingleDoesNotReusePartialBodySentAs200(t *testing.T) {
	data := []byte("complete body")
	srv := &fileServer{data: data, noRanges: true}
	srv.fault = func(w http.ResponseWriter, r *http.Request, _, _, _ int64) bool {
		if r.Header.Get("Range") == "" {
			return false
		}
		w.Header().Set("Content-Range", "bytes 0-0/"+strconv.Itoa(len(data)))
		w.Header().Set("Content-Length", "1")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:1])
		return true
	}
	server := srv.start(t)
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := run(t, testConfig(), server.URL, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
}

func TestSingleFallsBackWhenProbeHasNoBody(t *testing.T) {
	data := []byte("complete body")
	srv := &fileServer{data: data, noRanges: true}
	srv.fault = func(w http.ResponseWriter, r *http.Request, _, _, _ int64) bool {
		if r.Header.Get("Range") == "" {
			return false
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		return true
	}
	server := srv.start(t)
	cfg := testConfig()
	cfg.MaxAttempts = 1
	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := run(t, cfg, server.URL, dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
	if got := srv.requests.Load(); got != 2 {
		t.Fatalf("sent %d requests after the server omitted the probe body; want 2", got)
	}
}
