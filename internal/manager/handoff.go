package manager

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/amemiya02/nimget/internal/download"
)

const websiteSessionRecovery = "Sign in to the site again and use Refresh download link in NimGet, or send the link with the browser's website session."

// AcceptHandoff queues a browser request using the same settings, destination
// selection and notifications as a task added in the desktop app.
func (m *Manager) AcceptHandoff(ctx context.Context, pairingID, handoffID, pairingCredential string, req download.Request, captureProof string) (download.HandoffReceipt, error) {
	repo, ok := m.repo.(download.HandoffRepository)
	if !ok {
		return download.HandoffReceipt{}, errors.New("download repository does not support browser handoff")
	}
	digest, err := handoffDigest(req, pairingCredential, captureProof)
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	handoff := download.Handoff{PairingID: pairingID, ID: handoffID, Digest: digest}
	receipt, err := repo.FindHandoff(ctx, handoff)
	if err != nil || receipt.TaskID != "" {
		return receipt, err
	}

	// Leave time for persistence inside the browser's acknowledgement window.
	// A slow origin still gets the queue's existing URL-based name fallback.
	inspectTimeout := min(m.opts.InspectTimeout, time.Second)
	_, err = m.add(ctx, req, inspectTimeout, captureProof, func(ctx context.Context, item download.Item) (bool, error) {
		var storeErr error
		receipt, storeErr = repo.CreateHandoff(ctx, handoff, item)
		return receipt.Created, storeErr
	})
	return receipt, err
}

func (m *Manager) LookupHandoff(ctx context.Context, pairingID, handoffID string) (download.HandoffReceipt, error) {
	repo, ok := m.repo.(download.HandoffRepository)
	if !ok {
		return download.HandoffReceipt{}, errors.New("download repository does not support browser handoff")
	}
	return repo.LookupHandoff(ctx, pairingID, handoffID)
}

func handoffDigest(req download.Request, credential, captureProof string) ([32]byte, error) {
	// Keep the digest for existing URL-only receipts stable. Bind the typed
	// session as well for new protected requests, using a keyed digest so
	// a copy of the database cannot be used to guess a cookie offline.
	if req.WebsiteSession == nil && captureProof == "" {
		payload, err := json.Marshal(req)
		return sha256.Sum256(payload), err
	}
	payload, err := json.Marshal(struct {
		download.Request
		Session      *download.WebsiteSession `json:"websiteSession"`
		CaptureProof string                   `json:"captureProof,omitempty"`
	}{req, req.WebsiteSession, captureProof})
	if err != nil {
		return [32]byte{}, err
	}
	hash := hmac.New(sha256.New, []byte(credential))
	_, _ = hash.Write(payload)
	return [32]byte(hash.Sum(nil)), nil
}
