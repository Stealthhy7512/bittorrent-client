package peerconn

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestDialHonorsCancellationDuringHandshake(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	addr, err := netip.ParseAddrPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	accepted := make(chan net.Conn, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		if _, err := peerwire.ReadHandshake(conn); err != nil {
			conn.Close()
			serverErr <- err
			return
		}
		accepted <- conn
	}()

	ctx, cancel := context.WithCancel(context.Background())
	dialDone := make(chan error, 1)
	go func() {
		conn, _, err := Dial(ctx, addr, [20]byte{1}, [20]byte{2})
		if conn != nil {
			conn.Close()
		}
		dialDone <- err
	}()

	var serverConn net.Conn
	select {
	case serverConn = <-accepted:
		t.Cleanup(func() { serverConn.Close() })
	case err := <-serverErr:
		t.Fatalf("fake peer error = %v", err)
	case <-time.After(time.Second):
		t.Fatal("fake peer did not receive handshake")
	}

	cancel()
	select {
	case err := <-dialDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Dial() error = %v, want context.Canceled", err)
		}
	case <-time.After(200 * time.Millisecond):
		serverConn.Close()
		<-dialDone
		t.Fatal("Dial() did not stop after context cancellation")
	}
}

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
			serverDone <- errors.New("received different info hash")
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
