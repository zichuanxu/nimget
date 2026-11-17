package service_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/engine"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
)

func TestSiteSettingsRoundTripAndApplyToNewDownloads(t *testing.T) {
	payload := bytes.Repeat([]byte("site settings\n"), 1<<19)
	type observation struct {
		requests chan http.Header
		release  chan struct{}
	}
	observations := map[string]observation{
		"/matched":   {make(chan http.Header, 10), make(chan struct{})},
		"/parent":    {make(chan http.Header, 10), make(chan struct{})},
		"/unrelated": {make(chan http.Header, 10), make(chan struct{})},
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "bytes=0-0" {
			observation := observations[r.URL.Path]
			select {
			case observation.requests <- r.Header.Clone():
			case <-r.Context().Done():
				return
			}
			select {
			case <-observation.release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("ETag", `"site-settings"`)
		http.ServeContent(w, r, "download.bin", time.Time{}, bytes.NewReader(payload))
	}))
	t.Cleanup(source.Close)
	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, source.Listener.Addr().String())
	}}
	t.Cleanup(transport.CloseIdleConnections)
	httpClient := &http.Client{Transport: transport}
	runner, err := engine.NewRunner(engine.Config{MaxAttempts: 1, ProbeClient: httpClient, SegmentClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	dir := t.TempDir()
	cfg := service.Config{
		Address: "127.0.0.1:0", TokenPath: filepath.Join(dir, "token"),
		DatabasePath: filepath.Join(dir, "queue.db"), DownloadRoots: []string{dir},
		Runner: runner, Sealer: testSealer(t),
	}
	svc, api := startRenewalService(t, ctx, cfg)
	prefs := settings.Default()
	prefs.Connections = 3
	prefs.Sites = []settings.Site{
		{Host: "Example.test", Connections: 1, Headers: map[string]string{"X-Site": "parent"}},
		{Host: " Files.Example.test ", Connections: 1, HostConnections: 2, Headers: map[string]string{"X-Site": "specific", "X-Empty": ""}, Username: "saved-user", Password: "saved-password"},
	}
	shown, err := api.PutSettings(ctx, prefs)
	if err != nil || len(shown.Sites) != 2 || shown.Sites[1].Password != settings.Masked {
		t.Fatalf("initial save returned incorrect site settings or failed: %v", err)
	}
	shown.Sites[1].Connections = 5
	if _, err := api.PutSettings(ctx, shown); err != nil {
		t.Fatal(err)
	}
	want := []settings.Site{prefs.Sites[0], shown.Sites[1]}
	shown.Sites[1].Connections = 33
	if _, err := api.PutSettings(ctx, shown); err == nil || !strings.Contains(err.Error(), "INVALID_SETTINGS") {
		t.Fatalf("invalid site settings save = %v", err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	_, api = startRenewalService(t, ctx, cfg)
	loaded, err := api.Settings(ctx)
	if err != nil || !reflect.DeepEqual(loaded.Sites, want) {
		t.Fatalf("site settings did not round-trip after restart: %v", err)
	}

	for _, tt := range []struct {
		host, path, header, auth string
		conns                    int
	}{
		{"cdn.files.example.test", "/matched", "specific", "Basic c2F2ZWQtdXNlcjpzYXZlZC1wYXNzd29yZA==", 2},
		{"cdn.example.test", "/parent", "parent", "", 1},
		{"unrelated.test", "/unrelated", "", "", 3},
	} {
		t.Run(tt.host, func(t *testing.T) {
			observation := observations[tt.path]
			item, err := api.Add(ctx, download.Request{
				URL: "http://" + tt.host + tt.path, Destination: filepath.Join(dir, tt.host+".bin"),
			})
			if err != nil {
				t.Fatal(err)
			}
			for range tt.conns {
				select {
				case headers := <-observation.requests:
					if headers.Get("X-Site") != tt.header || headers.Get("Authorization") != tt.auth {
						t.Fatal("request was sent with incorrect site headers or credentials")
					}
				case <-ctx.Done():
					t.Fatal("download did not use the expected number of connections")
				}
			}
			select {
			case <-observation.requests:
				t.Fatal("download exceeded the expected number of connections")
			case <-time.After(100 * time.Millisecond):
			}
			close(observation.release)
			waitTaskStatus(t, ctx, api, item.ID, download.StatusCompleted)
			data, err := os.ReadFile(item.Destination)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatalf("downloaded file has incorrect contents: %v", err)
			}
		})
	}
}
