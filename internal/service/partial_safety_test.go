package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/service"
)

func TestDownloadAPIReportsUnsafePartialAndAllowsRetry(t *testing.T) {
	const original = "unrelated file contents"
	const payload = "downloaded bytes"
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(remote.Close)

	dir := t.TempDir()
	root := filepath.Join(dir, "downloads")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "file.bin")
	victim := filepath.Join(dir, "unrelated.bin")
	if err := os.WriteFile(victim, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(victim, dest+".part"); err != nil {
		t.Skipf("filesystem cannot create a hard link: %v", err)
	}
	terminal := make(chan download.Item, 2)
	tokenPath := filepath.Join(dir, "token")
	svc, err := service.New(service.Config{
		Address:       "127.0.0.1:0",
		DatabasePath:  filepath.Join(dir, "nimget.db"),
		TokenPath:     tokenPath,
		DownloadRoots: []string{root},
		Sealer:        testSealer(t),
		Publish: func(event download.Event) {
			if event.Type == download.EventUpdated && (event.Item.Status == download.StatusFailed || event.Item.Status == download.StatusCompleted) {
				terminal <- event.Item
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if err := svc.Wait(); err != nil {
			t.Error(err)
		}
	})
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}

	send := func(path string, body []byte, status int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+svc.Address()+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		if resp.StatusCode != status {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("POST %s status = %d, want %d (%s)", path, resp.StatusCode, status, data)
		}
		return resp
	}
	wait := func() download.Item {
		t.Helper()
		select {
		case item := <-terminal:
			return item
		case <-ctx.Done():
			t.Fatalf("waiting for terminal download state: %v", ctx.Err())
			return download.Item{}
		}
	}
	body, err := json.Marshal(download.Request{URL: remote.URL, Destination: dest})
	if err != nil {
		t.Fatal(err)
	}
	var accepted download.Item
	if err := json.NewDecoder(send("/v1/downloads", body, http.StatusAccepted).Body).Decode(&accepted); err != nil {
		t.Fatal(err)
	}
	failed := wait()
	if failed.ID != accepted.ID || failed.Status != download.StatusFailed ||
		!strings.Contains(failed.Error, "remove or rename the partial path and retry") {
		t.Fatalf("download failure = %+v, want an actionable partial-path error", failed)
	}
	if data, err := os.ReadFile(victim); err != nil || string(data) != original {
		t.Fatalf("unrelated file = %q, %v, want %q", data, err, original)
	}
	if err := os.Remove(dest + ".part"); err != nil {
		t.Fatal(err)
	}
	send("/v1/downloads/"+accepted.ID+":retry", nil, http.StatusNoContent)
	if done := wait(); done.ID != accepted.ID || done.Status != download.StatusCompleted {
		t.Fatalf("download retry = %+v, want completed", done)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != payload {
		t.Fatalf("downloaded file = %q, %v, want %q", data, err, payload)
	}
	if runtime.GOOS == "windows" {
		if data, err := os.ReadFile(dest + ":Zone.Identifier"); err != nil || !strings.Contains(string(data), "ZoneId=3") {
			t.Fatalf("completed file quarantine = %q, %v", data, err)
		}
	}
}
