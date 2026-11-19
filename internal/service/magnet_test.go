package service_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/amemiya02/nimget/internal/client"
	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/netproxy"
	"github.com/amemiya02/nimget/internal/service"
)

func TestMagnetSubmissionPersistsWithoutDiscoveryUnderProxy(t *testing.T) {
	ctx := context.Background()
	cfg := handoffServiceConfig(t)
	svc := startHandoffService(t, cfg)
	c := magnetClient(t, svc, cfg.TokenPath)
	prefs, err := c.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefs.Proxy = netproxy.Config{Mode: netproxy.ModeManual, URL: "http://127.0.0.1:1"}
	if _, err := c.PutSettings(ctx, prefs); err != nil {
		t.Fatal(err)
	}

	var announces atomic.Int32
	tracker := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		announces.Add(1)
	}))
	defer tracker.Close()
	if _, err := c.Add(ctx, download.Request{URL: "magnet:?xt=urn:btih:not-a-hash"}); err == nil || !strings.Contains(err.Error(), "info hash") {
		t.Fatalf("invalid magnet did not explain the info hash: %v", err)
	}

	const hex = "0123456789abcdef0123456789abcdef01234567"
	const base32 = "AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH"
	suffix := "&tr=" + url.QueryEscape(tracker.URL+"/announce?passkey=secret-value")
	first, err := c.Add(ctx, download.Request{URL: "magnet:?xt=urn:btih:" + hex + "&dn=provisional" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Add(ctx, download.Request{URL: "magnet:?xt=urn:btih:" + base32 + "&dn=other" + suffix})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || first.Status != download.StatusQueued || strings.Contains(first.URL, "secret-value") {
		t.Fatalf("expected one queued magnet with a redacted URL, got %+v and %+v", first, second)
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := startHandoffService(t, cfg)
	c = magnetClient(t, restarted, cfg.TokenPath)
	items, err := c.List(ctx)
	if err != nil || len(items) != 1 || items[0].ID != first.ID || items[0].Status != download.StatusQueued {
		t.Fatalf("magnet missing after restart: %+v, %v", items, err)
	}
	time.Sleep(100 * time.Millisecond)
	if n := announces.Load(); n != 0 {
		t.Fatalf("blocked magnet made %d tracker requests", n)
	}
}

func magnetClient(t *testing.T, svc *service.Service, tokenPath string) *client.Client {
	t.Helper()
	token, err := service.ReadToken(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	return client.New("http://"+svc.Address(), token, nil)
}

func TestServiceShutdownCancelsTrackerDiscovery(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	started, disconnected := make(chan struct{}), make(chan struct{})
	tracker := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("event") != "started" {
			return
		}
		close(started)
		<-r.Context().Done()
		close(disconnected)
	}))
	defer tracker.CloseClientConnections()
	defer tracker.Close()
	cfg := handoffServiceConfig(t)
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
	link := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&tr=" + url.QueryEscape(tracker.URL)
	item, err := c.Add(ctx, download.Request{URL: link})
	if err != nil || item.Status != download.StatusQueued {
		t.Fatalf("enqueue discovery: %+v, %v", item, err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("tracker did not receive a discovery request")
	}
	if err := svc.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		tracker.CloseClientConnections()
		t.Fatal("in-flight tracker request survived service shutdown")
	}
}
