package service

import (
	"context"

	"github.com/amemiya02/nimget/internal/pairing"
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
