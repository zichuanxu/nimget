package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/filelock"
	"github.com/amemiya02/nimget/internal/hls"
)

// Runner adapts the engine to the service's download.Runner contract. Every
// download shares the base configuration's HTTP clients and connection pools.
type Runner struct {
	base Config
}

func NewRunner(cfg Config) (*Runner, error) {
	if cfg.ProbeClient == nil {
		cfg.ProbeClient = newProbeClient(cfg.Proxy)
	}
	if cfg.SegmentClient == nil {
		cfg.SegmentClient = newSegmentClient(2*maxConnections, cfg.Proxy)
	}
	if _, err := New(cfg); err != nil {
		return nil, err
	}
	return &Runner{base: cfg}, nil
}

func (r *Runner) Inspect(ctx context.Context, rawURL string, headers http.Header) (download.Remote, error) {
	cfg := r.base
	cfg.Headers = headers
	e, err := New(cfg)
	if err != nil {
		return download.Remote{}, err
	}
	remote, err := e.Inspect(ctx, rawURL)
	if err != nil {
		return download.Remote{}, err
	}
	return download.Remote{Filename: remote.Filename, URL: remote.URL, ContentType: remote.ContentType, Size: remote.Size, AttachmentProof: remote.AttachmentProof}, nil
}

func (r *Runner) Download(ctx context.Context, spec download.Spec, progress func(download.Progress)) error {
	cfg := r.base
	if spec.Connections > 0 {
		cfg.Connections = spec.Connections
	}
	cfg.Headers = spec.Headers
	cfg.Checksum = spec.Checksum
	if spec.ResumeKey != "" {
		cfg.ResumeKey = spec.ResumeKey
	}
	cfg.ValidateSource = spec.ValidateSource
	cfg.Limiters = spec.Limiters
	e, err := New(cfg)
	if err != nil {
		return err
	}
	err = e.Download(ctx, spec.URL, spec.Destination, func(current Progress) {
		// A failed or canceled probe has not inspected the saved bytes yet.
		if progress != nil && !(spec.ValidateSource && current == (Progress{Total: -1})) {
			progress(download.Progress{Completed: current.Completed, Total: current.Total})
		}
	})
	if errors.Is(err, ErrLocked) {
		// Windows may release a killed process's lock a moment after it exits.
		return fmt.Errorf("%w: %w", download.ErrDestinationBusy, err)
	}
	return err
}

// Discard removes the downloader's resumable partial state for destination,
// including an HLS download's segment directory.
func (r *Runner) Discard(spec download.Spec) error {
	identity := spec.ResumeKey
	if identity == "" {
		identity = r.base.ResumeKey
	}
	if identity == "" {
		identity = spec.URL
	}
	if err := hls.Discard(spec.Destination, identity); err != nil {
		return err
	}
	release, err := filelock.Acquire(spec.Destination + ".lock")
	if err != nil {
		return err
	}
	defer release()
	return removePartial(spec.Destination+".part", spec.Destination+".part.meta", identity)
}
