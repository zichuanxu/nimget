package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/service"
	"golang.org/x/time/rate"
)

func TestMagnetResumesVerifiedPiecesAfterPauseAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	list := func() ([]download.Item, error) { return c.List(ctx) }
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	prefs.MaxConcurrent = 1
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	const pieceLength = 32 << 10
	payload := make([]byte, 32*pieceLength)
	for i := range payload {
		payload[i] = byte(i/257 + i/pieceLength)
	}
	limit := rate.NewLimiter(128<<10, 16<<10)
	link, seeder, trackerOnline := trackedMagnetPeer(t, "resume.bin", payload, false, limit)
	queued, err := c.Add(ctx, download.Request{URL: link})
	if err != nil {
		t.Fatal(err)
	}
	waitForMagnetState(t, ctx, list, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Completed >= 4*pieceLength && item.Completed < int64(len(payload))
	})
	if err := c.Pause(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	matches, err := filepath.Glob(filepath.Join(cfg.DownloadRoots[0], ".nimget-magnet-*.part"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("pause and shutdown should retain one staged payload: %v, %v", matches, err)
	}

	// Change one piece that had reached disk. The restarted service must
	// validate the saved data and request the damaged bytes again.
	staged, err := os.OpenFile(matches[0], os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	stagedBytes, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	damagedOffset := -1
	for i := 0; i+pieceLength <= len(stagedBytes); i += pieceLength {
		if bytes.Equal(stagedBytes[i:i+pieceLength], payload[i:i+pieceLength]) {
			damagedOffset = i
			break
		}
	}
	if damagedOffset < 0 {
		t.Fatal("pause did not keep any verified fixture pieces")
	}
	if _, err := staged.WriteAt([]byte{payload[damagedOffset] ^ 0xff}, int64(damagedOffset)); err != nil {
		t.Fatal(err)
	}
	if err := staged.Close(); err != nil {
		t.Fatal(err)
	}

	stats := seeder.Stats()
	before := stats.BytesWrittenData.Int64()
	limit.SetLimit(rate.Inf)
	trackerOnline.Store(false)
	restarted := startHandoffService(t, cfg)
	c = magnetClient(t, restarted, cfg.TokenPath)
	paused := waitForMagnetState(t, ctx, list, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusPaused
	})
	if paused.Completed < 4*pieceLength || paused.Total != int64(len(payload)) {
		t.Fatalf("verify progress after pause and restart: %+v", paused)
	}
	if err := c.Resume(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	waitForMagnetState(t, ctx, list, queued.ID, func(item download.Item) bool {
		return item.Status == download.StatusRunning && item.Phase == download.PhaseWaitingPeers &&
			item.Total == int64(len(payload)) && item.Completed >= 3*pieceLength
	})
	stats = seeder.Stats()
	if stats.BytesWrittenData.Int64() != before {
		t.Fatal("rechecking the staged payload required a peer")
	}
	if err := c.Pause(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	trackerOnline.Store(true)
	if err := c.Resume(ctx, queued.ID); err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, list, queued.ID)
	got, err := os.ReadFile(done.Destination)
	if done.Status != download.StatusCompleted || err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("resumed output mismatch: %+v, %d bytes, %v", done, len(got), err)
	}
	stats = seeder.Stats()
	if received := stats.BytesWrittenData.Int64() - before; received >= int64(len(payload))-pieceLength {
		t.Fatalf("download did not reuse good pieces: peer sent %d more bytes", received)
	}
}

const magnetCrashEnv = "NIMGET_TEST_MAGNET_CRASH"
const magnetCrashCode = 97

type magnetCrashFixture struct {
	DB, Token, Root, URL string
	Step                 download.MagnetPublicationStep
}

func TestMagnetPublicationCrashHelper(t *testing.T) {
	params := os.Getenv(magnetCrashEnv)
	if params == "" {
		t.Skip("invoked by the publication recovery fixture")
	}
	var fixture magnetCrashFixture
	if err := json.Unmarshal([]byte(params), &fixture); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	cfg := service.Config{
		Address: "127.0.0.1:0", DatabasePath: fixture.DB, TokenPath: fixture.Token,
		DownloadRoots: []string{fixture.Root}, Runner: idleRunner{}, Sealer: testSealer(t), MagnetLoopbackOnly: true,
		MagnetPublicationCheckpoint: func(step download.MagnetPublicationStep, item download.Item) {
			if step == fixture.Step {
				fmt.Printf("publication=%s\n", item.ID)
				os.Exit(magnetCrashCode)
			}
		},
	}
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	added, err := c.Add(ctx, download.Request{URL: fixture.URL})
	if err != nil {
		t.Fatal(err)
	}
	done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, added.ID)
	t.Fatalf("attempt reached %s without reaching crash boundary %s", done.Status, fixture.Step)
}

