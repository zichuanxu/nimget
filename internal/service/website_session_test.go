package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/queuefile"
	"github.com/amemiya02/nimget/internal/secrets"
	"github.com/amemiya02/nimget/internal/service"
	"github.com/amemiya02/nimget/internal/settings"
)

func TestWebsiteSessionHandoff(t *testing.T) {
	const body = "protected fixture bytes"
	const cookie = "session=valid-fixture-cookie"
	const stale = "session=expired-fixture-cookie"
	const userAgent = "NimGet browser fixture"
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("another HTTP origin received the website session")
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer unrelated.Close()
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect.bin" {
			http.Redirect(w, r, unrelated.URL, http.StatusFound)
			return
		}
		if r.URL.Path != "/public.bin" && (r.Header.Get("Cookie") != cookie ||
			r.Header.Get("User-Agent") != userAgent || r.Referer() != "http://"+r.Host+"/") {
			http.Error(w, r.Header.Get("Cookie"), http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		if r.Method != http.MethodHead {
			_, _ = w.Write([]byte(body))
		}
	}))
	defer site.Close()

	var logs bytes.Buffer
	cfg := handoffServiceConfig(t)
	cfg.Runner = nil
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	svc := startHandoffService(t, cfg)
	token := approveHandoffPairing(t, svc, 'a')
	expires := time.Now().Add(time.Hour).Unix()
	protected := websiteHandoffBody(t, "1", site.URL+"/protected.bin", cookie, site.URL+"/", userAgent, expires)

	accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, protected, http.StatusCreated)
	retry := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, protected, http.StatusOK)
	if accepted.TaskID == "" || retry.TaskID != accepted.TaskID || accepted.WebsiteSessionPersistence != "secure" {
		t.Fatal("protected handoff did not return a durable receipt")
	}
	conflict := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, strings.Replace(protected, cookie, stale, 1), http.StatusConflict)
	if conflict.Error.Code != "HANDOFF_CONFLICT" {
		t.Fatal("changed session bypassed the handoff fingerprint")
	}

	public := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, websiteHandoffBody(t, "2", site.URL+"/public.bin", "", "", "", 0), http.StatusCreated)
	for _, id := range []string{accepted.TaskID, public.TaskID} {
		item := waitWebsiteTask(t, svc, id, download.StatusCompleted)
		got, err := os.ReadFile(item.Destination)
		if err != nil || string(got) != body {
			t.Fatalf("downloaded bytes did not match fixture: %v", err)
		}
	}
	for _, tc := range []struct {
		id, path, cookie string
		expiry           int64
	}{
		{"3", "/expired.bin", stale, expires},
		{"4", "/redirect.bin", cookie, expires},
		{"5", "/expired-snapshot.bin", cookie, time.Now().Add(-time.Minute).Unix()},
	} {
		reply := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token,
			websiteHandoffBody(t, tc.id, site.URL+tc.path, tc.cookie, site.URL+"/", userAgent, tc.expiry), http.StatusCreated)
		item := waitWebsiteTask(t, svc, reply.TaskID, download.StatusFailed)
		if item.Failure != download.FailureAuthorization || !strings.Contains(item.Error, "website session") {
			t.Errorf("protected download lacks recovery guidance: %q", item.Error)
		}
	}
	mismatch := websiteHandoffBody(t, "6", site.URL+"/invalid.bin", cookie, unrelated.URL+"/", userAgent, expires)
	bad := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, mismatch, http.StatusBadRequest)
	if bad.Error.Code != "INVALID_WEBSITE_SESSION" {
		t.Fatal("cross-origin request context was accepted")
	}
	items, err := svc.Manager().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	exported, err := queuefile.ExportSummary(items)
	if err != nil {
		t.Fatal(err)
	}
	taskJSON, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range []string{logs.String(), string(exported), string(taskJSON)} {
		if strings.Contains(output, cookie) || strings.Contains(output, stale) {
			t.Fatal("website session appeared in diagnostics or exports")
		}
	}
	db := handoffFixtureDB(t, cfg.DatabasePath)
	var stored string
	if err := db.QueryRow("SELECT website_session FROM downloads WHERE id = ?", accepted.TaskID).Scan(&stored); err != nil || stored != "" {
		t.Fatalf("completed task retained its website session or could not be read: %v", err)
	}
}

