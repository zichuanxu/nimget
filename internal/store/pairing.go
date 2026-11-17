package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/amemiya02/nimget/internal/pairing"
)

const (
	pairingLifetime    = 10 * time.Minute
	maxPendingPairings = 16
)

func (s *SQLite) RequestExtensionPairing(ctx context.Context, token, browser string) (pairing.Status, error) {
	digest, code, err := pairingCredential(token)
	if err != nil {
		return pairing.Status{}, err
	}
	if browser != "chrome" && browser != "edge" {
		return pairing.Status{}, pairing.ErrBrowser
	}
	now := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return pairing.Status{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_pairings WHERE status = ? AND expires_at <= ?`,
		pairing.AwaitingApproval, now.Unix()); err != nil {
		return pairing.Status{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extension_pairings (id, token_digest, browser, status, code, requested_at, expires_at)
SELECT ?, ?, ?, ?, ?, ?, ?
WHERE (SELECT count(*) FROM extension_pairings WHERE status = ?) < ?
ON CONFLICT(token_digest) DO NOTHING`,
		rand.Text(), digest[:], browser, pairing.AwaitingApproval, code, now.Unix(), now.Add(pairingLifetime).Unix(),
		pairing.AwaitingApproval, maxPendingPairings); err != nil {
		return pairing.Status{}, err
	}
	status, err := readExtensionPairing(tx.QueryRowContext(ctx, lookupPairing, digest[:], now.Unix()))
	if err != nil {
		return pairing.Status{}, err
	}
	if status.Status == pairing.Disconnected {
		return pairing.Status{}, pairing.ErrPendingLimit
	}
	if err := tx.Commit(); err != nil {
		return pairing.Status{}, err
	}
	return status, nil
}

const lookupPairing = `SELECT id, status, code FROM extension_pairings
WHERE token_digest = ? AND (status <> 'awaiting-approval' OR expires_at > ?)`

func (s *SQLite) ExtensionPairingStatus(ctx context.Context, token string) (pairing.Status, error) {
	digest, _, err := pairingCredential(token)
	if err != nil {
		return pairing.Status{}, err
	}
	return readExtensionPairing(s.db.QueryRowContext(ctx, lookupPairing, digest[:], time.Now().Unix()))
}

func readExtensionPairing(row *sql.Row) (pairing.Status, error) {
	var status pairing.Status
	err := row.Scan(&status.ID, &status.Status, &status.Code)
	if errors.Is(err, sql.ErrNoRows) {
		return pairing.Status{Status: pairing.Disconnected}, nil
	}
	return status, err
}

// The random 256-bit token has enough entropy for a one-way verifier. The code
// is independent of that verifier and is discarded when the desktop responds.
func pairingCredential(token string) ([32]byte, string, error) {
	if len(token) != 64 || strings.ToLower(token) != token {
		return [32]byte{}, "", pairing.ErrCredential
	}
	raw, err := hex.DecodeString(token)
	if err != nil {
		return [32]byte{}, "", pairing.ErrCredential
	}
	code := sha256.Sum256([]byte("nimget pairing confirmation v1:" + token))
	return sha256.Sum256(raw), strings.ToUpper(hex.EncodeToString(code[:4])), nil
}

func (s *SQLite) ListExtensionPairings(ctx context.Context) ([]pairing.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, browser, status FROM extension_pairings
WHERE status <> 'awaiting-approval' OR expires_at > ?
ORDER BY CASE status WHEN 'awaiting-approval' THEN 0 WHEN 'connected' THEN 1 ELSE 2 END,
requested_at DESC, id`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]pairing.Record, 0)
	for rows.Next() {
		var record pairing.Record
		if err := rows.Scan(&record.ID, &record.Browser, &record.Status); err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *SQLite) ApproveExtensionPairing(ctx context.Context, id, code string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE extension_pairings SET status = ?, code = ''
WHERE id = ? AND status = ? AND expires_at > ? AND code = ?`,
		pairing.Connected, id, pairing.AwaitingApproval, time.Now().Unix(), strings.ToUpper(strings.TrimSpace(code)))
	return pairingChanged(result, err, pairing.ErrApproval)
}

func (s *SQLite) DenyExtensionPairing(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE extension_pairings SET status = ?, code = ''
WHERE id = ? AND (status = ? OR (status = ? AND expires_at > ?))`,
		pairing.Denied, id, pairing.Denied, pairing.AwaitingApproval, time.Now().Unix())
	return pairingChanged(result, err, pairing.ErrTransition)
}

func (s *SQLite) RevokeExtensionPairing(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE extension_pairings SET status = ?, code = ''
WHERE id = ? AND status IN (?, ?)`, pairing.Revoked, id, pairing.Connected, pairing.Revoked)
	return pairingChanged(result, err, pairing.ErrTransition)
}

// DeleteExtensionPairing forgets a denied or revoked pairing and its handoff
// receipts. Its credential can no longer authenticate, so no retry needs them.
func (s *SQLite) DeleteExtensionPairing(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status pairing.State
	err = tx.QueryRowContext(ctx, `SELECT status FROM extension_pairings WHERE id = ?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != pairing.Denied && status != pairing.Revoked) {
		return pairing.ErrTransition
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_handoffs WHERE pairing_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM extension_pairings WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func pairingChanged(result sql.Result, err, invalid error) error {
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 0 {
		return invalid
	}
	return err
}