func TestMagnetPublicationRecovery(t *testing.T) {
	for _, name := range []string{"before move with occupied name", "after move", "ambiguous destination", "forget ambiguous publication"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			cfg := handoffServiceConfig(t)
			payload := bytes.Repeat([]byte("verified crash fixture bytes "), 4000)
			link, seeder, _ := trackedMagnetPeer(t, "publication.bin", payload, false, nil)
			step := download.MagnetPublicationMoved
			if name == "before move with occupied name" {
				step = download.MagnetPublicationRecorded
			}
			params, err := json.Marshal(magnetCrashFixture{cfg.DatabasePath, cfg.TokenPath, cfg.DownloadRoots[0], link, step})
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMagnetPublicationCrashHelper$")
			cmd.Env = append(os.Environ(), magnetCrashEnv+"="+string(params))
			output, err := cmd.CombinedOutput()
			var exited *exec.ExitError
			if !errors.As(err, &exited) || exited.ExitCode() != magnetCrashCode {
				t.Fatalf("helper did not exit at %s: %v\n%s", step, err, output)
			}
			var id string
			for _, line := range strings.Split(string(output), "\n") {
				if value, ok := strings.CutPrefix(strings.TrimSpace(line), "publication="); ok {
					id = value
				}
			}
			if id == "" {
				t.Fatalf("missing crash fixture task ID: %s", output)
			}
			seeder.Drop()
			wanted := filepath.Join(cfg.DownloadRoots[0], "publication.bin")
			original := wanted
			saved := filepath.Join(t.TempDir(), "owned.bin")
			if name == "ambiguous destination" || name == "forget ambiguous publication" {
				if err := os.Rename(wanted, saved); err != nil {
					t.Fatal(err)
				}
			}
			foreign := []byte("independent bytes")
			if name != "after move" {
				if err := os.WriteFile(wanted, foreign, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "before move with occupied name" {
				wanted = filepath.Join(cfg.DownloadRoots[0], "publication (1).bin")
			}

			restarted := startHandoffService(t, cfg)
			c := magnetClient(t, restarted, cfg.TokenPath)
			done := waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, id)
			if name == "ambiguous destination" || name == "forget ambiguous publication" {
				if done.Status != download.StatusFailed || done.Completed != int64(len(payload)) ||
					!strings.Contains(strings.ToLower(done.Error), "retry") {
					t.Fatalf("ambiguous publication should retain verified progress with a retry action: %+v", done)
				}
				got, err := os.ReadFile(original)
				if err != nil || !bytes.Equal(got, foreign) {
					t.Fatalf("ambiguous publication changed the foreign file: %q, %v", got, err)
				}
				if err := c.Delete(ctx, id, true); err == nil {
					t.Fatal("deleting a pending publication discarded its recovery record")
				}
				if name == "forget ambiguous publication" {
					if err := c.Delete(ctx, id, false); err != nil {
						t.Fatalf("delete only the record: %v", err)
					}
					if items, err := c.List(ctx); err != nil || len(items) != 0 {
						t.Fatalf("list after deleting only the record: %+v, %v", items, err)
					}
					for path, want := range map[string][]byte{original: foreign, saved: payload} {
						if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
							t.Fatalf("delete kept %q with %d bytes; want %d, %v", path, len(got), len(want), err)
						}
					}
					return
				}
				if err := os.Rename(original, filepath.Join(t.TempDir(), "foreign.bin")); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(saved, original); err != nil {
					t.Fatal(err)
				}
				if err := c.Retry(ctx, id); err != nil {
					t.Fatal(err)
				}
				done = waitForMagnetTask(t, ctx, func() ([]download.Item, error) { return c.List(ctx) }, id)
			}
			got, err := os.ReadFile(wanted)
			if done.Status != download.StatusCompleted || done.ID != id ||
				done.Completed != int64(len(payload)) || err != nil || !bytes.Equal(got, payload) {
				t.Fatalf("publication did not recover: %+v, %d bytes, %v", done, len(got), err)
			}
			assertMagnetOutputPath(t, wanted, done.Destination)
			if name == "before move with occupied name" {
				got, err = os.ReadFile(original)
				if err != nil || !bytes.Equal(got, foreign) {
					t.Fatalf("publication changed the occupied file: %q, %v", got, err)
				}
			}
			files, err := os.ReadDir(cfg.DownloadRoots[0])
			expectedFiles := 1
			if name == "before move with occupied name" {
				expectedFiles++
			}
			if err != nil || len(files) != expectedFiles {
				t.Fatalf("publication produced unexpected files: %v, %v", files, err)
			}
			switch name {
			case "before move with occupied name":
				if err := c.Delete(ctx, id, true); err != nil {
					t.Fatal(err)
				}
				if _, err := os.Stat(wanted); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("deleting the download should remove its owned file: %v", err)
				}
				if got, err := os.ReadFile(original); err != nil || !bytes.Equal(got, foreign) {
					t.Fatalf("deleting the download changed the foreign file: %q, %v", got, err)
				}
			case "after move":
				if err := c.Delete(ctx, id, false); err != nil {
					t.Fatal(err)
				}
				if got, err := os.ReadFile(wanted); err != nil || !bytes.Equal(got, payload) {
					t.Fatalf("Delete with keep-files should preserve the output: %d bytes, %v", len(got), err)
				}
			case "ambiguous destination":
				edited := append([]byte(nil), payload...)
				edited[0] ^= 0xff
				if err := os.WriteFile(wanted, edited, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := c.Delete(ctx, id, true); err == nil {
					t.Fatal("the service did not report keeping the edited output")
				}
				if got, err := os.ReadFile(wanted); err != nil || !bytes.Equal(got, edited) {
					t.Fatalf("deleting the task should preserve the edited output: %d bytes, %v", len(got), err)
				}
			}
		})
	}
}

