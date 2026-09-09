package peerwire

import (
	"bytes"
	"testing"
)

type shortWriter struct {
	bytes.Buffer
	limit int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) > w.limit {
		p = p[:w.limit]
	}
	return w.Buffer.Write(p)
}

func TestHandshakeWriteToCompletesShortWrites(t *testing.T) {
	w := &shortWriter{limit: 3}
	want := Handshake{
		InfoHash: [20]byte{1, 2, 3},
		PeerID:   [20]byte{4, 5, 6},
	}

	n, err := want.WriteTo(w)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(handshakeSize) {
		t.Fatalf("WriteTo() wrote %d bytes, want %d", n, handshakeSize)
	}

	got, err := ReadHandshake(bytes.NewReader(w.Bytes()))
	if err != nil {
		t.Fatalf("ReadHandshake() error = %v", err)
	}
	if got != want {
		t.Fatalf("ReadHandshake() = %#v, want %#v", got, want)
	}
}
