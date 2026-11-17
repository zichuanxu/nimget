package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/pairing"
)

const findHandoff = `SELECT p.status, h.request_digest, COALESCE(h.task_id, ''), COALESCE(h.website_session_persistence, '')
FROM extension_pairings p LEFT JOIN extension_handoffs h ON h.pairing_id = p.id AND h.handoff_id = ?
WHERE p.id = ?`

func (s *SQLite) FindHandoff(ctx context.Context, handoff download.Handoff) (download.HandoffReceipt, error) {
	return readHandoff(s.db.QueryRowContext(ctx, findHandoff, handoff.ID, handoff.PairingID), &handoff.Digest)
}

func (s *SQLite) LookupHandoff(ctx context.Context, pairingID, handoffID string) (download.HandoffReceipt, error) {
	return readHandoff(s.db.QueryRowContext(ctx, findHandoff, handoffID, pairingID), nil)
}

func readHandoff(row *sql.Row, expectedDigest *[32]byte) (download.HandoffReceipt, error) {
	var receipt download.HandoffReceipt
	var state pairing.State
	var digest []byte
	err := row.Scan(&state, &digest, &receipt.TaskID, &receipt.WebsiteSessionPersistence)
	if errors.Is(err, sql.ErrNoRows) {
		return download.HandoffReceipt{}, download.ErrHandoffPairing
	}
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	if state != pairing.Connected {
		return download.HandoffReceipt{}, download.ErrHandoffPairing
	}
	if receipt.TaskID != "" && expectedDigest != nil && !bytes.Equal(digest, expectedDigest[:]) {
		return download.HandoffReceipt{}, download.ErrHandoffConflict
	}
	return receipt, nil
}

func (s *SQLite) CreateHandoff(ctx context.Context, handoff download.Handoff, item download.Item) (download.HandoffReceipt, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	defer tx.Rollback()
	// Claim the identity only while the pairing remains approved. The unique
	// key also covers concurrent deliveries that missed the earlier lookup.
	persistence := ""
	if item.WebsiteSession != nil {
		persistence = "secure"
		if s.sealer == nil || s.sealer.KeyringErr != nil {
			persistence = "memory-only"
		}
	}
	const claim = `INSERT INTO extension_handoffs (pairing_id, handoff_id, request_digest, task_id, website_session_persistence)
SELECT id, ?, ?, ?, ? FROM extension_pairings WHERE id = ? AND status = ?
ON CONFLICT(pairing_id, handoff_id) DO NOTHING`
	result, err := tx.ExecContext(ctx, claim, handoff.ID, handoff.Digest[:], item.ID, persistence, handoff.PairingID, pairing.Connected)
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	claimed, err := result.RowsAffected()
	if err != nil {
		return download.HandoffReceipt{}, err
	}
	if claimed == 0 {
		return readHandoff(tx.QueryRowContext(ctx, findHandoff, handoff.ID, handoff.PairingID), &handoff.Digest)
	}
	if err := s.create(ctx, tx, item); err != nil {
		return download.HandoffReceipt{}, err
	}
	if err := tx.Commit(); err != nil {
		return download.HandoffReceipt{}, err
	}
	return download.HandoffReceipt{TaskID: item.ID, WebsiteSessionPersistence: persistence, Created: true}, nil
}
