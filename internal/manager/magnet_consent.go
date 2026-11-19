package manager

import (
	"context"

	"github.com/amemiya02/nimget/internal/download"
)

func magnetProxyBlocked(item download.Item, proxyEffective bool) bool {
	return proxyEffective && item.SourceKind == download.SourceMagnet && !item.DirectConnectionAllowed
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
