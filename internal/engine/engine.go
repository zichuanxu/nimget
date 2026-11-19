// Package engine downloads HTTP resources with dynamic work-stealing
// segmentation and byte-granular resume, as specified in DESIGN.md section 3.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/amemiya02/nimget/internal/download"
	"github.com/amemiya02/nimget/internal/filelock"
	"github.com/amemiya02/nimget/internal/hls"
	"github.com/amemiya02/nimget/internal/httporigin"
	"github.com/amemiya02/nimget/internal/logging"
	"github.com/amemiya02/nimget/internal/partialfile"
	"github.com/amemiya02/nimget/internal/quarantine"
	"golang.org/x/time/rate"
)

var (
	ErrDestinationExists = errors.New("destination already exists")
	ErrSourceChanged     = errors.New("remote resource changed while downloading")
	ErrRangeUnsupported  = errors.New("remote server stopped honoring range requests")
	ErrLocked            = errors.New("destination is locked by another download")
	ErrStalled           = download.ErrStalled
	ErrInsufficientSpace = download.ErrDiskSpace
)

const (
	maxConnections = 32
	bufferSize     = 256 << 10
)

type Config struct {
	// Connections per download: default 8, at most 32.
	Connections int
	// MinSplitSize is the smallest range handed to a connection: default
	// 1 MiB, at least 256 KiB so a split never cuts through a buffered write.
	MinSplitSize int64
	// MaxAttempts bounds consecutive failed requests without progress.
	MaxAttempts int
	BaseBackoff time.Duration
	MaxBackoff  time.Duration
	// StallTimeout fails a request that receives no bytes for this long.
	// Time spent blocked outside the network read never counts.
	StallTimeout       time.Duration
	CheckpointInterval time.Duration
	ProgressInterval   time.Duration

	Headers   http.Header
	UserAgent string
	// Checksum is an optional "algo:hex" digest verified before commit.
	Checksum string
	// ResumeKey identifies the file across expiring signed URLs; the URL is
	// used when it is empty.
	ResumeKey string
	// ValidateSource prohibits replacing partial data after the source has changed.
	ValidateSource bool
	Overwrite      bool
	// FileMode is applied after the complete file has been verified.
	FileMode os.FileMode
	// Limiters cap body reads (DESIGN.md 3.9); every one must admit the bytes.
	// Time spent waiting on them never counts toward the stall timeout.
	Limiters []*rate.Limiter
	// Proxy selects the proxy for the default clients; nil connects directly.
	Proxy func(*http.Request) (*url.URL, error)

	// ProbeClient negotiates protocols normally; SegmentClient forces
	// HTTP/1.1 so every connection gets its own TCP congestion window.
	ProbeClient   *http.Client
	SegmentClient *http.Client
}

type Progress struct {
	Completed int64
	Total     int64 // -1 when unknown
}

type Engine struct {
	cfg Config
	sum *checksum
}

