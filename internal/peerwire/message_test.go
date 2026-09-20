package peerwire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestMessageWriteToCompletesShortWrites(t *testing.T) {
	w := &shortWriter{limit: 2}
	message := Message{ID: MessageInterested}
	want := []byte{0, 0, 0, 1, 2}

	n, err := message.WriteTo(w)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len(want)) {
		t.Fatalf("WriteTo() wrote %d bytes, want %d", n, len(want))
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("WriteTo() bytes = %v, want %v", w.Bytes(), want)
	}
}

func TestMessageWriteToIncludesPayload(t *testing.T) {
	var w bytes.Buffer
	message := Message{
		ID:      MessageHave,
		Payload: []byte{0, 0, 0, 5},
	}
	want := []byte{
		0, 0, 0, 5,
		4,
		0, 0, 0, 5,
	}

	n, err := message.WriteTo(&w)
	if err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	if n != int64(len(want)) {
		t.Fatalf("WriteTo() wrote %d bytes, want %d", n, len(want))
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("WriteTo() bytes = %v, want %v", w.Bytes(), want)
	}
}

func TestWriteKeepAliveWritesZeroLengthPrefix(t *testing.T) {
	var w bytes.Buffer
	want := []byte{0, 0, 0, 0}

	n, err := WriteKeepAlive(&w)
	if err != nil {
		t.Fatalf("WriteKeepAlive() error = %v", err)
	}
	if n != int64(len(want)) {
		t.Fatalf("WriteKeepAlive() wrote %d bytes, want %d", n, len(want))
	}
	if !bytes.Equal(w.Bytes(), want) {
		t.Fatalf("WriteKeepAlive() bytes = %v, want %v", w.Bytes(), want)
	}
}

func TestReadMessageReadsInterested(t *testing.T) {
	input := []byte{0, 0, 0, 1, 2}

	got, err := ReadMessage(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if got == nil {
		t.Fatal("ReadMessage() = nil, want interested message")
	}
	if got.ID != MessageInterested {
		t.Fatalf("Message ID = %d, want %d", got.ID, MessageInterested)
	}
	if len(got.Payload) != 0 {
		t.Fatalf("Payload = %v, want empty payload", got.Payload)
	}
}

func TestReadMessageReadsPayload(t *testing.T) {
	input := []byte{
		0, 0, 0, 5,
		4,
		0, 0, 0, 5,
	}
	wantPayload := []byte{0, 0, 0, 5}

	got, err := ReadMessage(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if got == nil {
		t.Fatal("ReadMessage() = nil, want have message")
	}
	if got.ID != MessageHave {
		t.Fatalf("Message ID = %d, want %d", got.ID, MessageHave)
	}
	if !bytes.Equal(got.Payload, wantPayload) {
		t.Fatalf("Payload = %v, want %v", got.Payload, wantPayload)
	}
}

func TestReadMessageRecognizesKeepAlive(t *testing.T) {
	got, err := ReadMessage(bytes.NewReader([]byte{0, 0, 0, 0}))
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	if got != nil {
		t.Fatalf("ReadMessage() = %#v, want nil keep-alive", got)
	}
}

func TestReadMessageRejectsTruncatedLength(t *testing.T) {
	_, err := ReadMessage(bytes.NewReader([]byte{0, 0}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadMessage() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadMessageRejectsTruncatedBody(t *testing.T) {
	input := []byte{0, 0, 0, 5, 4, 0, 0}

	_, err := ReadMessage(bytes.NewReader(input))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadMessage() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadMessageConsumesOneFrame(t *testing.T) {
	input := []byte{
		0, 0, 0, 1, 2,
		0, 0, 0, 1, 0,
	}
	r := bytes.NewReader(input)

	first, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("first ReadMessage() error = %v", err)
	}
	if first == nil || first.ID != MessageInterested {
		t.Fatalf("first ReadMessage() = %#v, want interested message", first)
	}

	second, err := ReadMessage(r)
	if err != nil {
		t.Fatalf("second ReadMessage() error = %v", err)
	}
	if second == nil || second.ID != MessageChoke {
		t.Fatalf("second ReadMessage() = %#v, want choke message", second)
	}
}

func TestReadMessageRejectsOversizedMessage(t *testing.T) {
	input := []byte{0, 16, 0, 1} // 1 MiB + 1 byte.

	_, err := ReadMessage(bytes.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("ReadMessage() error = %v, want maximum-length error", err)
	}
}
