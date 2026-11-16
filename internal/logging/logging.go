// Package logging builds the structured service logger and sanitizes network errors.
package logging

import (
	"io"
	"log/slog"
)

// New returns a JSON logger. Callers must never pass request headers, cookies,
// URLs, or origin response bodies as attributes.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
