package magnet

import (
	"context"
	"fmt"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/anacrolix/torrent"
	"golang.org/x/time/rate"
)

const progressInterval = 250 * time.Millisecond

func (r *Runner) waitForMetadata(ctx context.Context, tor *torrent.Torrent) error {
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()
	watch := r.newStallWatch()
	// Anacrolix counts metadata chunks even when a later hash check rejects
	// them. Bound the attempt independently of that cumulative counter.
	deadline := watch.lastProgress.Add(4 * r.stallTimeout)
	var throttleGrace time.Duration
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tor.GotInfo():
			return nil
		case now := <-ticker.C:
			stats := tor.Stats()
			wireBytes := stats.BytesRead.Int64()
			stalled := watch.expired(now, stats.MetadataChunksRead.Int64(), wireBytes)
			// Give a capped connection time for one maximum-sized metadata
			// exchange (16 MiB in the engine, plus protocol framing).
			const metadataWireBudget = 17 << 20
			if wireBytes <= metadataWireBudget && now.Before(watch.budgetUntil) {
				bytesPerSecond := max(float64(watch.limit.Limit()), 1)
				grace := time.Duration(metadataWireBudget / bytesPerSecond * float64(time.Second))
				throttleGrace = max(throttleGrace, grace)
			}
			if stalled || now.Sub(deadline) >= throttleGrace {
				select {
				case <-tor.GotInfo():
					return nil
				default:
					return magnetStall("while fetching torrent metadata")
				}
			}
		}
	}
}

type stallWatch struct {
	timeout       time.Duration
	limit         *rate.Limiter
	lastProgress  time.Time
	budgetUntil   time.Time
	mostProgress  int64
	lastWireBytes int64
}

func (r *Runner) newStallWatch() stallWatch {
	return stallWatch{timeout: r.stallTimeout, limit: r.downloadLimit, lastProgress: time.Now()}
}

func (w *stallWatch) expired(now time.Time, progress, wireBytes int64) bool {
	if progress > w.mostProgress {
		w.mostProgress, w.lastProgress = progress, now
	}
	if wireBytes > w.lastWireBytes {
		w.lastWireBytes = wireBytes
		// The engine counts a socket read before it waits for the shared rate
		// limiter. Give that read time to reach metadata or the piece verifier.
		if w.limit != nil {
			if perSecond, debt := w.limit.Limit(), -w.limit.TokensAt(now); perSecond > 0 && perSecond != rate.Inf && debt > 0 {
				delay := time.Duration(debt / float64(perSecond) * float64(time.Second))
				if until := now.Add(delay); until.After(w.budgetUntil) {
					w.budgetUntil = until
				}
			}
		}
	}
	return now.Sub(w.lastProgress) >= w.timeout && now.Sub(w.budgetUntil) >= w.timeout
}

func magnetStall(reason string) error {
	return fmt.Errorf("%w %s. Retry when peers are available", download.ErrStalled, reason)
}
