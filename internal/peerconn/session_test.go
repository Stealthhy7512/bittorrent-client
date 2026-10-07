package peerconn

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestNewSessionStartsChokedWithNoPieces(t *testing.T) {
	session, peer := newTestSession(t, 9)
	defer peer.Close()

	if !session.Choked() {
		t.Fatal("Choked() = false, want true")
	}
	for index := range 9 {
		if session.HasPiece(index) {
			t.Fatalf("HasPiece(%d) = true, want false", index)
		}
	}
}

func TestNewSessionRejectsInvalidArguments(t *testing.T) {
	t.Run("nil connection", func(t *testing.T) {
		if _, err := NewSession(nil, 1); err == nil {
			t.Fatal("NewSession() error = nil, want missing-connection error")
		}
	})

	t.Run("negative piece count", func(t *testing.T) {
		client, peer := net.Pipe()
		defer client.Close()
		defer peer.Close()

		if _, err := NewSession(client, -1); err == nil {
			t.Fatal("NewSession() error = nil, want negative-piece-count error")
		}
	})
}

func TestSessionReadUpdatesChokeState(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()

	readMessage(t, session, peerwire.Message{ID: peerwire.MessageUnchoke}, peer)
	if session.Choked() {
		t.Fatal("Choked() = true after unchoke, want false")
	}

	readMessage(t, session, peerwire.Message{ID: peerwire.MessageChoke}, peer)
	if !session.Choked() {
		t.Fatal("Choked() = false after choke, want true")
	}
}

func TestSessionReadAppliesBitfield(t *testing.T) {
	session, peer := newTestSession(t, 9)
	defer peer.Close()

	readMessage(t, session, peerwire.Message{
		ID:      peerwire.MessageBitfield,
		Payload: []byte{0b10100000, 0b10000000},
	}, peer)

	for index := range 9 {
		want := index == 0 || index == 2 || index == 8
		if got := session.HasPiece(index); got != want {
			t.Fatalf("HasPiece(%d) = %t, want %t", index, got, want)
		}
	}
}

func TestSessionReadAppliesHave(t *testing.T) {
	session, peer := newTestSession(t, 9)
	defer peer.Close()

	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, 7)
	readMessage(t, session, peerwire.Message{
		ID:      peerwire.MessageHave,
		Payload: payload,
	}, peer)

	if !session.HasPiece(7) {
		t.Fatal("HasPiece(7) = false after have, want true")
	}
}

func TestSessionReadSkipsKeepAlive(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()

	writeDone := make(chan error, 1)
	go func() {
		if _, err := peerwire.WriteKeepAlive(peer); err != nil {
			writeDone <- err
			return
		}
		_, err := (peerwire.Message{ID: peerwire.MessageInterested}).WriteTo(peer)
		writeDone <- err
	}()

	message, err := session.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write frames: %v", err)
	}
	if message.ID != peerwire.MessageInterested {
		t.Fatalf("Read() message ID = %d, want %d", message.ID, peerwire.MessageInterested)
	}
}

func TestSessionReadAllowsKeepAliveBeforeBitfield(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()

	writeDone := make(chan error, 1)
	go func() {
		if _, err := peerwire.WriteKeepAlive(peer); err != nil {
			writeDone <- err
			return
		}
		_, err := (peerwire.Message{
			ID:      peerwire.MessageBitfield,
			Payload: []byte{0b10000000},
		}).WriteTo(peer)
		writeDone <- err
	}()

	message, err := session.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write frames: %v", err)
	}
	if message.ID != peerwire.MessageBitfield {
		t.Fatalf("Read() message ID = %d, want %d", message.ID, peerwire.MessageBitfield)
	}
	if !session.HasPiece(0) {
		t.Fatal("HasPiece(0) = false after bitfield, want true")
	}
}

func TestSessionReadAllowsUnsupportedMessageBeforeBitfield(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()

	writeDone := make(chan error, 1)
	go func() {
		if _, err := (peerwire.Message{ID: 20, Payload: []byte{1, 2, 3}}).WriteTo(peer); err != nil {
			writeDone <- err
			return
		}
		_, err := (peerwire.Message{ID: peerwire.MessageBitfield, Payload: []byte{0x80}}).WriteTo(peer)
		writeDone <- err
	}()

	message, err := session.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write frames: %v", err)
	}
	if message.ID != peerwire.MessageBitfield || !session.HasPiece(0) {
		t.Fatalf("Read() message ID = %d, HasPiece(0) = %t; want Bitfield advertising piece 0", message.ID, session.HasPiece(0))
	}
}

