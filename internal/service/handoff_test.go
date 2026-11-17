package service_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/engine"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
)

const (
	handoffPath = "/v1/extension/handoffs"
	handoffBody = `{"handoffId":"97eba4cf-7936-4d48-96fd-8e1110a9e2d8","url":"https://example.com/public.zip"}`
)

func TestAutomaticHandoffRequiresMatchingReplayBeforeReceipt(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/private.zip" {
			http.Error(w, "NimGet lacks the browser's authentication", http.StatusForbidden)
			return
		}
		if r.URL.Path == "/login.zip" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<p>Sign in</p>"))
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", "4")
		w.Header().Set("Content-Disposition", "attachment; filename=public.zip")
		switch r.URL.Path {
		case "/no-validator.zip":
		case "/hidden-auth.zip":
			// Anonymous replay offers a same-sized, different attachment.
			w.Header().Set("ETag", `"public-fallback"`)
			_, _ = w.Write([]byte("fake"))
			return
		default:
			w.Header().Set("ETag", `"expected"`)
		}
		_, _ = w.Write([]byte("data"))
	}))
	defer fixture.Close()
	cfg := handoffServiceConfig(t)
	runner, err := engine.NewRunner(engine.Config{MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Runner = runner
	svc := startHandoffService(t, cfg)
	token := approveHandoffPairing(t, svc, 'a')
	body := func(name, etag string) string {
		proof := sha256.Sum256([]byte("attachment; filename=public.zip\napplication/zip\n4\n" + etag))
		return fmt.Sprintf(`{"handoffId":"97eba4cf-7936-4d48-96fd-8e1110a9e2d8","url":"%s/%s.zip","captureProof":"%x"}`, fixture.URL, name, proof)
	}
	for _, name := range []string{"private", "login", "hidden-auth", "no-validator"} {
		rejected := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body(name, `"expected"`), http.StatusBadRequest)
		if rejected.Error.Code != "CAPTURE_NOT_REPLAYABLE" {
			t.Fatalf("%s capture = %+v", name, rejected)
		}
	}
	rejected := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body("no-validator", ""), http.StatusBadRequest)
	if rejected.Error.Code != "CAPTURE_NOT_REPLAYABLE" {
		t.Fatalf("proof without a validator was accepted: %+v", rejected)
	}
	accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body("public", `"expected"`), http.StatusCreated)
	repeat := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body("public", `"expected"`), http.StatusOK)
	if accepted.TaskID == "" || repeat.TaskID != accepted.TaskID {
		t.Fatal("automatic capture did not persist one idempotent receipt")
	}
}

func TestManualHandoffRetainsItsTaskAfterRestart(t *testing.T) {
	cfg := handoffServiceConfig(t)
	ctx := context.Background()
	svc := startHandoffService(t, cfg)
	token := approveHandoffPairing(t, svc, 'a')
	accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusCreated)
	if accepted.TaskID == "" {
		t.Fatal("acknowledgement omitted the task ID")
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}

	svc = startHandoffService(t, cfg)
	replay := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusOK)
	items, err := svc.Manager().List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != accepted.TaskID || replay.TaskID != accepted.TaskID {
		t.Fatalf("queue after restart = %+v (%v), original = %q, replay = %q", items, err, accepted.TaskID, replay.TaskID)
	}
}

func TestManualHandoffReconcilesAResponseLostAfterCommit(t *testing.T) {
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	token := approveHandoffPairing(t, svc, 'a')
	upstream, err := url.Parse("http://" + svc.Address())
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan string, 1)
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	proxy.ModifyResponse = func(resp *http.Response) error {
		var acknowledgement extensionReply
		if err := json.NewDecoder(resp.Body).Decode(&acknowledgement); err != nil {
			return err
		}
		committed <- acknowledgement.TaskID
		return fmt.Errorf("test dropped the acknowledgement")
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}
	lost := httptest.NewServer(proxy)
	defer lost.Close()
	resp, err := http.DefaultClient.Do(handoffHTTP(t, lost.URL, token))
	if err == nil {
		resp.Body.Close()
		t.Fatal("browser received a response that should have been lost")
	}
	select {
	case taskID := <-committed:
		if taskID == "" {
			t.Fatal("the desktop never acknowledged the original request")
		}
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
		svc = startHandoffService(t, cfg)
		replay := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusOK)
		items, err := svc.Manager().List(context.Background())
		if err != nil || len(items) != 1 || items[0].ID != taskID || replay.TaskID != taskID {
			t.Fatalf("retry = %q, queue = %+v (%v), want original %q", replay.TaskID, items, err, taskID)
		}
	default:
		t.Fatal("the proxy did not drop an acknowledged handoff")
	}
}