func New(cfg Config) (*Engine, error) {
	defaults := []struct {
		value *time.Duration
		def   time.Duration
		name  string
	}{
		{&cfg.BaseBackoff, 300 * time.Millisecond, "base backoff"},
		{&cfg.MaxBackoff, 10 * time.Second, "max backoff"},
		{&cfg.StallTimeout, 30 * time.Second, "stall timeout"},
		{&cfg.CheckpointInterval, 2 * time.Second, "checkpoint interval"},
		{&cfg.ProgressInterval, 250 * time.Millisecond, "progress interval"},
	}
	for _, d := range defaults {
		if *d.value == 0 {
			*d.value = d.def
		}
		if *d.value < 0 {
			return nil, fmt.Errorf("%s cannot be negative", d.name)
		}
	}
	if cfg.MaxBackoff < cfg.BaseBackoff {
		return nil, errors.New("max backoff is below base backoff")
	}
	if cfg.Connections == 0 {
		cfg.Connections = 8
	}
	if cfg.Connections < 1 || cfg.Connections > maxConnections {
		return nil, fmt.Errorf("connections must be in [1, %d], got %d", maxConnections, cfg.Connections)
	}
	if cfg.MinSplitSize == 0 {
		cfg.MinSplitSize = 1 << 20
	}
	if cfg.MinSplitSize < bufferSize {
		return nil, fmt.Errorf("minimum split size must be at least %d bytes", bufferSize)
	}
	if cfg.MaxAttempts == 0 {
		cfg.MaxAttempts = 5
	}
	if cfg.MaxAttempts < 1 {
		return nil, errors.New("max attempts must be at least 1")
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "nimget/1.0"
	}
	if cfg.FileMode == 0 {
		cfg.FileMode = 0o644
	}
	cfg.Headers = cfg.Headers.Clone()
	if cfg.Headers == nil {
		cfg.Headers = make(http.Header)
	}
	if cfg.ProbeClient == nil {
		cfg.ProbeClient = newProbeClient(cfg.Proxy)
	}
	if cfg.SegmentClient == nil {
		// The manager enforces per-host caps; this only bounds the pool.
		cfg.SegmentClient = newSegmentClient(2*maxConnections, cfg.Proxy)
	}
	probe := cfg.ProbeClient
	cfg.ProbeClient = httporigin.Client(probe)
	if cfg.SegmentClient == probe {
		cfg.SegmentClient = cfg.ProbeClient
	} else {
		cfg.SegmentClient = httporigin.Client(cfg.SegmentClient)
	}
	sum, err := parseChecksum(cfg.Checksum)
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, sum: sum}, nil
}

// Download writes dest+".part", keeps resume state in dest+".part.meta", and
// atomically renames the verified file to dest. Partial state survives
// failures and cancellation so a later call resumes. An HLS playlist, known
// by its URL or Content-Type, is downloaded as one stream (DESIGN.md 3.7).
func (e *Engine) Download(ctx context.Context, rawURL, dest string, onProgress func(Progress)) error {
	if ctx == nil {
		return errors.New("nil context")
	}
	if err := validateHTTPURL(rawURL); err != nil {
		return err
	}
	if strings.TrimSpace(dest) == "" {
		return errors.New("empty destination path")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	var err error
	if hls.Detect("", rawURL, nil) {
		err = e.downloadHLS(ctx, rawURL, dest, onProgress)
	} else if err = e.downloadFile(ctx, rawURL, dest, onProgress); errors.Is(err, errPlaylist) {
		err = e.downloadHLS(ctx, rawURL, dest, onProgress)
	}
	if errors.Is(err, partialfile.ErrExists) {
		return download.ErrRestartRequired
	}
	if err == nil {
		// Best effort: some file systems (FAT, network shares) refuse it.
		_ = quarantine.Mark(dest, rawURL)
	}
	return err
}

func (e *Engine) downloadFile(ctx context.Context, rawURL, dest string, onProgress func(Progress)) error {
	release, err := filelock.Acquire(dest + ".lock")
	if errors.Is(err, filelock.ErrLocked) {
		return ErrLocked
	}
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	if !e.cfg.Overwrite {
		if _, err := os.Lstat(dest); err == nil {
			return ErrDestinationExists
		}
	}

	job := &job{
		engine: e, url: rawURL, dest: dest,
		part: dest + ".part", meta: dest + ".part.meta",
		identity: e.cfg.ResumeKey,
		progress: startProgress(onProgress, e.cfg.ProgressInterval),
		segments: e.cfg.SegmentClient, connections: e.cfg.Connections,
	}
	if job.identity == "" {
		job.identity = rawURL
	}
	defer job.progress.finish()

	// One automatic restart covers a representation change between the probe
	// and a later request; one more covers a server that refuses HTTP/1.1.
	for attempt := 0; attempt < 3; attempt++ {
		info, err := e.probe(ctx, rawURL, true)
		if err != nil {
			return err
		}
		switch err := job.transfer(ctx, info); {
		case err == nil:
			return job.commit()
		case errors.Is(err, errNeedsHTTP2) && job.segments != e.cfg.ProbeClient:
			job.segments, job.connections = e.cfg.ProbeClient, 1
		case e.cfg.ValidateSource && (errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrRangeUnsupported)):
			return download.ErrRestartRequired
		case attempt == 0 && (errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrRangeUnsupported)):
			// Re-probe. Written bytes came from validated responses, so openPart
			// keeps them when the validators still match and discards them
			// otherwise.
		default:
			return err
		}
	}
	return ErrSourceChanged
}

