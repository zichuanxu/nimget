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
	Directory string `json:"directory"`
	TorrentIdentity
	Verified    *TorrentIdentity   `json:"verified,omitempty"`
	DuplicateOf string             `json:"duplicateOf,omitempty"`
	Trackers    []string           `json:"-"`
	Metadata    []byte             `json:"-"`
	Stage       *MagnetObject      `json:"stage,omitempty"`
	Publication *MagnetPublication `json:"publication,omitempty"`
	Output      *MagnetObject      `json:"output,omitempty"`
}

// TorrentIdentity carries full, normalized v1 and v2 info hashes.
type TorrentIdentity struct {
	V1 string `json:"v1,omitempty"`
	V2 string `json:"v2,omitempty"`
}

func (id TorrentIdentity) Contains(claim TorrentIdentity) bool {
	return (claim.V1 != "" || claim.V2 != "") &&
		(claim.V1 == "" || claim.V1 == id.V1) &&
		(claim.V2 == "" || claim.V2 == id.V2)
}

func (id TorrentIdentity) PreferredHash() string {
	if id.V1 != "" {
		return id.V1
	}
	return id.V2
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
