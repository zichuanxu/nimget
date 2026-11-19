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
	Directory string   `json:"directory"`
	V1        string   `json:"v1"`
	Trackers  []string `json:"-"`
}
