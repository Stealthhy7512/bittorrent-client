package tracker

import (
	"bytes"
	"testing"
	"time"
)

func TestDecodeAnnounceResponseWithDictionaryPeers(t *testing.T) {
	response := []byte(
		"d8:intervali1800e5:peersl" +
			"d2:ip9:127.0.0.17:peer id20:abcdefghijklmnopqrst4:porti6881e" +
			"eee",
	)

	got, err := decodeAnnounceResponse(bytes.NewReader(response))
	if err != nil {
		t.Fatalf("decodeAnnounceResponse() error = %v", err)
	}
	if got.Interval != 30*time.Minute {
		t.Fatalf("Interval = %v, want 30m", got.Interval)
	}
	if len(got.Peers) != 1 {
		t.Fatalf("len(Peers) = %d, want 1", len(got.Peers))
	}

	peer := got.Peers[0]
	if peer.Addr.String() != "127.0.0.1:6881" {
		t.Fatalf("Addr = %s, want 127.0.0.1:6881", peer.Addr)
	}
	if peer.PeerID == nil {
		t.Fatal("PeerID = nil, want peer ID")
	}
	if got := string(peer.PeerID[:]); got != "abcdefghijklmnopqrst" {
		t.Fatalf("PeerID = %q, want abcdefghijklmnopqrst", got)
	}
}

func TestParseDictPeersAllowsMissingPeerID(t *testing.T) {
	peers, err := parseDictPeers([]any{
		map[string]any{
			"ip":   "192.0.2.1",
			"port": int64(6881),
		},
	})
	if err != nil {
		t.Fatalf("parseDictPeers() error = %v", err)
	}
	if peers[0].PeerID != nil {
		t.Fatalf("PeerID = %x, want nil", peers[0].PeerID)
	}
}
