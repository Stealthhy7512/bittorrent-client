package peerwire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
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

	got, err := ReadFrame(bytes.NewReader(input), 0)
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

	got, err := ReadFrame(bytes.NewReader(input), 0)
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
	got, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}), 0)
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
	_, err := ReadFrame(bytes.NewReader([]byte{0, 0}), 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadFrame() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadFrameRejectsTruncatedBody(t *testing.T) {
	input := []byte{0, 0, 0, 5, 4, 0, 0}

	_, err := ReadFrame(bytes.NewReader(input), 0)
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

	first, err := ReadFrame(r, 0)
	if err != nil {
		t.Fatalf("first ReadFrame() error = %v", err)
	}
	firstMessage := requireMessageFrame(t, first)
	if firstMessage.ID != MessageInterested {
		t.Fatalf("first ReadFrame() = %#v, want interested message", first)
	}

	second, err := ReadFrame(r, 0)
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

	_, err := ReadFrame(bytes.NewReader(input), 0)
	if err == nil || !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("ReadFrame() error = %v, want maximum-length error", err)
	}
}

func TestReadFrameChecksBitfieldLengthBeforeReadingPayload(t *testing.T) {
	for _, declaredLength := range []uint32{2, 4} {
		t.Run(fmt.Sprintf("length %d", declaredLength), func(t *testing.T) {
			input := make([]byte, 6)
			binary.BigEndian.PutUint32(input[:4], declaredLength)
			input[4] = byte(MessageBitfield)
			input[5] = 0xaa
			r := bytes.NewReader(input)

			if _, err := ReadFrame(r, 9); err == nil {
				t.Fatal("ReadFrame() error = nil, want bitfield-length error")
			}
			if r.Len() != 1 {
				t.Fatalf("unread bytes = %d, want payload byte left unread", r.Len())
			}
		})
	}

	input := []byte{0, 0, 0, 3, byte(MessageBitfield), 0x80, 0x80}
	frame, err := ReadFrame(bytes.NewReader(input), 9)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	message := requireMessageFrame(t, frame)
	if message.ID != MessageBitfield || !bytes.Equal(message.Payload, []byte{0x80, 0x80}) {
		t.Fatalf("ReadFrame() message = %#v, want two-byte bitfield", message)
	}
}

func TestReadFrameDrainsUnsupportedMessage(t *testing.T) {
	r := bytes.NewReader([]byte{
		0, 0, 0, 4, 20, 0xaa, 0xbb, 0xcc,
		0, 0, 0, 1, byte(MessageChoke),
	})
	frame, err := ReadFrame(r, 0)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if !frame.IsUnsupported() {
		t.Fatalf("ReadFrame() = %#v, want unsupported frame", frame)
	}
	if _, ok := frame.Message(); ok {
		t.Fatal("Message() ok = true for unsupported frame, want false")
	}

	frame, err = ReadFrame(r, 0)
	if err != nil {
		t.Fatalf("next ReadFrame() error = %v", err)
	}
	if message := requireMessageFrame(t, frame); message.ID != MessageChoke {
		t.Fatalf("next message ID = %d, want Choke", message.ID)
	}
}

func TestReadFrameRejectsTruncatedUnsupportedMessage(t *testing.T) {
	input := []byte{0, 0, 0, 4, 20, 0xaa}
	if _, err := ReadFrame(bytes.NewReader(input), 0); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame() error = %v, want io.EOF", err)
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

func TestParsePiece(t *testing.T) {
	payload := make([]byte, 2*uint32Size+MaxBlockSize)
	binary.BigEndian.PutUint32(payload[:uint32Size], 258)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], 16*1024)
	for i := 2 * uint32Size; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	wantBlock := payload[2*uint32Size:]

	pieceIndex, begin, block, err := ParsePiece(Message{
		ID:      MessagePiece,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("ParsePiece() error = %v", err)
	}
	if pieceIndex != 258 {
		t.Fatalf("ParsePiece() piece index = %d, want 258", pieceIndex)
	}
	if begin != 16*1024 {
		t.Fatalf("ParsePiece() begin = %d, want %d", begin, 16*1024)
	}
	if !bytes.Equal(block, wantBlock) {
		t.Fatalf("ParsePiece() block differs from input block")
	}
}

