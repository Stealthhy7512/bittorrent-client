package tracker

import (
	"net/http"
	"net/netip"
	"time"
)

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

type AnnounceResponse struct {
	Interval time.Duration
	Peers    []Peer
}

// Peer struct combines compact and non-compact for by
// having PeerID field optional by having it nil.
type Peer struct {
	PeerID *[20]byte
	Addr   netip.AddrPort
}

type Client struct {
	HTTPClient *http.Client
}
