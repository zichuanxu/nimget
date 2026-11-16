// Package pairing describes extension authorization independently of website sessions.
package pairing

import "errors"

type State string

const (
	Disconnected     State = "disconnected"
	AwaitingApproval State = "awaiting-approval"
	Connected        State = "connected"
	Denied           State = "denied"
	Revoked          State = "revoked"
)

var (
	ErrCredential   = errors.New("a valid extension credential is required")
	ErrBrowser      = errors.New("browser must be chrome or edge")
	ErrPendingLimit = errors.New("too many pending pairing requests")
	ErrApproval     = errors.New("the code is incorrect or this pairing request is no longer pending")
	ErrTransition   = errors.New("this pairing is no longer in the expected state")
)

// Record is visible to the desktop. It excludes credentials and approval codes.
type Record struct {
	ID      string `json:"id"`
	Browser string `json:"browser"`
	Status  State  `json:"status"`
}

// Status is returned only to the requesting extension installation.
type Status struct {
	ID     string `json:"id,omitempty"`
	Status State  `json:"status"`
	Code   string `json:"code,omitempty"`
}
