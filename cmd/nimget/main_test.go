package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
)

const mainEnv = "NIMGET_TEST_RUN_MAIN"

// TestMain lets the test binary act as the nimget executable in child processes.
func TestMain(m *testing.M) {
	if os.Getenv(mainEnv) == "1" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func nimget(args ...string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), mainEnv+"=1")
	return cmd
}

// throttledServer serves a deterministic file with ranges and a strong ETag,
// at about 2 MiB/s per connection, and counts the body bytes it sends.
func throttledServer(t *testing.T, data []byte, sent *atomic.Int64) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"e2e-v1"`)
		w.Header().Set("Accept-Ranges", "bytes")
		start, end := 0, len(data)-1
		status := http.StatusOK
		if spec, ok := strings.CutPrefix(r.Header.Get("Range"), "bytes="); ok {
			first, last, _ := strings.Cut(spec, "-")
			start, _ = strconv.Atoi(first)
			if last != "" {
				end, _ = strconv.Atoi(last)
			}
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		}
		w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
		w.WriteHeader(status)
		const piece = 256 << 10
		for offset := start; offset <= end; offset += piece {
			stop := min(offset+piece, end+1)
			n, err := w.Write(data[offset:stop])
			sent.Add(int64(n))
			if err != nil {
				return
			}
			if stop-offset == piece {
				time.Sleep(125 * time.Millisecond)
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

type serviceProcess struct {
	cmd     *exec.Cmd
	client  *client.Client
	address string
}

func startServiceProcess(t *testing.T, dir, root string) *serviceProcess {
	t.Helper()
	tokenPath := filepath.Join(dir, "token")
	cmd := nimget("service",
		"--listen", "127.0.0.1:0",
		"--database", filepath.Join(dir, "nimget.db"),
		"--token-file", tokenPath,
		"--download-root", root,
		"--log-level", "error",
	)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("service did not report its address: %v", err)
	}
	address, ok := strings.CutPrefix(strings.TrimSpace(line), "nimget service listening on ")
	if !ok {
		t.Fatalf("unexpected service output %q", line)
	}
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceProcess{cmd: cmd, address: "http://" + address, client: client.New("http://"+address, token, nil)}
}

func (p *serviceProcess) item(t *testing.T) download.Item {
	t.Helper()
	items, err := p.client.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %+v; want exactly one", items)
	}
	return items[0]
}

func TestAddCommandQueuesMagnetInSelectedDirectory(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "downloads")
	svc := startServiceProcess(t, dir, root)
	prefs, err := svc.client.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := svc.client.PutSettings(t.Context(), prefs); err != nil {
		t.Fatal(err)
	}
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=http%3A%2F%2F127.0.0.1%3A1%2Fannounce"
	cmd := nimget("--address", svc.address, "--token-file", filepath.Join(dir, "token"), "add", link, "--dir", filepath.Join("downloads", "magnets"))
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nimget add magnet: %v: %s", err, output)
	}
	var item download.Item
	if err := json.Unmarshal(output, &item); err != nil {
		t.Fatal(err)
	}
	if item.SourceKind != download.SourceMagnet || item.Status != download.StatusQueued || item.Directory != filepath.Join(root, "magnets") || svc.item(t).ID != item.ID {
		t.Fatalf("unexpected magnet submission: %+v", item)
	}
}

func TestServiceResumesAfterKill(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end crash test takes several seconds")
	}
	// Eight connections at about 2 MiB/s each need about 8 s for 128 MiB, so
	// half the file is written and checkpointed well before the kill.
	data := make([]byte, 128<<20)
	for i := range data {
		data[i] = byte(i % 251)
	}
	var sent atomic.Int64
	server := throttledServer(t, data, &sent)

	dir := t.TempDir()
	root := filepath.Join(dir, "downloads")
	dest := filepath.Join(root, "big.bin")

	first := startServiceProcess(t, dir, root)
	if _, err := first.client.Add(context.Background(), download.Request{URL: server.URL + "/big.bin", Destination: dest}); err != nil {
		t.Fatal(err)
	}
	// The server's byte count is a precise, poll-free signal of progress.
	deadline := time.Now().Add(30 * time.Second)
	for sent.Load() < 64<<20 {
		if time.Now().After(deadline) {
			t.Fatalf("half the file was not sent: %+v", first.item(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	halfway := time.Now()
	// Checkpoints are written every 2 s; wait for one after the halfway mark.
	for {
		info, err := os.Stat(dest + ".part.meta")
		if err == nil && info.ModTime().After(halfway) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no checkpoint after the halfway mark: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if item := first.item(t); item.Status != download.StatusRunning {
		t.Fatalf("download no longer running before the kill: %+v", item)
	}
	if err := first.cmd.Process.Kill(); err != nil { // SIGKILL / TerminateProcess
		t.Fatal(err)
	}
	_ = first.cmd.Wait()
	sentBeforeKill := sent.Load()

	second := startServiceProcess(t, dir, root)
	deadline = time.Now().Add(60 * time.Second)
	for {
		item := second.item(t)
		if item.Status == download.StatusCompleted {
			break
		}
		if item.Status == download.StatusFailed || time.Now().After(deadline) {
			t.Fatalf("download after restart = %+v", item)
		}
		time.Sleep(100 * time.Millisecond)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatal("resumed file differs from the source")
	}
	if sentBeforeKill >= int64(len(data)) {
		t.Fatalf("the whole file was sent before the kill; the test did not interrupt anything")
	}
	resent := sent.Load() - sentBeforeKill
	if resent >= int64(len(data)) {
		t.Fatalf("restart re-downloaded %d of %d bytes; checkpointed chunks were not reused", resent, len(data))
	}
	t.Logf("sent %d bytes before the kill, %d after restart (file is %d)", sentBeforeKill, resent, len(data))
	for _, suffix := range []string{".part", ".part.meta", ".lock"} {
		if _, err := os.Stat(dest + suffix); err == nil {
			t.Errorf("temporary file %s remains", dest+suffix)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	out, err := nimget("version").CombinedOutput()
	if err != nil || !strings.HasPrefix(string(out), "nimget dev (commit none") {
		t.Fatalf("version output = %q, %v", out, err)
	}
}

func TestParseSpeedAndPriority(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "500": 500, "500K": 500 << 10, "2m": 2 << 20, "1.5M": 3 << 19, "1G/s": 1 << 30, "4KB": 4096} {
		if got, err := parseSpeed(in); err != nil || got != want {
			t.Errorf("parseSpeed(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"-1", "fast", "2T"} {
		if _, err := parseSpeed(bad); err == nil {
			t.Errorf("parseSpeed(%q) accepted", bad)
		}
	}
	if p, err := parsePriority("HIGH"); err != nil || p != 1 {
		t.Fatalf("parsePriority = %d, %v", p, err)
	}
	if _, err := parsePriority("urgent"); err == nil {
		t.Fatal("unknown priority accepted")
	}
}

func TestQueueCommandsKeepSourceCredentialsOutOfSharedOutput(t *testing.T) {
	dir := t.TempDir()
	p := startServiceProcess(t, dir, dir)
	cfg, err := p.client.Settings(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cfg.Schedule = settings.Schedule{Start: now.Add(time.Hour).Format("15:04"), Stop: now.Add(2 * time.Hour).Format("15:04")}
	if _, err := p.client.PutSettings(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.RequestURI != "/path-secret/file-secret.zip?sig=query-secret" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("expected bytes"))
	}))
	defer fixture.Close()
	url := fixture.URL + "/path-secret/file-secret.zip?sig=query-secret"
	if _, err := p.client.Add(t.Context(), download.Request{URL: url, Headers: map[string]string{"Cookie": "cookie-secret"}}); err != nil {
		t.Fatal(err)
	}

	command := func(args ...string) *exec.Cmd {
		return nimget(append([]string{"--address", p.address, "--token-file", filepath.Join(dir, "token")}, args...)...)
	}
	summary, err := command("export").CombinedOutput()
	if err != nil || !bytes.Contains(summary, []byte(`"mode": "summary"`)) {
		t.Fatalf("export summary: %v, %s", err, summary)
	}
	assertSafe := func(output []byte) {
		t.Helper()
		for _, marker := range []string{"path-secret", "file-secret", "query-secret", "cookie-secret"} {
			if bytes.Contains(output, []byte(marker)) {
				t.Errorf("ordinary output exposed %s", marker)
			}
		}
	}
	assertSafe(summary)
	summaryImport := command("import", "-")
	summaryImport.Stdin = bytes.NewReader(summary)
	if output, err := summaryImport.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("contain no source URLs")) {
		t.Fatalf("import summary = %q, %v", output, err)
	}
	if output, err := command("export", "--include-source-urls", "-").CombinedOutput(); err == nil {
		t.Fatal("backup was written to standard output")
	} else {
		assertSafe(output)
	}

	backupPath := filepath.Join(dir, "backup.json")
	if output, err := command("export", "--include-source-urls", backupPath).CombinedOutput(); err != nil {
		t.Fatalf("export backup = %q, %v", output, err)
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil || !bytes.Contains(backup, []byte(url)) || bytes.Contains(backup, []byte("cookie-secret")) {
		t.Fatalf("invalid source backup with exact link and omitted credentials: %v", err)
	}
	if count, err := p.client.List(t.Context()); err != nil || len(count) != 1 {
		t.Fatalf("summary import added tasks: %d, %v", len(count), err)
	}

	original := p.item(t)
	if err := p.client.Delete(t.Context(), original.ID, false); err != nil {
		t.Fatal(err)
	}
	if output, err := command("import", backupPath).CombinedOutput(); err != nil {
		t.Fatalf("import backup = %q, %v", output, err)
	} else {
		assertSafe(output)
	}
	if restored := p.item(t); restored.URL != url || restored.Destination != original.Destination {
		t.Fatal("backup did not restore exact source and destination")
	}

	legacy := fmt.Sprintf(`{"version":1,"downloads":[{"url":%q},{"url":%q,"priority":2}]}`, url, url)
	unacknowledged := command("import", "-")
	unacknowledged.Stdin = strings.NewReader(legacy)
	message, err := unacknowledged.CombinedOutput()
	if err == nil || !bytes.Contains(message, []byte("--allow-legacy-urls")) {
		t.Fatalf("legacy file accepted before its URLs were acknowledged: %q, %v", message, err)
	}
	if count, err := p.client.List(t.Context()); err != nil || len(count) != 1 {
		t.Fatalf("legacy refusal added tasks: %d, %v", len(count), err)
	}
	input := command("import", "--allow-legacy-urls", "-")
	input.Stdin = strings.NewReader(legacy)
	output, err := input.CombinedOutput()
	if err == nil || !bytes.Contains(output, []byte("Entry 2")) || !bytes.Contains(output, []byte("Added 1 of 2 downloads")) {
		t.Fatalf("partial import result = %q, %v", output, err)
	}
	assertSafe(output)
}
