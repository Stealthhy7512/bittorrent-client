package peerconn

import (
	"encoding/binary"
	"net"
	"testing"

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

			writeDone := writePeerMessage(peer, test.message)
			if _, err := session.Read(); err == nil {
				t.Fatal("Read() error = nil, want malformed-message error")
			}
			if err := <-writeDone; err != nil {
				t.Fatalf("write message: %v", err)
			}
		})
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
	if err := <-writeDone; err != nil {
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