// job is the state of one Download call.
type job struct {
	engine      *Engine
	url, dest   string
	part, meta  string
	identity    string
	progress    *progressReporter
	segments    *http.Client
	connections int
}

func (j *job) transfer(ctx context.Context, info probeInfo) error {
	defer info.initial.close()
	if isDASHManifest(info.contentType, info.finalURL) {
		return fmt.Errorf("%w: DASH manifests are unsupported. %s", download.ErrUnsupportedMedia, supportedMediaSource)
	}
	// The final URL catches a redirect to an .m3u8 served as a generic type.
	if hls.Detect(info.contentType, info.finalURL, nil) {
		return errPlaylist // handled after the lock is released
	}
	if j.engine.cfg.ValidateSource {
		paths := []string{j.dest + ".hls"}
		if !info.ranges || info.size <= 0 {
			paths = append(paths, j.part, j.meta)
		}
		if err := partialfile.RequireAbsent(paths...); err != nil {
			return err
		}
	}
	if info.ranges && info.size > 0 {
		return j.ranged(ctx, info)
	}
	return j.single(ctx, info)
}

func (j *job) discard() error {
	return removePartial(j.part, j.meta, j.identity)
}

// loadPartial validates ownership separately from remote byte continuity.
func loadPartial(part, meta, identity, legacyURL string) (*os.File, checkpoint, error) {
	f, err := partialfile.Open(part, os.O_RDWR, 0)
	if err != nil {
		return nil, checkpoint{}, err
	}
	cp, readErr := readCheckpoint(meta)
	if readErr == nil && identity != "" && (cp.Identity == identity ||
		(cp.FileID == "" && cp.Identity == legacyURL)) {
		if cp.FileID != "" {
			if fileID, err := partialfile.Identity(f); err == nil && cp.FileID == fileID {
				return f, cp, nil
			}
		} else if info, err := f.Stat(); err == nil && cp.Size == info.Size() {
			// An older sidecar may guide read-only comparison. It cannot
			// authorize writing or discarding the current file.
			return f, cp, nil
		}
	}
	_ = f.Close()
	return nil, checkpoint{}, partialfile.Collision("an existing file has no matching download state")
}

func removePartial(part, meta, identity string) error {
	f, cp, err := loadPartial(part, meta, identity, "")
	if os.IsNotExist(err) {
		return partialfile.RejectExisting(meta)
	}
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if cp.FileID == "" {
		return partialfile.Collision("legacy partial ownership could not be verified")
	}
	if err := os.Remove(part); err != nil {
		return err
	}
	return os.Remove(meta)
}

