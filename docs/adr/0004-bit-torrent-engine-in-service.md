# Run BitTorrent in the NimGet service process

NimGet uses a BitTorrent runner backed by one in-process client that starts on demand. This lets BitTorrent and HTTP-based downloads share the service's rate limiter and cancellation lifecycle while keeping the installer and headless release as single application binaries. The engine shares the service's process failure boundary, so dependency and cross-platform validation precede pinning an engine release.
