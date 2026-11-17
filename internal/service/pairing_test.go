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

// requireApproval turns off automatic connection so requests wait for the
// desktop approval code.
func requireApproval(t *testing.T, svc *service.Service) {
	t.Helper()
	prefs := svc.Manager().Settings()
	prefs.Extensions.AutoConnect = false
	if err := svc.Manager().UpdateSettings(context.Background(), prefs); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionRequestsAwaitDesktopApproval(t *testing.T) {
	svc, _, _ := startService(t)
	requireApproval(t, svc)
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
	requireApproval(t, svc)
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
	requireApproval(t, svc)
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

func TestExtensionConnectsAutomaticallyUntilRevoked(t *testing.T) {
	svc, _, _ := startService(t)
	ctx := context.Background()
	addr := svc.Address()
	token := strings.Repeat("e", 64)

	got := extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if got.Status != "connected" || got.ID == "" || got.Code != "" {
		t.Fatalf("automatic request = %+v, want connected without a code", got)
	}
	if status := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK); status != got {
		t.Fatalf("status = %+v, want %+v", status, got)
	}
	// Other origins still cannot pair.
	extensionPost(t, addr, pairingRequest, "chrome-extension://aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", strings.Repeat("f", 64), `{"browser":"chrome"}`, http.StatusForbidden)

	if err := svc.RevokeExtensionPairing(ctx, got.ID); err != nil {
		t.Fatal(err)
	}
	again := extensionPost(t, addr, pairingRequest, pairingOrigin, token, `{"browser":"chrome"}`, http.StatusOK)
	if again.Status != "revoked" || again.ID != got.ID {
		t.Fatalf("revoked credential reconnected: %+v", again)
	}

	// A request made while approval was required is approved once the
	// extension asks again after automatic connection is turned on.
	requireApproval(t, svc)
	waiting := strings.Repeat("9", 64)
	pending := extensionPost(t, addr, pairingRequest, pairingOrigin, waiting, `{"browser":"chrome"}`, http.StatusOK)
	if pending.Status != "awaiting-approval" {
		t.Fatalf("request with approval required = %+v", pending)
	}
	prefs := svc.Manager().Settings()
	prefs.Extensions.AutoConnect = true
	if err := svc.Manager().UpdateSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}
	approved := extensionPost(t, addr, pairingRequest, pairingOrigin, waiting, `{"browser":"chrome"}`, http.StatusOK)
	if approved.Status != "connected" || approved.ID != pending.ID {
		t.Fatalf("pending request after enabling automatic connection = %+v", approved)
	}
}

func TestDeleteExtensionPairingForgetsOnlyEndedPairings(t *testing.T) {
	svc, _, _ := startService(t)
	ctx := context.Background()
	addr := svc.Address()
	token := approveHandoffPairing(t, svc, '7')
	connected := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK)
	body := `{"handoffId":"97eba4cf-7936-4d48-96fd-8e1110a9e2d8","url":"https://example.com/file.bin"}`
	extensionPost(t, addr, handoffPath, pairingOrigin, token, body, http.StatusCreated)

	if err := svc.DeleteExtensionPairing(ctx, connected.ID); err == nil {
		t.Fatal("a connected pairing was deleted")
	}
	if err := svc.RevokeExtensionPairing(ctx, connected.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteExtensionPairing(ctx, connected.ID); err != nil {
		t.Fatal(err)
	}
	if records, err := svc.ListExtensionPairings(ctx); err != nil || len(records) != 0 {
		t.Fatalf("records after delete = %+v (%v)", records, err)
	}
	if got := extensionPost(t, addr, pairingStatus, pairingOrigin, token, "{}", http.StatusOK); got.Status != "disconnected" {
		t.Fatalf("deleted credential status = %q", got.Status)
	}
	if err := svc.DeleteExtensionPairing(ctx, connected.ID); err == nil {
		t.Fatal("deleting a missing pairing succeeded")
	}

	requireApproval(t, svc)
	pending := extensionPost(t, addr, pairingRequest, pairingOrigin, strings.Repeat("8", 64), `{"browser":"edge"}`, http.StatusOK)
	if err := svc.DeleteExtensionPairing(ctx, pending.ID); err == nil {
		t.Fatal("a pending request was deleted")
	}
	if err := svc.DenyExtensionPairing(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteExtensionPairing(ctx, pending.ID); err != nil {
		t.Fatal(err)
	}
}