func TestManualHandoffCommitFailureDoesNotReserveOrAcknowledgeTask(t *testing.T) {
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	token := approveHandoffPairing(t, svc, 'a')
	// A deferred constraint fails at COMMIT, after both the handoff and task
	// writes have been attempted. Observe results only through service APIs.
	fault := handoffFixtureDB(t, cfg.DatabasePath)
	if _, err := fault.Exec(`CREATE TABLE handoff_fault_parent (id INTEGER PRIMARY KEY);
CREATE TABLE handoff_fault (id INTEGER REFERENCES handoff_fault_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER fail_handoff_commit AFTER INSERT ON downloads BEGIN INSERT INTO handoff_fault VALUES (1); END;`); err != nil {
		t.Fatal(err)
	}
	failed := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusInternalServerError)
	if failed.TaskID != "" || failed.Error.Code != "HANDOFF_STORAGE_FAILED" {
		t.Fatalf("failed commit response = %+v", failed)
	}
	if items, err := svc.Manager().List(context.Background()); err != nil || len(items) != 0 {
		t.Fatalf("failed commit left tasks = %+v (%v)", items, err)
	}
	if _, err := fault.Exec(`DROP TRIGGER fail_handoff_commit`); err != nil {
		t.Fatal(err)
	}
	accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusCreated)
	if accepted.TaskID == "" {
		t.Fatal("retry after recovery did not acknowledge a task")
	}
}

func TestManualHandoffConcurrentDeliveriesCreateOneTask(t *testing.T) {
	svc, _, _ := startService(t)
	token := approveHandoffPairing(t, svc, 'a')
	const deliveries = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	taskIDs := make(chan string, deliveries)
	created := make(chan bool, deliveries)
	for range deliveries {
		wg.Go(func() {
			<-start
			resp, err := http.DefaultClient.Do(handoffHTTP(t, "http://"+svc.Address(), token))
			if err != nil {
				t.Error(err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				t.Errorf("concurrent handoff returned HTTP %d", resp.StatusCode)
				return
			}
			var reply extensionReply
			if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
				t.Error(err)
				return
			}
			taskIDs <- reply.TaskID
			if resp.StatusCode == http.StatusCreated {
				created <- true
			}
		})
	}
	close(start)
	wg.Wait()
	close(taskIDs)
	items, err := svc.Manager().List(context.Background())
	if err != nil || len(items) != 1 || len(created) != 1 || len(taskIDs) != deliveries {
		t.Fatalf("created responses = %d, deliveries = %d, queue = %+v (%v)", len(created), len(taskIDs), items, err)
	}
	for id := range taskIDs {
		if id != items[0].ID {
			t.Fatalf("request received task %q, want %q", id, items[0].ID)
		}
	}
}

