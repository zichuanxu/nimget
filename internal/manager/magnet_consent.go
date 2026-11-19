package manager

import "github.com/amemiya02/nimget/internal/download"

func magnetProxyBlocked(item download.Item, proxyEffective bool) bool {
	return proxyEffective && item.SourceKind == download.SourceMagnet && !item.DirectConnectionAllowed
}
