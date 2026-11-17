package service_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
	"github.com/amemiya02/nimget/internal/store"
)

type idleRunner struct{}

func (idleRunner) Inspect(context.Context, string, http.Header) (download.Remote, error) {
	return download.Remote{}, nil
}

func (idleRunner) Download(context.Context, download.Spec, func(download.Progress)) error {
	return nil
}

func (idleRunner) Discard(download.Spec) error { return nil }

func startService(t *testing.T) (*service.Service, string, context.CancelFunc) {
	t.Helper()
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "token")
	svc, err := service.New(service.Config{
		Address:       "127.0.0.1:0",
		DatabasePath:  filepath.Join(dir, "nimget.db"),
		TokenPath:     tokenPath,
		DownloadRoots: []string{filepath.Join(dir, "downloads")},
		Runner:        idleRunner{},
		Sealer:        testSealer(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := svc.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = svc.Wait()
	})
	return svc, tokenPath, cancel
}

func get(t *testing.T, url, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestServiceStartsOnLoopbackAndAnswersPing(t *testing.T) {
	svc, _, _ := startService(t)
	if status := get(t, "http://"+svc.Address()+"/v1/ping", ""); status != http.StatusOK {
		t.Fatalf("ping status = %d", status)
	}
}

func TestServiceRequiresItsTokenAndStopsOnCancel(t *testing.T) {
	svc, tokenPath, cancel := startService(t)
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(tokenPath); err == nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("token file mode = %v; want owner-only", info.Mode().Perm())
	}
	list := "http://" + svc.Address() + "/v1/downloads"
	if status := get(t, list, ""); status != http.StatusUnauthorized {
		t.Fatalf("list without token = %d; want 401", status)
	}
	if status := get(t, list, token); status != http.StatusOK {
		t.Fatalf("list with token = %d; want 200", status)
	}

	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- svc.Wait() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Wait after cancel = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("service did not stop after its context was canceled")
	}
}

func TestBatchEntriesKeepAcceptedTasksWhenAnEntryIsRejected(t *testing.T) {
	svc, tokenPath, _ := startService(t)
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	c := client.New("http://"+svc.Address(), token, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const firstURL = "https://example.test/first.zip"
	const lastURL = "https://example.test/last.zip"
	const correctedURL = "https://example.test/corrected.zip"

	add := func(rawURL string) download.Item {
		t.Helper()
		item, err := c.Add(ctx, download.Request{URL: rawURL})
		if err != nil || item.ID == "" || item.URL != rawURL {
			t.Fatalf("Add(%q) = %+v, %v; want an accepted task", rawURL, item, err)
		}
		return item
	}
	assertTasks := func(want ...download.Item) {
		t.Helper()
		items, err := c.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != len(want) {
			t.Fatalf("List returned %d tasks; want %d", len(items), len(want))
		}
		urlByID := make(map[string]string, len(items))
		for _, item := range items {
			urlByID[item.ID] = item.URL
		}
		for _, item := range want {
			if urlByID[item.ID] != item.URL {
				t.Errorf("List is missing accepted task %s for %s", item.ID, item.URL)
			}
		}
	}

	first := add(firstURL)
	if _, err := c.Add(ctx, download.Request{URL: "invalid URL"}); err == nil {
		t.Fatal("invalid URL was accepted")
	}
	last := add(lastURL)
	assertTasks(first, last)

	corrected := add(correctedURL)
	assertTasks(first, last, corrected)

	duplicate := add(firstURL)
	if duplicate.ID == first.ID || duplicate.Destination == first.Destination {
		t.Fatalf("intentional duplicate reused task identity or destination: first=%+v duplicate=%+v", first, duplicate)
	}
	assertTasks(first, last, corrected, duplicate)
}

func TestLoadOrCreateTokenIsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "token")
	first, err := service.LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || first != second {
		t.Fatalf("tokens = %q, %q; want one stable 64-hex token", first, second)
	}
}

func testSealer(t *testing.T) *secrets.Sealer {
	s, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSiteHeaderEditsRejectNewCaseCollisionsAndPreserveLegacyRules(t *testing.T) {
	ctx := context.Background()
	cfg := handoffServiceConfig(t)
	legacy := settings.Default()
	legacy.Sites = []settings.Site{
		{Host: "example.test", Headers: map[string]string{"X-Example": "old", "x-example": "old lowercase"}},
	}
	db, err := store.Open(cfg.DatabasePath, cfg.Sealer)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SaveSettings(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	svc := startHandoffService(t, cfg)
	token, err := service.ReadToken(cfg.TokenPath)
	if err != nil {
		t.Fatal(err)
	}
	c := client.New("http://"+svc.Address(), token, nil)

	current, err := c.Settings(ctx)
	if err != nil || len(current.Sites) != 1 {
		t.Fatalf("load legacy settings: %v", err)
	}
	current.MaxConcurrent++
	if _, err := c.PutSettings(ctx, current); err != nil {
		t.Fatalf("editing unrelated settings: %v", err)
	}

	current.Sites[0].Headers["X-EXAMPLE"] = "duplicate"
	if _, err := c.PutSettings(ctx, current); err == nil {
		t.Fatal("accepted a new case-insensitive duplicate")
	}
	delete(current.Sites[0].Headers, "X-EXAMPLE")
	delete(current.Sites[0].Headers, "x-example")
	if _, err := c.PutSettings(ctx, current); err != nil {
		t.Fatalf("repairing a legacy rule: %v", err)
	}
}
