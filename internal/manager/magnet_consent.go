package manager

import (
	"context"

	"github.com/amemiya02/nimget/internal/download"
)

func magnetProxyBlocked(item download.Item, proxyEffective bool) bool {
	return proxyEffective && item.SourceKind == download.SourceMagnet && !item.DirectConnectionAllowed
}

func queuedMagnetPhase(item download.Item, proxyEffective bool) download.Phase {
	if item.SourceKind != download.SourceMagnet || item.Status != download.StatusQueued {
		return item.Phase
	}
	if magnetProxyBlocked(item, proxyEffective) && !hasMagnetPublication(item) {
		return download.PhaseProxyBlocked
	}
	return ""
}

func waitForCheckpoints(ctx context.Context, checkpoints ...<-chan struct{}) error {
	for _, done := range checkpoints {
		select {
		case <-done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}