func TestSessionReadRejectsRepeatedBitfield(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()
	message := peerwire.Message{
		ID:      peerwire.MessageBitfield,
		Payload: []byte{0b10000000},
	}

	readMessage(t, session, message, peer)
	assertReadRejectsMessage(t, session, message, peer)
}

func TestSessionReadRejectsLateBitfield(t *testing.T) {
	session, peer := newTestSession(t, 1)
	defer peer.Close()

	readMessage(t, session, peerwire.Message{ID: peerwire.MessageUnchoke}, peer)
	assertReadRejectsMessage(t, session, peerwire.Message{
		ID:      peerwire.MessageBitfield,
		Payload: []byte{0b10000000},
	}, peer)
}

func TestSessionReadRejectsMalformedStateMessages(t *testing.T) {
	tests := []struct {
		name       string
		pieceCount int
		message    peerwire.Message
	}{
		{
			name:       "choke with payload",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageChoke, Payload: []byte{1}},
		},
		{
			name:       "unchoke with payload",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageUnchoke, Payload: []byte{1}},
		},
		{
			name:       "interested with payload",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageInterested, Payload: []byte{1}},
		},
		{
			name:       "not interested with payload",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageNotInterested, Payload: []byte{1}},
		},
		{
			name:       "bitfield with wrong length",
			pieceCount: 9,
			message:    peerwire.Message{ID: peerwire.MessageBitfield, Payload: []byte{0}},
		},
		{
			name:       "have with wrong length",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageHave, Payload: []byte{0, 0, 0}},
		},
		{
			name:       "have outside piece range",
			pieceCount: 1,
			message:    peerwire.Message{ID: peerwire.MessageHave, Payload: []byte{0, 0, 0, 1}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, peer := newTestSession(t, test.pieceCount)
			defer peer.Close()

			assertReadRejectsMessage(t, session, test.message, peer)
		})
	}
}

func TestSessionFetchesOneBlockFromPeer(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()

	deadline := time.Now().Add(3 * time.Second)
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}

	session, err := NewSession(client, 1)
	if err != nil {
		t.Fatal(err)
	}

	peerDone := make(chan error, 1)
	go func() {
		peerDone <- func() error {
			if _, err := (peerwire.Message{
				ID:      peerwire.MessageBitfield,
				Payload: []byte{0x80},
			}).WriteTo(peer); err != nil {
				return fmt.Errorf("advertise Piece: %w", err)
			}

			frame, err := peerwire.ReadFrame(peer, 1)
			if err != nil {
				return fmt.Errorf("read Interested: %w", err)
			}
			message, ok := frame.Message()
			if !ok || message.ID != peerwire.MessageInterested {
				return fmt.Errorf("received %#v, want Interested", frame)
			}

			if _, err := peerwire.WriteUnchoke(peer); err != nil {
				return fmt.Errorf("send Unchoke: %w", err)
			}
			frame, err = peerwire.ReadFrame(peer, 1)
			if err != nil {
				return fmt.Errorf("read Request: %w", err)
			}
			message, ok = frame.Message()
			if !ok {
				return fmt.Errorf("received %#v, want Request", frame)
			}
			index, offset, length, err := peerwire.ParseRequest(message)
			if err != nil {
				return fmt.Errorf("parse Request: %w", err)
			}
			if index != 0 || offset != 0 || length != 4 {
				return fmt.Errorf("Request = (%d, %d, %d), want (0, 0, 4)", index, offset, length)
			}
			if _, err := peerwire.WritePiece(peer, 0, 0, []byte("data")); err != nil {
				return fmt.Errorf("send Block: %w", err)
			}
			return nil
		}()
	}()

	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	var received []byte
	var receivedOffset uint32
	var blockCount int
	err = session.FetchPiece(ctx, PieceSpec{Index: 0, Length: 4}, func(offset uint32, block []byte) error {
		blockCount++
		receivedOffset = offset
		received = append([]byte(nil), block...)
		return nil
	})
	_ = client.Close() // Unblock the fake Peer if FetchPiece returned early.
	peerErr := <-peerDone
	if err != nil {
		t.Fatalf("FetchPiece() error = %v (fake Peer: %v)", err, peerErr)
	}
	if peerErr != nil {
		t.Fatalf("fake Peer: %v", peerErr)
	}
	if blockCount != 1 || receivedOffset != 0 || !bytes.Equal(received, []byte("data")) {
		t.Fatalf("accepted Blocks = %d, offset = %d, bytes = %q; want one Block at offset 0 with data", blockCount, receivedOffset, received)
	}
}

