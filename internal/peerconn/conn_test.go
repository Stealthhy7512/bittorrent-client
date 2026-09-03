package peerconn

import (
	"net"
	"testing"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestExchangeHandshake(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	t.Cleanup(func() { clientConn.Close() })
	t.Cleanup(func() { serverConn.Close() })

	var infoHash [20]byte
	copy(infoHash[:], "torrent-info-hash-xx")

	var localID [20]byte
	copy(localID[:], "local-peer-id-00000")

	var remoteID [20]byte
	copy(remoteID[:], "remote-peer-id-0000")

	serverDone := make(chan error, 1)
	go func() {
		defer serverConn.Close()

		received, err := peerwire.ReadHandshake(serverConn)
		if err != nil {
			serverDone <- err
			return
		}
		if received.InfoHash != infoHash {
			serverDone <- err
			return
		}

		_, err = (peerwire.Handshake{
			InfoHash: infoHash,
			PeerID:   remoteID,
		}).WriteTo(serverConn)

		serverDone <- err
	}()

	remote, err := exchangeHandshake(clientConn, infoHash, localID)
	if err != nil {
		t.Fatalf("exchangeHandshake() error = %v", err)
	}
	if remote.PeerID != remoteID {
		t.Fatalf("remote PeerID = %x, want %x", remote.PeerID, remoteID)
	}

	if err := <-serverDone; err != nil {
		t.Fatalf("server error = %v", err)
	}
}
