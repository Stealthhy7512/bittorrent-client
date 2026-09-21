package peerwire

import (
	"bytes"
	"encoding/binary"
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

func TestReadFrameReadsInterested(t *testing.T) {
	input := []byte{0, 0, 0, 1, 2}

	got, err := ReadFrame(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	message := requireMessageFrame(t, got)
	if message.ID != MessageInterested {
		t.Fatalf("Message ID = %d, want %d", message.ID, MessageInterested)
	}
	if len(message.Payload) != 0 {
		t.Fatalf("Payload = %v, want empty payload", message.Payload)
	}
}

func TestReadFrameReadsPayload(t *testing.T) {
	input := []byte{
		0, 0, 0, 5,
		4,
		0, 0, 0, 5,
	}
	wantPayload := []byte{0, 0, 0, 5}

	got, err := ReadFrame(bytes.NewReader(input))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	message := requireMessageFrame(t, got)
	if message.ID != MessageHave {
		t.Fatalf("Message ID = %d, want %d", message.ID, MessageHave)
	}
	if !bytes.Equal(message.Payload, wantPayload) {
		t.Fatalf("Payload = %v, want %v", message.Payload, wantPayload)
	}
}

func TestReadFrameRecognizesKeepAlive(t *testing.T) {
	got, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if !got.IsKeepAlive() {
		t.Fatalf("ReadFrame() = %#v, want keep-alive frame", got)
	}
	if _, ok := got.Message(); ok {
		t.Fatal("Message() ok = true for keep-alive, want false")
	}
}

func TestReadFrameRejectsTruncatedLength(t *testing.T) {
	_, err := ReadFrame(bytes.NewReader([]byte{0, 0}))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadFrame() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameRejectsTruncatedBody(t *testing.T) {
	input := []byte{0, 0, 0, 5, 4, 0, 0}

	_, err := ReadFrame(bytes.NewReader(input))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadFrame() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameConsumesOneFrame(t *testing.T) {
	input := []byte{
		0, 0, 0, 1, 2,
		0, 0, 0, 1, 0,
	}
	r := bytes.NewReader(input)

	first, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("first ReadFrame() error = %v", err)
	}
	firstMessage := requireMessageFrame(t, first)
	if firstMessage.ID != MessageInterested {
		t.Fatalf("first ReadFrame() = %#v, want interested message", first)
	}

	second, err := ReadFrame(r)
	if err != nil {
		t.Fatalf("second ReadFrame() error = %v", err)
	}
	secondMessage := requireMessageFrame(t, second)
	if secondMessage.ID != MessageChoke {
		t.Fatalf("second ReadFrame() = %#v, want choke message", second)
	}
}

func TestReadFrameRejectsOversizedMessage(t *testing.T) {
	input := []byte{0, 16, 0, 1} // 1 MiB + 1 byte.

	_, err := ReadFrame(bytes.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("ReadFrame() error = %v, want maximum-length error", err)
	}
}

func TestParseHave(t *testing.T) {
	payload := make([]byte, uint32Size)
	binary.BigEndian.PutUint32(payload, 258)

	got, err := ParseHave(Message{ID: MessageHave, Payload: payload})
	if err != nil {
		t.Fatalf("ParseHave() error = %v", err)
	}
	if got != 258 {
		t.Fatalf("ParseHave() = %d, want 258", got)
	}
}

func TestParseHaveRejectsInvalidMessage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name:    "wrong message ID",
			message: Message{ID: MessageRequest, Payload: make([]byte, uint32Size)},
		},
		{
			name:    "empty payload",
			message: Message{ID: MessageHave},
		},
		{
			name:    "short payload",
			message: Message{ID: MessageHave, Payload: make([]byte, uint32Size-1)},
		},
		{
			name:    "long payload",
			message: Message{ID: MessageHave, Payload: make([]byte, uint32Size+1)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseHave(test.message); err == nil {
				t.Fatal("ParseHave() error = nil, want invalid-message error")
			}
		})
	}
}

func requireMessageFrame(t *testing.T, frame Frame) Message {
	t.Helper()

	if frame.IsKeepAlive() {
		t.Fatal("frame is keep-alive, want message frame")
	}
	message, ok := frame.Message()
	if !ok {
		t.Fatal("Message() ok = false, want true")
	}
	return message
}
