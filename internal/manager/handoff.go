package manager

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"time"

	"github.com/zichuanxu/nimget/internal/download"
)

// AcceptHandoff queues a browser request using the same settings, destination
// selection and notifications as a task added in the desktop app.
func (m *Manager) AcceptHandoff(ctx context.Context, pairingID, handoffID string, req download.Request) (download.HandoffReceipt, error) {
	repo, ok := m.repo.(download.HandoffRepository)
	if !ok {
		return download.HandoffReceipt{}, errors.New("download repository does not support browser handoff")
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	handoff := download.Handoff{PairingID: pairingID, ID: handoffID, Digest: sha256.Sum256(payload)}
	receipt, err := repo.FindHandoff(ctx, handoff)
	if err != nil || receipt.TaskID != "" {
		return receipt, err
	}

	// Leave time for persistence inside the browser's acknowledgement window.
	// A slow origin still gets the queue's existing URL-based name fallback.
	inspectTimeout := min(m.opts.InspectTimeout, time.Second)
	_, err = m.add(ctx, req, inspectTimeout, func(ctx context.Context, item download.Item) (bool, error) {
		var storeErr error
		receipt, storeErr = repo.CreateHandoff(ctx, handoff, item)
		return receipt.Created, storeErr
	})
	return receipt, err
}
