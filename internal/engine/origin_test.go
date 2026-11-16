package engine

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadCredentialsAcrossRedirectChain(t *testing.T) {
	data := []byte("downloaded data")
	file := &fileServer{data: data, etag: "test"}
	var otherURL, originURL string
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			checkRedirectCredentials(t, r, true)
			http.Redirect(w, r, "/same-origin", http.StatusFound)
		case "/same-origin":
			checkRedirectCredentials(t, r, true)
			http.Redirect(w, r, otherURL+"/other-origin", http.StatusFound)
		case "/finish":
			checkRedirectCredentials(t, r, false)
			file.ServeHTTP(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer origin.Close()
	originURL = origin.URL
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRedirectCredentials(t, r, false)
		if r.Header.Get("Range") == "" || r.UserAgent() != "test-downloader" {
			t.Error("redirect lost Range or User-Agent")
		}
		if strings.Contains(r.Referer(), "fixture-query-secret") {
			t.Error("redirect forwarded a signed URL in Referer")
		}
		http.Redirect(w, r, originURL+"/finish", http.StatusFound)
	}))
	defer other.Close()
	otherURL = other.URL

	cfg := testConfig()
	cfg.UserAgent = "test-downloader"
	cfg.Headers = http.Header{
		"Cookie":        {"session=fixture-cookie"},
		"Authorization": {"Bearer fixture-token"},
		"X-Api-Key":     {"fixture-key"},
		"Referer":       {origin.URL + "/page?token=fixture-query-secret"},
	}
	dest := filepath.Join(t.TempDir(), "output.bin")
	if err := run(t, cfg, origin.URL+"/start", dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
}

func checkRedirectCredentials(t *testing.T, r *http.Request, allowed bool) {
	t.Helper()
	for name, value := range map[string]string{
		"Cookie":        "session=fixture-cookie",
		"Authorization": "Bearer fixture-token",
		"X-Api-Key":     "fixture-key",
	} {
		if !allowed {
			value = ""
		}
		if r.Header.Get(name) != value {
			t.Errorf("%s did not follow credential policy on %s", name, r.URL.Path)
		}
	}
}

func TestDownloadStripsCredentialsOnHTTPSDowngrade(t *testing.T) {
	data := []byte("public file")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRedirectCredentials(t, r, false)
		if r.Referer() != "" {
			t.Error("HTTP hop received the HTTPS Referer")
		}
		_, _ = w.Write(data)
	}))
	defer plain.Close()

	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		checkRedirectCredentials(t, r, true)
		// Only the scheme changes in the URL. The test transport routes the
		// plain HTTP connection to its separate loopback fixture.
		http.Redirect(w, r, "http://"+r.Host+"/public", http.StatusFound)
	}))
	defer secure.Close()

	transport := secure.Client().Transport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, plain.Listener.Addr().String())
	}
	transport.DialTLSContext = (&tls.Dialer{Config: transport.TLSClientConfig}).DialContext

	cfg := testConfig()
	cfg.ProbeClient = &http.Client{Transport: transport}
	cfg.Headers = http.Header{
		"Cookie":        {"session=fixture-cookie"},
		"Authorization": {"Bearer fixture-token"},
		"X-Api-Key":     {"fixture-key"},
		"Referer":       {secure.URL + "/page?token=fixture-query-secret"},
	}
	dest := filepath.Join(t.TempDir(), "public.bin")
	if err := run(t, cfg, secure.URL+"/start", dest); err != nil {
		t.Fatal(err)
	}
	requireFile(t, dest, data)
}
