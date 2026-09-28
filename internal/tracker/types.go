package tracker

import (
	"net/http"
	"net/netip"
	"time"
)

// AnnounceRequest identifies the client and torrent and reports byte counters
// and an optional participation event to a tracker.
type AnnounceRequest struct {
	InfoHash   [20]byte
	PeerID     [20]byte
	Port       uint16
	Uploaded   uint64
	Downloaded uint64
	Left       uint64
	Compact    bool
	Event      Event
}

// Event describes a change in this client's state for a torrent.
type Event string

const (
	EventStarted   Event = "started"
	EventCompleted Event = "completed"
	EventStopped   Event = "stopped"
)

// AnnounceResponse contains the tracker's next-announce interval and peer list.
type AnnounceResponse struct {
	Interval time.Duration
	Peers    []Peer
}

// Peer is a tracker-provided endpoint. PeerID is nil when the tracker omits it,
// as in compact responses; it is not the identity used to deduplicate endpoints.
type Peer struct {
	PeerID *[20]byte
	Addr   netip.AddrPort
}

// Client performs HTTP announces. Its zero value uses a default HTTP client.
type Client struct {
	HTTPClient *http.Client
}