func (j *job) createPartial(state checkpoint) (*os.File, string, error) {
	if err := partialfile.RejectExisting(j.meta); err != nil {
		return nil, "", err
	}
	f, err := partialfile.Open(j.part, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	state.FileID, err = partialfile.Identity(f)
	if err == nil {
		err = createCheckpoint(j.meta, state)
	}
	if err != nil {
		_ = f.Close()
		_ = os.Remove(j.part)
		return nil, "", err
	}
	return f, state.FileID, nil
}

// commit verifies the closed .part file and renames it into place.
func (j *job) commit() error {
	f, _, err := loadPartial(j.part, j.meta, j.identity, j.url)
	if err != nil {
		return fmt.Errorf("open completed partial file: %w", err)
	}
	if err := j.engine.sum.verify(f); err != nil {
		_ = f.Close()
		if errors.Is(err, ErrChecksumMismatch) {
			_ = j.discard()
		}
		return err
	}
	if err := errors.Join(f.Chmod(j.engine.cfg.FileMode), f.Close()); err != nil {
		return fmt.Errorf("finalize completed file: %w", err)
	}
	if err := j.place(); err != nil {
		return err
	}
	syncDir(filepath.Dir(j.dest))
	_ = os.Remove(j.meta) // the destination is committed; stale metadata is harmless
	return nil
}

// place moves the completed file to dest. Without Overwrite it hard-links,
// which fails atomically if dest appeared meanwhile; file systems without
// hard links (FAT, exFAT) fall back to check-then-rename.
func (j *job) place() error {
	if !j.engine.cfg.Overwrite {
		err := os.Link(j.part, j.dest)
		if err == nil {
			_ = os.Remove(j.part)
			return nil
		}
		if errors.Is(err, fs.ErrExist) {
			return ErrDestinationExists
		}
		if _, err := os.Lstat(j.dest); err == nil {
			return ErrDestinationExists
		}
	}
	if err := os.Rename(j.part, j.dest); err != nil {
		return fmt.Errorf("rename completed file: %w", err)
	}
	return nil
}

type probeInfo struct {
	size         int64 // -1 when unknown
	ranges       bool
	etag         string // strong ETag only
	lastModified string
	sourceScope  string
	sourcePath   string
	initial      *initialResponse // complete response to a Range probe, when retained for downloading
	// For Inspect.
	disposition, contentType, finalURL string
}

func isDASHManifest(contentType, rawURL string) bool {
	mediaType, _, _ := mime.ParseMediaType(contentType)
	if mediaType == "application/dash+xml" {
		return true
	}
	u, err := url.Parse(rawURL)
	return err == nil && strings.EqualFold(filepath.Ext(u.Path), ".mpd")
}

func (e *Engine) probe(ctx context.Context, rawURL string, keepFullResponse bool) (probeInfo, error) {
	var lastErr error
	for attempt := 0; attempt < e.cfg.MaxAttempts; attempt++ {
		if attempt > 0 {
			var retryAfter string
			var aerr *attemptError
			if errors.As(lastErr, &aerr) {
				retryAfter = aerr.retryAfter
			}
			if err := sleepBeforeRetry(ctx, attempt-1, retryAfter, e.cfg.BaseBackoff, e.cfg.MaxBackoff); err != nil {
				return probeInfo{}, err
			}
		}
		info, aerr := e.probeOnce(ctx, rawURL, keepFullResponse)
		if aerr == nil {
			return info, nil
		}
		if ctx.Err() != nil {
			return probeInfo{}, ctx.Err()
		}
		lastErr = aerr
		if !aerr.retryable {
			break
		}
	}
	return probeInfo{}, lastErr
}

func (e *Engine) probeOnce(ctx context.Context, rawURL string, keepFullResponse bool) (probeInfo, *attemptError) {
	reqCtx, cancel := context.WithCancelCause(ctx)
	// Limit the probe itself. A retained body gets its own idle timer once
	// the downloader is ready to read it.
	timer := time.AfterFunc(e.cfg.StallTimeout, func() { cancel(context.DeadlineExceeded) })
	var retained bool
	defer func() {
		timer.Stop()
		if !retained {
			cancel(nil)
		}
	}()
	req, err := e.newRequest(reqCtx, rawURL)
	if err != nil {
		return probeInfo{}, fatal(err)
	}
	req.Header.Set("Range", "bytes=0-0")
	resp, err := e.cfg.ProbeClient.Do(req)
	if err != nil {
		return probeInfo{}, retryable(fmt.Errorf("probe request: %w", logging.HTTPError(err)))
	}
	defer func() {
		if !retained {
			_ = resp.Body.Close()
		}
	}()
	if err := identityEncoding(resp); err != nil {
		return probeInfo{}, fatal(err)
	}
	info := probeInfo{
		etag: strongETag(resp.Header.Get("ETag")), lastModified: resp.Header.Get("Last-Modified"),
		disposition: resp.Header.Get("Content-Disposition"), contentType: resp.Header.Get("Content-Type"),
		finalURL: resp.Request.URL.String(), sourceScope: sourceScope(resp.Request.URL),
		sourcePath: sourcePath(resp.Request.URL),
	}
	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, end, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != 0 || end != 0 {
			return probeInfo{}, fatal(errors.New("invalid probe Content-Range"))
		}
		if _, err := io.CopyN(io.Discard, resp.Body, 1); err != nil {
			return probeInfo{}, retryable(fmt.Errorf("read probe body: %w", err))
		}
		info.size, info.ranges = total, true
		return info, nil
	case http.StatusOK:
		info.size = resp.ContentLength
		if keepFullResponse && info.size != 0 && resp.Header.Get("Content-Range") == "" {
			if !timer.Stop() {
				return probeInfo{}, retryable(fmt.Errorf("probe response: %w", context.DeadlineExceeded))
			}
			info.initial = &initialResponse{resp: resp, ctx: reqCtx, cancel: cancel}
			retained = true
		}
		return info, nil
	case http.StatusRequestedRangeNotSatisfiable:
		if total, ok := parseUnsatisfiedContentRange(resp.Header.Get("Content-Range")); ok && total == 0 {
			info.size = 0
			return info, nil
		}
		return probeInfo{}, fatal(errors.New("probe returned 416 with invalid Content-Range"))
	default:
		return probeInfo{}, statusError("probe", resp)
	}
}