func TestWebsiteSessionHandoffRestart(t *testing.T) {
	for _, tc := range []struct {
		name, persistence string
		want              download.Status
	}{
		{"keyring available", "secure", download.StatusCompleted},
		{"keyring unavailable", "memory-only", download.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const cookie = "session=restart-fixture-cookie"
			site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Cookie") != cookie {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Length", "4")
				if r.Method != http.MethodHead {
					_, _ = w.Write([]byte("data"))
				}
			}))
			defer site.Close()

			cfg := handoffServiceConfig(t)
			cfg.Runner = nil
			if tc.persistence == "memory-only" {
				cfg.Sealer.KeyringErr = errors.New("fixture keyring unavailable")
			}
			svc := startHandoffService(t, cfg)
			prefs := settings.Default()
			later := time.Now().Add(2 * time.Hour)
			prefs.Schedule.Start = later.Format("15:04")
			prefs.Schedule.Stop = later.Add(time.Hour).Format("15:04")
			if err := svc.Manager().UpdateSettings(context.Background(), prefs); err != nil {
				t.Fatal(err)
			}
			token := approveHandoffPairing(t, svc, 'a')
			body := websiteHandoffBody(t, "1", site.URL+"/restart.bin", cookie, "", "", time.Now().Add(time.Hour).Unix())
			accepted := extensionPost(t, svc.Address(), handoffPath, pairingOrigin, token, body, http.StatusCreated)
			if accepted.WebsiteSessionPersistence != tc.persistence {
				t.Fatalf("session persistence = %q, want %q", accepted.WebsiteSessionPersistence, tc.persistence)
			}
			if err := svc.Close(); err != nil {
				t.Fatal(err)
			}
			db := handoffFixtureDB(t, cfg.DatabasePath)
			var stored string
			if err := db.QueryRow("SELECT website_session FROM downloads WHERE id = ?", accepted.TaskID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(stored, "sealed:") || strings.Contains(stored, cookie) {
				t.Fatal("queued session was not encrypted")
			}
			_ = db.Close()
			if tc.persistence == "memory-only" {
				key := make([]byte, 32)
				key[0] = 1
				var err error
				cfg.Sealer, err = secrets.New(key)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Sealer.KeyringErr = errors.New("fixture keyring still unavailable")
			}
			svc = startHandoffService(t, cfg)
			receiptBody := fmt.Sprintf(`{"handoffId":"%s"}`, websiteHandoffID("1"))
			receipt := extensionPost(t, svc.Address(), handoffPath+"/status", pairingOrigin, token, receiptBody, http.StatusOK)
			if receipt.TaskID != accepted.TaskID || receipt.WebsiteSessionPersistence != tc.persistence {
				t.Fatal("receipt could not be resolved without the cookies after restart")
			}
			other := approveHandoffPairing(t, svc, 'b')
			extensionPost(t, svc.Address(), handoffPath+"/status", pairingOrigin, other, receiptBody, http.StatusNotFound)
			prefs.Schedule = settings.Schedule{}
			if err := svc.Manager().UpdateSettings(context.Background(), prefs); err != nil {
				t.Fatal(err)
			}
			item := waitWebsiteTask(t, svc, receipt.TaskID, tc.want)
			if tc.want == download.StatusFailed && (item.Failure != download.FailureAuthorization || !strings.Contains(item.Error, "website session")) {
				t.Fatalf("lost session lacked recovery guidance: %q", item.Error)
			}
		})
	}
}

func websiteHandoffID(prefix string) string { return prefix + "7eba4cf-7936-4d48-96fd-8e1110a9e2d8" }

func websiteHandoffBody(t *testing.T, prefix, url, cookie, referer, userAgent string, expiresAt int64) string {
	t.Helper()
	body := map[string]any{"handoffId": websiteHandoffID(prefix), "url": url}
	if cookie != "" {
		body["websiteSession"] = map[string]any{"cookie": cookie, "referer": referer, "userAgent": userAgent, "expiresAt": expiresAt}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func waitWebsiteTask(t *testing.T, svc *service.Service, id string, status download.Status) download.Item {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		items, err := svc.Manager().List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range items {
			if item.ID != id {
				continue
			}
			if item.Status == status {
				return item
			}
			if item.Status == download.StatusCompleted || item.Status == download.StatusFailed {
				t.Fatalf("task %s reached %s, want %s: %s", id, item.Status, status, item.Error)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s", id, status)
	return download.Item{}
}