func TestParsePieceRejectsInvalidMessage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name: "wrong message ID",
			message: Message{
				ID:      MessageRequest,
				Payload: make([]byte, 2*uint32Size),
			},
		},
		{
			name:    "empty payload",
			message: Message{ID: MessagePiece},
		},
		{
			name: "short header",
			message: Message{
				ID:      MessagePiece,
				Payload: make([]byte, 2*uint32Size-1),
			},
		},
		{
			name: "oversized block",
			message: Message{
				ID:      MessagePiece,
				Payload: make([]byte, 2*uint32Size+MaxBlockSize+1),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := ParsePiece(test.message); err == nil {
				t.Fatal("ParsePiece() error = nil, want invalid-message error")
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

func TestReadFrameRejectsInvalidLengthBeforeReadingPayload(t *testing.T) {
	tests := []struct {
		id     MessageID
		length uint32
	}{
		{MessageChoke, 2},
		{MessageUnchoke, 2},
		{MessageInterested, 2},
		{MessageNotInterested, 2},
		{MessageHave, 4},
		{MessageHave, 6},
		{MessageRequest, 12},
		{MessageRequest, 14},
		{MessageCancel, 12},
		{MessageCancel, 14},
		{MessagePiece, 8},
		{MessagePiece, 9 + MaxBlockSize + 1},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("ID %d length %d", test.id, test.length), func(t *testing.T) {
			input := []byte{0, 0, 0, 0, byte(test.id), 0xaa}
			binary.BigEndian.PutUint32(input[:4], test.length)
			r := bytes.NewReader(input)
			if _, err := ReadFrame(r, 0); err == nil {
				t.Fatal("ReadFrame() error = nil, want invalid-length error")
			}
			if r.Len() != 1 {
				t.Fatalf("unread bytes = %d, want payload byte left unread", r.Len())
			}
		})
	}
}

func TestReadFrameAcceptsCoreMessageLengths(t *testing.T) {
	tests := []struct {
		id      MessageID
		payload []byte
	}{
		{MessageChoke, nil},
		{MessageUnchoke, nil},
		{MessageInterested, nil},
		{MessageNotInterested, nil},
		{MessageHave, []byte{0, 0, 0, 1}},
		{MessageRequest, []byte{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}},
		{MessageCancel, []byte{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}},
		{MessagePiece, append(make([]byte, 8), bytes.Repeat([]byte{0xab}, MaxBlockSize)...)},
	}
	for _, test := range tests {
		t.Run(fmt.Sprintf("ID %d", test.id), func(t *testing.T) {
			input := make([]byte, 5)
			binary.BigEndian.PutUint32(input[:4], uint32(1+len(test.payload)))
			input[4] = byte(test.id)
			input = append(input, test.payload...)
			frame, err := ReadFrame(bytes.NewReader(input), 0)
			if err != nil {
				t.Fatalf("ReadFrame() error = %v", err)
			}
			got := requireMessageFrame(t, frame)
			if got.ID != test.id || !bytes.Equal(got.Payload, test.payload) {
				t.Fatalf("ReadFrame() ID = %d, payload length = %d; want ID %d and matching payload", got.ID, len(got.Payload), test.id)
			}
		})
	}
}

func TestSemanticWritersEncodeFrames(t *testing.T) {
	bitfield, err := NewBitfield(9)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []int{0, 8} {
		if err := bitfield.SetPiece(index); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name  string
		write func(io.Writer) (int64, error)
		want  []byte
	}{
		{"keep-alive", WriteKeepAlive, []byte{0, 0, 0, 0}},
		{"choke", WriteChoke, []byte{0, 0, 0, 1, 0}},
		{"unchoke", WriteUnchoke, []byte{0, 0, 0, 1, 1}},
		{"interested", WriteInterested, []byte{0, 0, 0, 1, 2}},
		{"not interested", WriteNotInterested, []byte{0, 0, 0, 1, 3}},
		{"have", func(w io.Writer) (int64, error) { return WriteHave(w, 258) }, []byte{0, 0, 0, 5, 4, 0, 0, 1, 2}},
		{"bitfield", func(w io.Writer) (int64, error) { return WriteBitfield(w, bitfield) }, []byte{0, 0, 0, 3, 5, 0x80, 0x80}},
		{"request", func(w io.Writer) (int64, error) { return WriteRequest(w, 258, 16384, 7) }, []byte{0, 0, 0, 13, 6, 0, 0, 1, 2, 0, 0, 0x40, 0, 0, 0, 0, 7}},
		{"piece", func(w io.Writer) (int64, error) { return WritePiece(w, 258, 16384, []byte{0xab, 0xcd}) }, []byte{0, 0, 0, 11, 7, 0, 0, 1, 2, 0, 0, 0x40, 0, 0xab, 0xcd}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := &shortWriter{limit: 2}
			n, err := test.write(w)
			if err != nil || n != int64(len(test.want)) || !bytes.Equal(w.Bytes(), test.want) {
				t.Fatalf("write() = (%d, %v), bytes %v; want %d, nil, %v", n, err, w.Bytes(), len(test.want), test.want)
			}
		})
	}
}

func TestRequestLengthBounds(t *testing.T) {
	for _, length := range []uint32{0, 1, MaxBlockSize, MaxBlockSize + 1} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			valid := length > 0 && length <= MaxBlockSize
			// Build incoming bytes independently of NewRequest.
			payload := []byte{0, 0, 1, 2, 0, 0, 0x40, 0, 0, 0, 0, 0}
			binary.BigEndian.PutUint32(payload[8:], length)
			index, begin, gotLength, err := ParseRequest(Message{ID: MessageRequest, Payload: payload})
			if (err == nil) != valid {
				t.Fatalf("ParseRequest() error = %v, valid = %t", err, valid)
			}
			if valid && (index != 258 || begin != 16384 || gotLength != length) {
				t.Fatalf("ParseRequest() = (%d, %d, %d), want (258, 16384, %d)", index, begin, gotLength, length)
			}
			message, err := NewRequest(258, 16384, length)
			if (err == nil) != valid {
				t.Fatalf("NewRequest() error = %v, valid = %t", err, valid)
			}
			if valid && (message.ID != MessageRequest || !bytes.Equal(message.Payload, payload)) {
				t.Fatalf("NewRequest() = %#v, want Request with payload %v", message, payload)
			}
		})
	}
}

