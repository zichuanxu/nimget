package download

// SourceKind identifies how a download task is fetched. Existing tasks use
// the zero value for HTTP and HTTPS.
type SourceKind string

const SourceMagnet SourceKind = "magnet"

type Phase string

const (
	PhaseMetadata     Phase = "metadata"
	PhaseWaitingPeers Phase = "waiting-peers"
	PhaseTransferring Phase = "transferring"
	PhaseProxyBlocked Phase = "proxy-blocked"
)

// Magnet holds service-only state. Tracker hints may contain credentials and
// are stored separately from the public URL.
type Magnet struct {
	Directory   string             `json:"directory"`
	V1          string             `json:"v1"`
	Trackers    []string           `json:"-"`
	Metadata    []byte             `json:"-"`
	Stage       *MagnetObject      `json:"stage,omitempty"`
	Publication *MagnetPublication `json:"publication,omitempty"`
	Output      *MagnetObject      `json:"output,omitempty"`
}

// MagnetObject records the rooted identity of one download output.
type MagnetObject struct {
	RootID    string `json:"rootId"`
	Name      string `json:"name"`
	ID        string `json:"id"`
	Directory bool   `json:"directory,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

// MagnetPublication binds the verified Stage to the task's current
// Destination. BaseName generates a new suffix if Destination is occupied.
type MagnetPublication struct {
	BaseName string `json:"baseName"`
}

// MagnetPublicationStep is a checkpoint reached around the exclusive move.
type MagnetPublicationStep string

const (
	MagnetPublicationRecorded MagnetPublicationStep = "recorded"
	MagnetPublicationMoved    MagnetPublicationStep = "moved"
)
