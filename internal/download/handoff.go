package download

import (
	"context"
	"errors"
)

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
	TaskID  string `json:"taskId"`
	Created bool   `json:"-"`
}

type HandoffRepository interface {
	// FindHandoff returns a receipt with an empty TaskID when the handoff is new.
	FindHandoff(context.Context, Handoff) (HandoffReceipt, error)
	// CreateHandoff commits the identity and task together or returns a replay.
	CreateHandoff(context.Context, Handoff, Item) (HandoffReceipt, error)
}
