package service_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/engine"
	"github.com/amemiya02/nimget/internal/logging"
	"github.com/amemiya02/nimget/internal/queuefile"
	"github.com/amemiya02/nimget/internal/service"
)

func TestProtectedDownloadsKeepCredentialsOutOfDiagnosticsAndExports(t *testing.T) {
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/public" {
			http.Error(w, "See https://example.invalid/fixture-body-path-secret?token=fixture-body-query-secret", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/bad-redirect" {
			w.Header().Set("Location", "http://[invalid/?ticket=fixture-location-secret")
			w.WriteHeader(http.StatusFound)
			return
		}
		http.Error(w, fmt.Sprintf("%s %s", r.Header.Get("Cookie"), r.Header.Get("Authorization")), http.StatusForbidden)
	}))
	defer source.Close()

	root := t.TempDir()
	tokenPath := filepath.Join(root, "token")
	var logs bytes.Buffer
	runner, err := engine.NewRunner(engine.Config{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := service.New(service.Config{
		Address: "127.0.0.1:0", DatabasePath: filepath.Join(root, "queue.db"),
		TokenPath: tokenPath, DownloadRoots: []string{root}, Sealer: testSealer(t),
		Runner: runner, Logger: logging.New(&logs, slog.LevelDebug),
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
		_ = svc.Wait()
	})
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	api := client.New("http://"+svc.Address(), token, nil)

	for _, path := range []string{"/rejected.bin", "/rejected.m3u8", "/bad-redirect"} {
		raw := strings.Replace(source.URL, "://", "://fixture-user:fixture-password@", 1) + path +
			"?ticket=fixture-query-secret#fixture-fragment"
		if _, err := api.Add(ctx, download.Request{
			URL: raw,
			Headers: map[string]string{
				"Cookie": "session=fixture-cookie-secret", "Authorization": "Bearer fixture-header-secret",
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := api.Add(ctx, download.Request{URL: source.URL + "/public"}); err != nil {
		t.Fatal(err)
	}

	var items []download.Item
	for {
		list, err := api.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		items = items[:0]
		for _, item := range list {
			if item.Status == download.StatusFailed {
				items = append(items, item)
			}
		}
		if len(items) == 4 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	if err := svc.Wait(); err != nil {
		t.Fatal(err)
	}
	export, err := queuefile.ExportSummary(items)
	if err != nil {
		t.Fatal(err)
	}
	diagnostics := map[string]string{"service log": logs.String(), "queue export": string(export)}
	for _, item := range items {
		if !strings.Contains(item.Error, "403") && !strings.Contains(item.Error, "redirect") {
			t.Errorf("failure for %s lacked an HTTP status or redirect diagnostic", item.Destination)
		}
		if strings.Contains(item.Error, "HTTP 403") && item.Failure != download.FailureAuthorization {
			t.Errorf("failure for %s = %q, want authorization", item.Destination, item.Failure)
		}
		diagnostics["task error "+item.ID] = item.Error
	}
	for label, diagnostic := range diagnostics {
		for _, secret := range []string{
			"fixture-user", "fixture-password", "fixture-query-secret", "fixture-fragment",
			"fixture-location-secret", "fixture-cookie-secret", "fixture-header-secret", "fixture-body-path-secret", "fixture-body-query-secret",
		} {
			if strings.Contains(diagnostic, secret) {
				t.Errorf("%s exposed %s", label, secret)
			}
		}
	}
}
