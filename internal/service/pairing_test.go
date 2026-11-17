package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amemiya02/nimget/internal/service"
)

const (
	pairingOrigin  = "chrome-extension://cbpkcafcmablmlkpkmmeincekjglfafb"
	pairingRequest = "/v1/extension/pairing"
	pairingStatus  = "/v1/extension/pairing/status"
)

type extensionReply struct {
	WebsiteSessionPersistence string                `json:"websiteSessionPersistence"`
	TaskID                    string                `json:"taskId"`
	ID                        string                `json:"id"`
	Status                    string                `json:"status"`
	Code                      string                `json:"code"`
	Error                     struct{ Code string } `json:"error"`
}

func extensionPost(t *testing.T, addr, path, origin, token, body string, wantStatus int) extensionReply {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+path, bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s returned HTTP %d, want %d", path, resp.StatusCode, wantStatus)
	}
	wantOrigin := ""
	if origin == pairingOrigin && strings.HasPrefix(path, "/v1/extension/") {
		wantOrigin = pairingOrigin
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != wantOrigin {
		t.Fatalf("allowed origin = %q, want %q", got, wantOrigin)
	}
	var got extensionReply
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestExtensionRequestsAwaitDesktopApproval(t *testing.T) {
	svc, _, _ := startService(t)
	token := strings.Repeat("a", 64)
	addr := svc.Address()

	got := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	if got.Status != "disconnected" {
		t.Fatalf("unpaired status = %q", got.Status)
	}
	got = extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if got.Status != "awaiting-approval" || got.ID == "" || len(got.Code) != 8 {
		t.Fatal("pairing request did not return a pending confirmation")
	}
	retry := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	if retry != got {
		t.Fatal("pending confirmation changed when polled")
	}
}

func TestExtensionApprovalAndRevocationSurviveServiceRestart(t *testing.T) {
	dir := t.TempDir()
	cfg := service.Config{
		Address:       "127.0.0.1:0",
		DatabasePath:  filepath.Join(dir, "nimget.db"),
		TokenPath:     filepath.Join(dir, "token"),
		DownloadRoots: []string{filepath.Join(dir, "downloads")},
		Runner:        idleRunner{},
		Sealer:        testSealer(t),
	}
	ctx := context.Background()
	start := func() *service.Service {
		svc, err := service.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = svc.Close() })
		if err := svc.Start(ctx); err != nil {
			t.Fatal(err)
		}
		return svc
	}
	closeService := func(svc *service.Service) {
		if err := svc.Close(); err != nil {
			t.Fatal(err)
		}
	}

	svc := start()
	token := strings.Repeat("b", 64)
	pending := extensionPost(t, svc.Address(), pairingRequest, pairingOrigin, token, `{"browser":"edge"}`, http.StatusOK)
	closeService(svc)

	svc = start()
	records, err := svc.ListExtensionPairings(ctx)
	if err != nil || len(records) != 1 || records[0].ID != pending.ID || records[0].Browser != "edge" || records[0].Status != "awaiting-approval" {
		t.Fatalf("pending desktop records after restart = %+v (%v)", records, err)
	}
	if err := svc.ApproveExtensionPairing(ctx, pending.ID, pending.Code+"x"); err == nil {
		t.Fatal("desktop accepted an incorrect confirmation code")
	}
	if err := svc.ApproveExtensionPairing(ctx, pending.ID, pending.Code); err != nil {
		t.Fatal(err)
	}
	closeService(svc)

	svc = start()
	addr := svc.Address()
	got := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	if got.Status != "connected" || got.ID != pending.ID || got.Code != "" {
		t.Fatal("approval did not persist or approval code was retained")
	}
	extensionPost(t, addr, "/v1/downloads", pairingOrigin, token, "{}", http.StatusForbidden)
	extensionPost(t, addr, "/v1/downloads", "", token, "{}", http.StatusUnauthorized)
	extensionPost(t, addr, pairingStatus, "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", token, "{}", http.StatusForbidden)
	extensionPost(t, addr, pairingStatus, "https://example.com", token, "{}", http.StatusForbidden)
	extensionPost(t, addr, pairingStatus, "", token, "{}", http.StatusForbidden)
	extensionPost(t, addr, pairingStatus, pairingOrigin, "", "{}", http.StatusUnauthorized)
	admin, err := service.ReadToken(cfg.TokenPath)
	if err != nil {
		t.Fatal(err)
	}
	extensionPost(t, addr, pairingStatus, pairingOrigin, admin, "{}", http.StatusUnauthorized)

	if err := svc.RevokeExtensionPairing(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	closeService(svc)

	svc = start()
	addr = svc.Address()
	got = extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"edge"}`, http.StatusOK)
	if got.Status != "revoked" || got.ID != pending.ID || got.Code != "" {
		t.Fatal("revoked pairing could be requested again")
	}
	got = extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	if got.Status != "revoked" || got.ID != pending.ID {
		t.Fatal("revoked state did not survive restart")
	}
}

func TestDesktopDenialCannotBeRetriedWithTheSameCredential(t *testing.T) {
	svc, _, _ := startService(t)
	ctx := context.Background()
	token := strings.Repeat("c", 64)
	addr := svc.Address()
	pending := extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if err := svc.DenyExtensionPairing(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApproveExtensionPairing(ctx, pending.ID, pending.Code); err == nil {
		t.Fatal("desktop could approve an already denied request")
	}
	got := extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if got.Status != "denied" || got.ID != pending.ID || got.Code != "" {
		t.Fatal("denied request was reopened")
	}
	fresh := extensionPost(t, addr, pairingRequest, pairingOrigin, strings.Repeat("d", 64), `{"browser":"chrome"}`, http.StatusOK)
	if fresh.Status != "awaiting-approval" || fresh.ID == pending.ID {
		t.Fatal("fresh pairing did not require separate approval")
	}
}

func TestExtensionPreflightRequiresApprovedOriginAndLoopbackHost(t *testing.T) {
	svc, _, _ := startService(t)
	for _, tc := range []struct {
		name, origin, host string
		want               int
	}{
		{"nimget", pairingOrigin, "127.0.0.1", http.StatusNoContent},
		{"website", "https://example.com", "127.0.0.1", http.StatusForbidden},
		{"unrelated extension", "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "127.0.0.1", http.StatusForbidden},
		{"rebinding", pairingOrigin, "example.com", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodOptions, "http://"+svc.Address()+pairingStatus, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = tc.host
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", http.MethodPost)
			req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Fatalf("preflight status = %d, want %d", resp.StatusCode, tc.want)
			}
			wantOrigin := ""
			if tc.want == http.StatusNoContent {
				wantOrigin = pairingOrigin
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != wantOrigin {
				t.Fatalf("preflight allowed origin = %q, want %q", got, wantOrigin)
			}
		})
	}
}
