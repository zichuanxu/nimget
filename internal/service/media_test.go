package service_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/engine"
	"github.com/amemiya02/nimget/internal/logging"
	"github.com/amemiya02/nimget/internal/service"
)

func TestUnsupportedMediaTasksReportWhyTheyFailed(t *testing.T) {
	for _, tt := range []struct {
		name, path, contentType, playlist, want string
	}{
		{"external subtitles", "/manifest.m3u8", "application/vnd.apple.mpegurl", "#EXTM3U\n#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"s\",NAME=\"English\",URI=\"subs.m3u8?token=subtitle-secret\"\n#EXT-X-STREAM-INF:BANDWIDTH=2000,SUBTITLES=\"s\"\nmedia.m3u8\n", "separate subtitles"},
		{"live HLS", "/manifest.m3u8", "application/vnd.apple.mpegurl", "#EXTM3U\n#EXTINF:4,\nvideo.ts\n", "live HLS"},
		{"DASH by media type", "/manifest", "application/dash+xml; charset=utf-8", "<MPD></MPD>", "DASH"},
		{"DASH by extension", "/manifest.mpd", "application/octet-stream", "<MPD></MPD>", "DASH"},
		{"unsupported encryption", "/manifest.m3u8", "application/vnd.apple.mpegurl", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,KEYFORMAT=\"https://fixture.test/k?token=keyformat-secret\",URI=\"key.bin?token=key-secret\"\n#EXTINF:4,\nvideo.ts\n#EXT-X-ENDLIST\n", "encryption"},
		{"key request rejected", "/manifest.m3u8", "application/vnd.apple.mpegurl", "#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key-path-secret.bin?token=key-secret\"\n#EXTINF:4,\nvideo.ts\n#EXT-X-ENDLIST\n", "HTTP 403"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			signedPath := strings.Replace(tt.path, "/manifest", "/manifest-path-secret", 1)
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case signedPath:
					w.Header().Set("Content-Type", tt.contentType)
					_, _ = io.WriteString(w, tt.playlist)
				case "/media.m3u8":
					_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:4,\nvideo.ts\n#EXT-X-ENDLIST\n")
				case "/video.ts":
					_, _ = io.WriteString(w, "video bytes")
				default:
					http.Error(w, r.Header.Get("Authorization"), http.StatusForbidden)
				}
			}))
			t.Cleanup(source.Close)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			dir := t.TempDir()
			var logs bytes.Buffer
			runner, err := engine.NewRunner(engine.Config{MaxAttempts: 1})
			if err != nil {
				t.Fatal(err)
			}
			svc, api := startRenewalService(t, ctx, service.Config{
				Address: "127.0.0.1:0", TokenPath: filepath.Join(dir, "token"),
				DatabasePath: filepath.Join(dir, "queue.db"), DownloadRoots: []string{dir},
				Runner: runner, Sealer: testSealer(t), Logger: logging.New(&logs, slog.LevelDebug),
			})
			dest := filepath.Join(dir, "video.ts")
			_, err = api.Add(ctx, download.Request{
				URL: source.URL + signedPath + "?sig=playlist-secret", Destination: dest,
				Headers: map[string]string{"Authorization": "Bearer credential-secret"},
			})
			if err != nil {
				t.Fatal(err)
			}

			failed := waitRenewalTask(t, ctx, api, download.StatusFailed)
			wantFailure := download.FailureUnsupportedMedia
			if tt.name == "key request rejected" {
				wantFailure = download.FailureAuthorization
			}
			if wantFailure == download.FailureUnsupportedMedia && !strings.Contains(failed.Error, "direct media file") {
				t.Errorf("failure lacks source guidance: %q", failed.Error)
			}
			if failed.Failure != wantFailure || !strings.Contains(failed.Error, tt.want) {
				t.Fatalf("failure = %s, %q, want %s and %q", failed.Failure, failed.Error, wantFailure, tt.want)
			}
			if err := svc.Close(); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(logs.String(), "-secret") {
				t.Fatalf("service log includes a secret")
			}
			if strings.Contains(failed.Error, "-secret") {
				t.Fatalf("failure diagnostic includes a secret: %q", failed.Error)
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed task left output: %v", err)
			}
		})
	}
}