func TestParseRequestRejectsInvalidMessage(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{"wrong ID", Message{ID: MessageCancel, Payload: []byte{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3}}},
		{"empty", Message{ID: MessageRequest}},
		{"short", Message{ID: MessageRequest, Payload: make([]byte, 11)}},
		{"long", Message{ID: MessageRequest, Payload: []byte{0, 0, 0, 1, 0, 0, 0, 2, 0, 0, 0, 3, 0}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, _, err := ParseRequest(test.message); err == nil {
				t.Fatal("ParseRequest() error = nil, want invalid-message error")
			}
		})
	}
}

func TestNewPieceCopiesMaximumBlock(t *testing.T) {
	block := bytes.Repeat([]byte{0xab}, MaxBlockSize)
	message, err := NewPiece(258, 16384, block)
	if err != nil {
		t.Fatal(err)
	}
	block[0] = 0
	want := append([]byte{0, 0, 1, 2, 0, 0, 0x40, 0}, bytes.Repeat([]byte{0xab}, MaxBlockSize)...)
	if message.ID != MessagePiece || !bytes.Equal(message.Payload, want) {
		t.Fatal("NewPiece() did not preserve the encoded header and independent block copy")
	}
}

func TestInvalidMessageWritersDoNotWrite(t *testing.T) {
	tests := []struct {
		name  string
		write func(io.Writer) (int64, error)
	}{
		{"empty request", func(w io.Writer) (int64, error) { return WriteRequest(w, 0, 0, 0) }},
		{"oversized request", func(w io.Writer) (int64, error) { return WriteRequest(w, 0, 0, MaxBlockSize+1) }},
		{"oversized piece", func(w io.Writer) (int64, error) { return WritePiece(w, 0, 0, make([]byte, MaxBlockSize+1)) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w := &recordingErrorWriter{err: errors.New("unexpected write")}
			n, err := test.write(w)
			if err == nil || n != 0 || w.calls != 0 {
				t.Fatalf("write() = (%d, %v), writer calls = %d; want zero bytes, validation error, no calls", n, err, w.calls)
			}
		})
	}
}