func TestSessionFetchPieceRejectsWrongBlock(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	session, err := NewSession(client, 2)
	if err != nil {
		t.Fatal(err)
	}
	peerDone := make(chan error, 1)
	go func() {
		if _, err := (peerwire.Message{ID: peerwire.MessageBitfield, Payload: []byte{0x80}}).WriteTo(peer); err != nil {
			peerDone <- err
			return
		}
		if _, err := peerwire.ReadFrame(peer, 2); err != nil { // Interested
			peerDone <- err
			return
		}
		if _, err := peerwire.WriteUnchoke(peer); err != nil {
			peerDone <- err
			return
		}
		if _, err := peerwire.ReadFrame(peer, 2); err != nil { // Request
			peerDone <- err
			return
		}
		_, err := peerwire.WritePiece(peer, 1, 0, []byte("data"))
		peerDone <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	called := false
	err = session.FetchPiece(ctx, PieceSpec{Index: 0, Length: 4}, func(uint32, []byte) error {
		called = true
		return nil
	})
	_ = client.Close()
	if peerErr := <-peerDone; peerErr != nil {
		t.Fatalf("fake Peer: %v", peerErr)
	}
	if !errors.Is(err, ErrPeerProtocol) || called {
		t.Fatalf("FetchPiece() error = %v, callback called = %t; want protocol error without callback", err, called)
	}
}

func TestSessionFetchPieceCancellationInterruptsRead(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	session, err := NewSession(client, 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- session.FetchPiece(ctx, PieceSpec{Index: 0, Length: 4}, func(uint32, []byte) error {
			return nil
		})
	}()

	if _, err := (peerwire.Message{ID: peerwire.MessageBitfield, Payload: []byte{0x80}}).WriteTo(peer); err != nil {
		t.Fatal(err)
	}
	frame, err := peerwire.ReadFrame(peer, 1)
	if err != nil {
		t.Fatal(err)
	}
	message, ok := frame.Message()
	if !ok || message.ID != peerwire.MessageInterested {
		t.Fatalf("received %#v, want Interested", frame)
	}

	cancel() // FetchPiece is now blocked waiting for Unchoke, with no context deadline.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FetchPiece() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("FetchPiece did not return after context cancellation")
	}
}

func newTestSession(t *testing.T, pieceCount int) (*Session, net.Conn) {
	t.Helper()

	client, peer := net.Pipe()
	t.Cleanup(func() { client.Close() })

	session, err := NewSession(client, pieceCount)
	if err != nil {
		peer.Close()
		t.Fatalf("NewSession() error = %v", err)
	}
	return session, peer
}

func readMessage(t *testing.T, session *Session, message peerwire.Message, peer net.Conn) {
	t.Helper()

	writeDone := writePeerMessage(peer, message)
	got, err := session.Read()
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write message: %v", err)
	}
	if got.ID != message.ID {
		t.Fatalf("Read() message ID = %d, want %d", got.ID, message.ID)
	}
}

func assertReadRejectsMessage(
	t *testing.T,
	session *Session,
	message peerwire.Message,
	peer net.Conn,
) {
	t.Helper()

	writeDone := writePeerMessage(peer, message)
	if _, err := session.Read(); err == nil {
		t.Fatal("Read() error = nil, want rejected-message error")
	}
	// A rejected header can leave its payload unread. Close the pipe before
	// waiting for the writer, which may still be blocked sending that payload.
	if err := peer.Close(); err != nil {
		t.Fatalf("close rejected peer: %v", err)
	}
	if err := <-writeDone; err != nil && !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write message: %v", err)
	}
}

func writePeerMessage(peer net.Conn, message peerwire.Message) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := message.WriteTo(peer)
		done <- err
	}()
	return done
}