// sourceScope limits validator comparisons to the final HTTP resource.
// Hash the full request URI so signed values stay out of the checkpoint.
func sourceScope(u *url.URL) string {
	sum := sha256.Sum256([]byte(httporigin.Key(u) + u.RequestURI()))
	return hex.EncodeToString(sum[:])
}

// sourcePath is the final resource without its query. Signed download URLs
// keep the path and change the query each time they are issued.
func sourcePath(u *url.URL) string {
	return httporigin.Key(u) + u.EscapedPath()
}

// newRequest builds a GET carrying the caller's headers, minus the ones the
// engine owns: letting callers inject them would break range validation.
func (e *Engine) newRequest(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header = e.cfg.Headers.Clone()
	for _, name := range []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"} {
		req.Header.Del(name)
	}
	req.Header.Set("Accept-Encoding", "identity")
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", e.cfg.UserAgent)
	}
	return req, nil
}

func validateHTTPURL(raw string) error {
	u, err := url.ParseRequestURI(raw)
	if err != nil {
		return errors.New("invalid download URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("URL has no host")
	}
	return nil
}

func identityEncoding(resp *http.Response) error {
	if enc := strings.TrimSpace(resp.Header.Get("Content-Encoding")); enc != "" && !strings.EqualFold(enc, "identity") {
		return errors.New("server returned unsupported Content-Encoding")
	}
	return nil
}

func statusError(what string, resp *http.Response) *attemptError {
	msg := fmt.Sprintf("%s: HTTP %d", what, resp.StatusCode)
	if reason := http.StatusText(resp.StatusCode); reason != "" {
		msg += " " + reason
	}
	return &attemptError{
		err:        &httporigin.StatusError{Code: resp.StatusCode, Diagnostic: msg},
		retryable:  retryableStatus(resp.StatusCode),
		retryAfter: resp.Header.Get("Retry-After"),
	}
}

// Remote describes a resource as the capability probe saw it.
type Remote struct {
	// Filename is the Content-Disposition file name, if the server sent one.
	Filename        string
	URL             string // after redirects
	ContentType     string
	Size            int64 // -1 when unknown
	AttachmentProof string
}

// Inspect probes rawURL without downloading it, for naming and type checks.
func (e *Engine) Inspect(ctx context.Context, rawURL string) (Remote, error) {
	if err := validateHTTPURL(rawURL); err != nil {
		return Remote{}, err
	}
	info, err := e.probe(ctx, rawURL, false)
	if err != nil {
		return Remote{}, err
	}
	return Remote{URL: info.finalURL, ContentType: info.contentType, Size: info.size, Filename: dispositionName(info.disposition), AttachmentProof: info.attachmentProof(rawURL)}, nil
}

// attachmentProof mirrors the observed Chromium response metadata. Status alone
// could represent a login page or another file. Always use the same bounded
// engine probe and request headers that a task would use.
func (info probeInfo) attachmentProof(rawURL string) string {
	original, err := url.Parse(rawURL)
	if err != nil || info.etag == "" || info.sourceScope != sourceScope(original) {
		return ""
	}
	disposition := strings.TrimSpace(info.disposition)
	kind, _, err := mime.ParseMediaType(disposition)
	if err != nil || !strings.EqualFold(kind, "attachment") ||
		hls.Detect(info.contentType, rawURL, nil) || isDASHManifest(info.contentType, rawURL) {
		return ""
	}
	contentType := strings.ToLower(strings.TrimSpace(info.contentType))
	if mediaType, _, _ := mime.ParseMediaType(contentType); mediaType == "text/html" {
		return ""
	}
	data := strings.Join([]string{disposition, contentType, strconv.FormatInt(info.size, 10), info.etag}, "\n")
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// dispositionName reads the file name of a Content-Disposition header.
// mime decodes RFC 5987 filename* into "filename"; a malformed header such as
// an unquoted name with spaces falls back to a lenient split. Callers
// sanitize the result.
func dispositionName(header string) string {
	if _, params, err := mime.ParseMediaType(header); err == nil {
		return params["filename"]
	}
	_, rest, ok := strings.Cut(header, "filename=")
	if !ok {
		return ""
	}
	rest, _, _ = strings.Cut(rest, ";")
	return strings.Trim(strings.TrimSpace(rest), `"`)
}

// limit shrinks a read to what one limiter wait can admit.
func (e *Engine) limit(n int64) int64 {
	for _, l := range e.cfg.Limiters {
		if l.Limit() != rate.Inf {
			n = min(n, int64(l.Burst()))
		}
	}
	return n
}

// wait blocks until every limiter admits n bytes.
func (e *Engine) wait(ctx context.Context, n int) error {
	for _, l := range e.cfg.Limiters {
		if err := l.WaitN(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// limited reports whether any limiter currently caps the speed.
func (e *Engine) limited() bool {
	for _, l := range e.cfg.Limiters {
		if l.Limit() != rate.Inf {
			return true
		}
	}
	return false
}

// progressReporter publishes byte counts at most once per interval from its
// own goroutine, so the download path only touches atomics.
type progressReporter struct {
	completed atomic.Int64
	total     atomic.Int64
	callback  func(Progress)
	stop      chan struct{}
	done      sync.WaitGroup
}

func startProgress(callback func(Progress), interval time.Duration) *progressReporter {
	p := &progressReporter{callback: callback, stop: make(chan struct{})}
	p.total.Store(-1)
	if callback == nil {
		return p
	}
	p.done.Add(1)
	go func() {
		defer p.done.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		last := Progress{Completed: -1}
		for {
			select {
			case <-ticker.C:
				if now := p.snapshot(); now != last {
					p.callback(now)
					last = now
				}
			case <-p.stop:
				return
			}
		}
	}()
	return p
}

func (p *progressReporter) snapshot() Progress {
	return Progress{Completed: p.completed.Load(), Total: p.total.Load()}
}

// finish stops the ticker and reports the final counts once.
func (p *progressReporter) finish() {
	if p.callback == nil {
		return
	}
	close(p.stop)
	p.done.Wait()
	p.callback(p.snapshot())
}
