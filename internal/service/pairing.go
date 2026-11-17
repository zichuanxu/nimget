package service

import (
	"context"

	"github.com/amemiya02/nimget/internal/pairing"
	"github.com/amemiya02/nimget/internal/settings"
	"github.com/amemiya02/nimget/internal/store"
)

// Pairing administration is available to the in-process desktop, outside HTTP.
func (s *Service) ListExtensionPairings(ctx context.Context) ([]pairing.Record, error) {
	return s.store.ListExtensionPairings(ctx)
}

func (s *Service) ApproveExtensionPairing(ctx context.Context, id, code string) error {
	return s.store.ApproveExtensionPairing(ctx, id, code)
}

func (s *Service) DenyExtensionPairing(ctx context.Context, id string) error {
	return s.store.DenyExtensionPairing(ctx, id)
}

func (s *Service) RevokeExtensionPairing(ctx context.Context, id string) error {
	return s.store.RevokeExtensionPairing(ctx, id)
}

func (s *Service) DeleteExtensionPairing(ctx context.Context, id string) error {
	return s.store.DeleteExtensionPairing(ctx, id)
}

// extensionPairings approves the extension's requests itself while automatic
// connection is on. The API already admits only the NimGet extension origin,
// and a local program can use the administrator token without pairing, so the
// approval code adds no protection in that mode. A denied or revoked
// credential stays as it is; the extension must send a new request.
type extensionPairings struct {
	*store.SQLite
	settings func() settings.Settings
}

func (p extensionPairings) RequestExtensionPairing(ctx context.Context, token, browser string) (pairing.Status, error) {
	status, err := p.SQLite.RequestExtensionPairing(ctx, token, browser)
	if err != nil || status.Status != pairing.AwaitingApproval || !p.settings().Extensions.AutoConnect {
		return status, err
	}
	if err := p.ApproveExtensionPairing(ctx, status.ID, status.Code); err != nil {
		return pairing.Status{}, err
	}
	return pairing.Status{ID: status.ID, Status: pairing.Connected}, nil
}
