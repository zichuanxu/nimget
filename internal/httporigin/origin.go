// Package httporigin confines download credentials to an HTTP security origin.
package httporigin

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// StatusError carries a safe HTTP failure message and the response status.
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string { return e.Message }

// Client copies a client's configuration, preserving its transport and
// redirect policy while restricting headers on any cross-origin hop.
func Client(client *http.Client) *http.Client {
	guarded := *client
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if client.CheckRedirect != nil {
			if err := client.CheckRedirect(req, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		origin := via[0].URL
		if !same(req.URL, origin) {
			strip(req, via[len(via)-1].URL)
			return nil
		}
		for _, previous := range via[1:] {
			if !same(previous.URL, origin) {
				// Go builds each hop from the initial headers. Once the
				// chain crosses an origin, even a return to it stays stripped.
				strip(req, via[len(via)-1].URL)
				break
			}
		}
		return nil
	}
	return &guarded
}

// Restrict removes credentials from requests outside their intended origin.
func Restrict(req *http.Request, origin *url.URL) {
	if !same(req.URL, origin) {
		strip(req, origin)
	}
}

func same(a, b *url.URL) bool {
	return a != nil && b != nil &&
		strings.EqualFold(a.Scheme, b.Scheme) &&
		hostname(a) == hostname(b) &&
		effectivePort(a) == effectivePort(b)
}

// Key returns the normalized HTTP security origin of u.
func Key(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + net.JoinHostPort(hostname(u), effectivePort(u))
}

func hostname(u *url.URL) string {
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}
	return strings.ToLower(host)
}

func effectivePort(u *url.URL) string {
	switch port := u.Port(); {
	case port == "":
		if strings.EqualFold(u.Scheme, "https") {
			return "443"
		}
		return "80"
	case strings.TrimLeft(port, "0") == "":
		return "0"
	default:
		return strings.TrimLeft(port, "0")
	}
}

func strip(req *http.Request, previous *url.URL) {
	for name := range req.Header {
		switch {
		case transportHeader(name):
		case http.CanonicalHeaderKey(name) == "Referer":
			values := req.Header[name]
			if len(values) == 0 {
				delete(req.Header, name)
				continue
			}
			ref, err := url.Parse(values[0])
			if err == nil && ref.Host != "" && (ref.Scheme == "http" || ref.Scheme == "https") &&
				(req.URL.Scheme != "http" || previous == nil || previous.Scheme != "https") {
				// A referrer's path, query and user info can also contain credentials.
				req.Header[name] = []string{ref.Scheme + "://" + ref.Host + "/"}
				continue
			}
			delete(req.Header, name)
		default:
			delete(req.Header, name)
		}
	}
	req.URL.User = nil
}

// MayCarryCredentials identifies requests whose responses should never be
// quoted in diagnostics. Services can reflect custom headers or URL values.
func MayCarryCredentials(req *http.Request) bool {
	if req == nil {
		return false
	}
	if req.URL.User != nil || req.URL.RawQuery != "" {
		return true
	}
	for name := range req.Header {
		if !transportHeader(name) {
			return true
		}
	}
	return false
}

func transportHeader(name string) bool {
	switch http.CanonicalHeaderKey(name) {
	case "User-Agent", "Accept", "Accept-Language", "Accept-Encoding", "Range", "If-Range", "If-Match":
		return true
	default:
		return false
	}
}