func TestPauseDuringMagnetMetadataReleasesTheQueueSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	announcing, disconnected := make(chan struct{}, 1), make(chan struct{}, 1)
	tracker := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		announcing <- struct{}{}
		<-r.Context().Done()
		disconnected <- struct{}{}
	}))
	t.Cleanup(func() {
		tracker.CloseClientConnections()
		tracker.Close()
	})
	cfg := handoffServiceConfig(t)
	c := magnetClient(t, startHandoffService(t, cfg), cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeNone}
	prefs.MaxConcurrent = 1
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	stalled, err := c.Add(ctx, download.Request{
		URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=" + url.QueryEscape(tracker.URL),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-announcing:
	case <-ctx.Done():
		t.Fatal("the first task did not start retrieving metadata")
	}
	next, err := c.Add(ctx, download.Request{
		URL: trackedMagnetFixture(t, "next.bin", []byte("queued payload"), false),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Pause(ctx, stalled.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("pausing did not cancel metadata discovery")
	}
	list := func() ([]download.Item, error) { return c.List(ctx) }
	done := waitForMagnetTask(t, ctx, list, next.ID)
	if done.Status != download.StatusCompleted {
		t.Fatalf("pausing the metadata download should let the next task complete: %+v", done)
	}
	paused := waitForMagnetState(t, ctx, list, stalled.ID, func(item download.Item) bool {
		return item.Status == download.StatusPaused
	})
	if paused.Total != -1 || paused.Completed != 0 {
		t.Fatalf("metadata pause unexpectedly reported verified payload: %+v", paused)
	}
}