func TestManualHandoffKeepsIntentAndPairingSeparate(t *testing.T) {
	svc, adminPath, _ := startService(t)
	addr := svc.Address()
	token := approveHandoffPairing(t, svc, 'a')
	other := approveHandoffPairing(t, svc, 'b')
	admin, err := service.ReadToken(adminPath)
	if err != nil {
		t.Fatal(err)
	}
	extensionPost(t, addr, handoffPath, "https://example.com", token, handoffBody, http.StatusForbidden)
	extensionPost(t, addr, handoffPath, pairingOrigin, admin, handoffBody, http.StatusUnauthorized)
	extensionPost(t, addr, handoffPath, pairingOrigin, strings.Repeat("c", 64), handoffBody, http.StatusForbidden)
	original := extensionPost(t, addr, handoffPath, pairingOrigin, token, handoffBody, http.StatusCreated)
	conflict := extensionPost(t, addr, handoffPath, pairingOrigin, token, strings.Replace(handoffBody, "public.zip", "other.zip", 1), http.StatusConflict)
	if conflict.Error.Code != "HANDOFF_CONFLICT" || conflict.TaskID != "" {
		t.Fatalf("handoff conflict response = %+v", conflict)
	}
	unsupported := extensionPost(t, addr, handoffPath, pairingOrigin, token, strings.Replace(handoffBody, "https://example.com", "blob:https://example.com", 1), http.StatusBadRequest)
	if unsupported.Error.Code != "UNSUPPORTED_LINK" || unsupported.TaskID != "" {
		t.Fatalf("unsupported link response = %+v", unsupported)
	}
	otherProfile := extensionPost(t, addr, handoffPath, pairingOrigin, other, handoffBody, http.StatusCreated)
	laterClick := strings.Replace(handoffBody, "97eba4cf", "17eba4cf", 1)
	another := extensionPost(t, addr, handoffPath, pairingOrigin, token, laterClick, http.StatusCreated)
	if otherProfile.TaskID == original.TaskID || another.TaskID == original.TaskID || another.TaskID == otherProfile.TaskID {
		t.Fatal("distinct pairings or intents were deduplicated")
	}
	ctx := context.Background()
	pairing := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	if err := svc.RevokeExtensionPairing(ctx, pairing.ID); err != nil {
		t.Fatal(err)
	}
	response := extensionPost(t, addr, handoffPath, pairingOrigin, token, handoffBody, http.StatusForbidden)
	if response.TaskID != "" || response.Error.Code != "PAIRING_REQUIRED" {
		t.Fatalf("revoked pairing response = %+v", response)
	}
	items, err := svc.Manager().List(ctx)
	if err != nil || len(items) != 3 {
		t.Fatalf("queue = %+v (%v), want only the three accepted tasks", items, err)
	}
}

type handoffQueueRunner struct {
	idleRunner
	started chan download.Spec
}

func (r handoffQueueRunner) Download(ctx context.Context, spec download.Spec, _ func(download.Progress)) error {
	r.started <- spec
	<-ctx.Done()
	return ctx.Err()
}

