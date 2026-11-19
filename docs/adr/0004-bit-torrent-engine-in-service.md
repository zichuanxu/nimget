# Run BitTorrent in the NimGet service process

NimGet uses a BitTorrent runner backed by one in-process client that starts on demand. This lets BitTorrent and HTTP-based downloads share the service's rate limiter and cancellation lifecycle while keeping the installer and headless release as single application binaries. The engine shares the service's process failure boundary, so dependency and cross-platform validation precede pinning an engine release.

The two Go modules pin the contributor revision from the upstream v2 empty-file fix. It includes v1.63.0 and requires Go 1.27. The versioned replacement can be removed when an upstream release includes the fix. [Upstream fix](https://github.com/anacrolix/torrent/pull/1194)
