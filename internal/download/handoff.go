package download

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// WebsiteSession is a per-task snapshot of browser-provided request context.
// It is stored separately from user-entered headers and never exported.
type WebsiteSession struct {
	Cookie    string `json:"cookie"`
	Referer   string `json:"referer,omitempty"`
	UserAgent string `json:"userAgent,omitempty"`
	ExpiresAt int64  `json:"expiresAt"`
}

func (s *WebsiteSession) Usable(now time.Time) bool {
	return s != nil && now.Unix() < s.ExpiresAt
}

func (s *WebsiteSession) Apply(headers http.Header) {
	headers.Set("Cookie", s.Cookie)
	if s.Referer != "" {
		headers.Set("Referer", s.Referer)
	}
	if s.UserAgent != "" {
		headers.Set("User-Agent", s.UserAgent)
	}
}

var (
	ErrHandoffConflict = errors.New("handoff ID was already used for a different request")
	ErrHandoffPairing  = errors.New("an approved extension pairing is required")
)

// Handoff identifies one browser intent within its approved pairing. Digest
// binds the identity to the submitted download request, independently of edits
// or deletion in the desktop queue.
type Handoff struct {
	PairingID string
	ID        string
	Digest    [32]byte
}

type HandoffReceipt struct {
	TaskID                    string `json:"taskId"`
	WebsiteSessionPersistence string `json:"websiteSessionPersistence,omitempty"`
	Created                   bool   `json:"-"`
}

type HandoffRepository interface {
	// FindHandoff returns a receipt with an empty TaskID when the handoff is new.
	FindHandoff(context.Context, Handoff) (HandoffReceipt, error)
	// LookupHandoff reconciles a receipt when the browser no longer has its
	// original payload. An empty TaskID means no receipt is committed yet.
	LookupHandoff(ctx context.Context, pairingID, handoffID string) (HandoffReceipt, error)
	// CreateHandoff commits the identity and task together or returns a replay.
	CreateHandoff(context.Context, Handoff, Item) (HandoffReceipt, error)
}
