// Package logging builds the structured service logger and scrubs secrets
// from values that are safe to log only in redacted form.
package logging

import (
	"io"
	"log/slog"
	"net/url"
)

// New returns a JSON logger. Callers must never pass request headers, cookies,
// or raw URLs as attributes; use RedactURL for URLs.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// RedactURL removes user info, query parameters and the fragment. Query
// parameter names cannot reliably distinguish download options from secrets.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparseable URL]"
	}
	u.User = nil
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	u.ForceQuery = false
	return u.String()
}
