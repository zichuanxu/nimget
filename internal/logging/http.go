package logging

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
)

// HTTPError reports request failures without exposing request URLs, redirect
// targets, or other transport text that can contain credentials.
func HTTPError(err error) error {
	var request *url.Error
	if !errors.As(err, &request) {
		return err
	}

	var dns *net.DNSError
	var network *net.OpError
	var certificate *tls.CertificateVerificationError
	message := "HTTP request failed"
	switch {
	case errors.Is(request, context.Canceled):
		message = "HTTP request canceled"
	case request.Timeout():
		message = "HTTP request timed out"
	case strings.HasPrefix(request.Err.Error(), "failed to parse Location header"):
		message = "invalid HTTP redirect location"
	case request.Err.Error() == "stopped after 10 redirects":
		message = "stopped after 10 redirects"
	case errors.As(request, &certificate):
		message = "HTTP server TLS certificate verification failed"
	case errors.As(request, &dns):
		message = "HTTP server name lookup failed"
	case errors.As(request, &network):
		message = "HTTP network connection failed"
	}
	return requestError{message: message, cause: err}
}

type requestError struct {
	message string
	cause   error
}

func (e requestError) Error() string { return e.message }
func (e requestError) Unwrap() error { return e.cause }