func TestManualHandoffRespectsQueueScheduleAndLimits(t *testing.T) {
	cfg := handoffServiceConfig(t)
	started := make(chan download.Spec, 3)
	cfg.Runner = handoffQueueRunner{started: started}
	svc := startHandoffService(t, cfg)
	ctx := context.Background()
	preferences := settings.Default()
	preferences.Connections = 6
	preferences.HostConnections = 3
	preferences.SpeedLimit = 9000
	later := time.Now().Add(2 * time.Hour)
	preferences.Schedule.Start = later.Format("15:04")
	preferences.Schedule.Stop = later.Add(time.Hour).Format("15:04")
	if err := svc.Manager().UpdateSettings(ctx, preferences); err != nil {
		t.Fatal(err)
	}
	token := approveHandoffPairing(t, svc, 'a')
	for _, body := range []string{handoffBody, strings.Replace(handoffBody, "97eba4cf", "17eba4cf", 1)} {
		extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body, http.StatusCreated)
	}
	items, err := svc.Manager().List(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("queue = %+v (%v)", items, err)
	}
	for _, item := range items {
		if item.Status != download.StatusQueued || item.Priority != download.PriorityNormal || item.Connections != 0 || item.SpeedLimit != 0 {
			t.Fatalf("handoff did not inherit queue defaults: %+v", item)
		}
	}
	preferences.Schedule = settings.Schedule{}
	if err := svc.Manager().UpdateSettings(ctx, preferences); err != nil {
		t.Fatal(err)
	}
	select {
	case spec := <-started:
		if spec.Connections != 3 || len(spec.Limiters) < 1 || spec.Limiters[0].Limit() != 9000 {
			t.Fatalf("handoff run did not inherit the host cap and speed limit: %+v", spec)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no queued handoff started when the schedule opened")
	}
	select {
	case <-started:
		t.Fatal("a second task for the same host exceeded its connection cap")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestManualHandoffMigratesExistingPairingAndDownload(t *testing.T) {
	cfg := handoffServiceConfig(t)
	fixture := handoffFixtureDB(t, cfg.DatabasePath)
	// Schema 4 shipped tasks, settings and extension pairings before handoff.
	if _, err := fixture.Exec(`CREATE TABLE downloads (
    id TEXT PRIMARY KEY, url TEXT NOT NULL, destination TEXT NOT NULL, status TEXT NOT NULL,
    completed INTEGER NOT NULL DEFAULT 0, total INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL, priority INTEGER NOT NULL DEFAULT 0,
    connections INTEGER NOT NULL DEFAULT 0, speed_limit INTEGER NOT NULL DEFAULT 0,
    checksum TEXT NOT NULL DEFAULT '', headers TEXT NOT NULL DEFAULT ''
);
CREATE INDEX downloads_created_at ON downloads(created_at, id);
CREATE TABLE settings (id INTEGER PRIMARY KEY CHECK (id = 1), value TEXT NOT NULL, secrets TEXT NOT NULL DEFAULT '');
CREATE TABLE extension_pairings (
    id TEXT PRIMARY KEY, token_digest BLOB NOT NULL UNIQUE CHECK (length(token_digest) = 32),
    browser TEXT NOT NULL CHECK (browser IN ('chrome', 'edge')),
    status TEXT NOT NULL CHECK (status IN ('awaiting-approval', 'connected', 'denied', 'revoked')),
    code TEXT NOT NULL, requested_at INTEGER NOT NULL, expires_at INTEGER NOT NULL
);
PRAGMA user_version = 4;`); err != nil {
		t.Fatal(err)
	}
	const existingID = "existing-download"
	if _, err := fixture.Exec(`INSERT INTO downloads (id, url, destination, status, created_at, updated_at)
VALUES (?, 'https://example.com/existing.zip', ?, 'paused', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
		existingID, filepath.Join(cfg.DownloadRoots[0], "existing.zip")); err != nil {
		t.Fatal(err)
	}
	credential := make([]byte, 32)
	digest := sha256.Sum256(credential)
	if _, err := fixture.Exec(`INSERT INTO extension_pairings
(id, token_digest, browser, status, code, requested_at, expires_at) VALUES ('test-pairing', ?, 'chrome', 'connected', '', 0, 0)`, digest[:]); err != nil {
		t.Fatal(err)
	}
	svc := startHandoffService(t, cfg)
	token := hex.EncodeToString(credential)
	accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, handoffBody, http.StatusCreated)
	items, err := svc.Manager().List(context.Background())
	if err != nil || len(items) != 2 || items[0].ID != existingID || items[1].ID != accepted.TaskID {
		t.Fatalf("migrated queue = %+v (%v), want %q and %q", items, err, existingID, accepted.TaskID)
	}
}

func handoffHTTP(t *testing.T, serviceURL, token string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, serviceURL+handoffPath, strings.NewReader(handoffBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", pairingOrigin)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func handoffFixtureDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func handoffServiceConfig(t *testing.T) service.Config {
	t.Helper()
	dir := t.TempDir()
	return service.Config{
		Address:       "127.0.0.1:0",
		DatabasePath:  filepath.Join(dir, "nimget.db"),
		TokenPath:     filepath.Join(dir, "token"),
		DownloadRoots: []string{filepath.Join(dir, "downloads")},
		Runner:        idleRunner{},
		Sealer:        testSealer(t),
	}
}

func startHandoffService(t *testing.T, cfg service.Config) *service.Service {
	t.Helper()
	svc, err := service.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func approveHandoffPairing(t *testing.T, svc *service.Service, tokenByte rune) string {
	t.Helper()
	token := strings.Repeat(string(tokenByte), 64)
	pending := extensionPost(t, svc.Address(), pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if err := svc.ApproveExtensionPairing(context.Background(), pending.ID, pending.Code); err != nil {
		t.Fatal(err)
	}
	return token
}
