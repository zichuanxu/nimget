// Package download defines the service-owned download model shared by the
// manager, store, and API layers.
package download

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/amemiya02/nimget/internal/settings"
	"golang.org/x/time/rate"
)

var (
	ErrNotFound = errors.New("download not found")
	// ErrInvalidTransition reports a command that the item's status forbids,
	// such as pausing a completed download.
	ErrInvalidTransition = errors.New("invalid download state transition")
	// ErrActive reports a command that requires the runner to have stopped.
	ErrActive = errors.New("download is active")
	// ErrRestartRequired means the refreshed source cannot safely reuse the saved bytes.
	ErrRestartRequired = errors.New("cannot verify the refreshed source matches the partial download; restart from the beginning or supply another source")
	// ErrInvalidDownload reports a rejected URL or destination.
	ErrInvalidDownload = errors.New("invalid download")
	// ErrDestinationBusy reports that another process holds the destination
	// lock; the download is retried later instead of failing.
	ErrDestinationBusy = errors.New("destination is locked by another download")
	// ErrInvalidSettings reports settings that failed validation.
	ErrInvalidSettings = errors.New("invalid settings")
	// Runner errors used to classify task failures without exposing diagnostics.
	ErrStalled          = errors.New("connection stalled")
	ErrDiskSpace        = errors.New("not enough free disk space")
	ErrUnsupportedMedia = errors.New("unsupported media stream")
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusPaused    Status = "paused"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

// Priorities order the queue; higher starts first, then first added.
const (
	PriorityLow    = -1
	PriorityNormal = 0
	PriorityHigh   = 1
)

type Recovery string

const (
	RecoveryValidateSource  Recovery = "validate-source"
	RecoveryRestartRequired Recovery = "restart-required"
)

type Failure string

const (
	FailureAuthorization        Failure = "authorization"
	FailureDiskSpace            Failure = "disk-space"
	FailureFilesystemPermission Failure = "filesystem-permission"
	FailureNetwork              Failure = "network"
	FailureUnsupportedMedia     Failure = "unsupported-media"
)

type Item struct {
	ID          string   `json:"id"`
	URL         string   `json:"url"`
	Destination string   `json:"destination"`
	Status      Status   `json:"status"`
	Completed   int64    `json:"completed"`
	Total       int64    `json:"total"`
	Error       string   `json:"error,omitempty"`
	Failure     Failure  `json:"failure,omitempty"`
	Recovery    Recovery `json:"recovery,omitempty"`
	Priority    int      `json:"priority"`
	// Connections overrides the site and default connection count when set.
	Connections int `json:"connections,omitempty"`
	// SpeedLimit caps this download in bytes per second; 0 is none.
	SpeedLimit int64  `json:"speedLimit,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
	// Headers are request headers such as cookies. They may hold secrets, so
	// they never leave the service.
	Headers                   map[string]string `json:"-"`
	WebsiteSession            *WebsiteSession   `json:"-"`
	WebsiteSessionUnavailable bool              `json:"-"`
	CreatedAt                 time.Time         `json:"createdAt"`
	UpdatedAt                 time.Time         `json:"updatedAt"`
}

// Request adds a download. Without a Destination the file name comes from the
// server, placed in Directory or in the root's category folder.
type Request struct {
	URL            string            `json:"url"`
	Destination    string            `json:"destination,omitempty"`
	Directory      string            `json:"directory,omitempty"`
	Priority       int               `json:"priority,omitempty"`
	Connections    int               `json:"connections,omitempty"`
	SpeedLimit     int64             `json:"speedLimit,omitempty"`
	Checksum       string            `json:"checksum,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	WebsiteSession *WebsiteSession   `json:"-"`
}

// Patch changes a download's options; nil fields are left alone. A new speed
// limit applies immediately, new connections from the next start.
type Patch struct {
	Priority    *int   `json:"priority,omitempty"`
	Connections *int   `json:"connections,omitempty"`
	SpeedLimit  *int64 `json:"speedLimit,omitempty"`
	// URL replaces the source of a stopped, failed or paused download and
	// queues it for recovery. Supply Headers with the new request context.
	// Omitted headers are cleared when replacing the source.
	URL *string `json:"url,omitempty"`
	// Headers replace the request headers, for example after the session
	// cookie expired; they apply from the next start.
	Headers *map[string]string `json:"headers,omitempty"`
}

type Progress struct {
	Completed int64
	Total     int64
}

type EventType string

const (
	EventUpdated  EventType = "updated"
	EventProgress EventType = "progress"
	EventDeleted  EventType = "deleted"
)

type Event struct {
	Sequence int64     `json:"sequence,omitempty"`
	Type     EventType `json:"type"`
	Item     Item      `json:"item"`
}

type Repository interface {
	Create(context.Context, Item) error
	Get(context.Context, string) (Item, error)
	List(context.Context) ([]Item, error)
	Update(context.Context, Item) error
	// UpdateProgress stores byte progress only while the item is still running,
	// so a periodic flush can never overwrite a newer terminal status.
	UpdateProgress(ctx context.Context, id string, completed, total int64) error
	Delete(context.Context, string) error
	// UpdateWithHeaders stores the item and its replacement request headers atomically.
	UpdateWithHeaders(context.Context, Item) error
	// LoadSettings returns the stored settings, or ok=false when none are.
	LoadSettings(context.Context) (s settings.Settings, ok bool, err error)
	SaveSettings(context.Context, settings.Settings) error
}

// Spec is one run of a download, resolved from the item and the settings.
type Spec struct {
	URL, Destination string
	Connections      int
	Headers          http.Header
	Checksum         string
	ResumeKey        string
	// ValidateSource requires proof of continuity before saved bytes can be reused.
	ValidateSource bool
	// Limiters cap the body reads; every one of them must admit the bytes.
	Limiters []*rate.Limiter
}

// Remote describes a resource before it is downloaded.
type Remote struct {
	// Filename is the Content-Disposition name, if any.
	Filename string
	// URL is the final URL after redirects.
	URL             string
	ContentType     string
	Size            int64  // -1 when unknown
	AttachmentProof string // hash of replayable attachment metadata at the requested URL
}

type Runner interface {
	Inspect(ctx context.Context, rawURL string, headers http.Header) (Remote, error)
	Download(context.Context, Spec, func(Progress)) error
	// Discard removes the resumable partial state kept for a destination.
	Discard(Spec) error
}
