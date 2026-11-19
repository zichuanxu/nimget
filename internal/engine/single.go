package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/amemiya02/nimget/internal/logging"
)

type initialResponse struct {
	resp   *http.Response
	ctx    context.Context
	cancel context.CancelCauseFunc
}

func (r *initialResponse) close() {
	if r != nil && r.resp != nil {
		r.cancel(nil)
		_ = r.resp.Body.Close()
		r.resp = nil
	}
}

// single downloads over one stream, using the probe body when the server
// ignored Range. Each attempt restarts from zero.
func (j *job) single(ctx context.Context, info probeInfo) error {
	e := j.engine
	if err := j.discard(); err != nil {
		return err
	}
	if info.size > 0 {
		if free, err := freeSpace(filepath.Dir(j.part)); err == nil && free < info.size+freeSpaceMargin {
			return fmt.Errorf("%w: need %d bytes plus margin, %d available", ErrInsufficientSpace, info.size, free)
		}
	}
	f, _, err := j.createPartial(checkpoint{Version: checkpointVersion, Identity: j.identity})
	if err != nil {
		return fmt.Errorf("create partial file: %w", err)
	}
	defer f.Close()
	if info.initial != nil {
		// A server can advertise the full size on the Range probe but leave
		// its body incomplete. A failed probe body does not consume any of
		// the ordinary GET attempts.
		first := j.singleOnce(ctx, info, f)
		if first == nil {
			return nil
		}
		if !first.retryable {
			return first
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	var last *attemptError
	for attempt := 0; attempt < e.cfg.MaxAttempts; attempt++ {
		if last != nil {
			if err := sleepBeforeRetry(ctx, attempt-1, last.retryAfter, e.cfg.BaseBackoff, e.cfg.MaxBackoff); err != nil {
				return err
			}
		}
		last = j.singleOnce(ctx, info, f)
		if last == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !last.retryable {
			break
		}
	}
	return last
}

func (j *job) singleOnce(ctx context.Context, info probeInfo, f *os.File) *attemptError {
	e := j.engine
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fatal(fmt.Errorf("seek partial file: %w", err))
	}
	if err := f.Truncate(0); err != nil {
		return fatal(fmt.Errorf("truncate partial file: %w", err))
	}
	j.progress.completed.Store(0)
	j.progress.total.Store(info.size)
	if info.size == 0 {
		return closeSynced(f)
	}

	initial := info.initial
	var reqCtx context.Context
	var cancel context.CancelCauseFunc
	var resp *http.Response
	if initial != nil && initial.resp != nil {
		resp = initial.resp
		reqCtx, cancel = initial.ctx, initial.cancel
		defer initial.close()
	} else {
		reqCtx, cancel = context.WithCancelCause(ctx)
		defer cancel(nil)
	}
	stall := newStallTimer(e.cfg.StallTimeout, func() { cancel(ErrStalled) })
	defer stall.Stop()
	interrupted := func(err error) *attemptError {
		if errors.Is(context.Cause(reqCtx), ErrStalled) && ctx.Err() == nil {
			err = ErrStalled
		}
		return retryable(fmt.Errorf("download: %w", err))
	}

	if resp != nil {
		stall.Reset(e.cfg.StallTimeout)
	} else {
		req, err := e.newRequest(stall.trace(reqCtx), j.url)
		if err != nil {
			return fatal(err)
		}
		if info.etag != "" {
			req.Header.Set("If-Match", info.etag)
		}
		resp, err = e.cfg.ProbeClient.Do(req)
		if err != nil {
			return interrupted(logging.HTTPError(err))
		}
		defer resp.Body.Close()
	}
	if err := identityEncoding(resp); err != nil {
		return fatal(err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusPreconditionFailed:
		return fatal(ErrSourceChanged)
	default:
		return statusError("download", resp)
	}
	if info.etag != "" && strongETag(resp.Header.Get("ETag")) != info.etag {
		return fatal(ErrSourceChanged)
	}
	total := resp.ContentLength
	j.progress.total.Store(total)

	buf := make([]byte, bufferSize)
	var written int64
	for {
		n, readErr := resp.Body.Read(buf[:e.limit(bufferSize)])
		if n > 0 {
			stall.Stop()
			if err := e.wait(ctx, n); err != nil {
				return interrupted(err)
			}
			if _, err := f.Write(buf[:n]); err != nil {
				return fatal(fmt.Errorf("write partial file: %w", err))
			}
			stall.Reset(e.cfg.StallTimeout)
			written += int64(n)
			j.progress.completed.Store(written)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return interrupted(readErr)
		}
	}
	if total >= 0 && written != total {
		return retryable(fmt.Errorf("short response: got %d bytes, want %d", written, total))
	}
	j.progress.total.Store(written)
	return closeSynced(f)
}

func closeSynced(f *os.File) *attemptError {
	if err := f.Sync(); err != nil {
		return fatal(fmt.Errorf("sync partial file: %w", err))
	}
	if err := f.Close(); err != nil {
		return fatal(fmt.Errorf("close partial file: %w", err))
	}
	return nil
}
