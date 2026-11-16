package engine

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"
)

type proxyFunc = func(*http.Request) (*url.URL, error)

func newProbeClient(proxy proxyFunc) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	transport.DisableCompression = true
	return &http.Client{Transport: transport}
}

func newSegmentClient(workers int, proxy proxyFunc) *http.Client {
	if workers < 1 {
		workers = 1
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = proxy
	transport.DialContext = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSClientConfig = &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1"},
	}
	transport.ForceAttemptHTTP2 = false
	transport.MaxConnsPerHost = workers
	transport.MaxIdleConnsPerHost = workers
	transport.DisableCompression = true
	return &http.Client{Transport: transport}
}
