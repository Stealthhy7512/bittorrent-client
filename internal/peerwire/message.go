package peerwire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxBlockSize is the largest block allowed in `Request` and `Piece` messages.
	MaxBlockSize = 16 * 1024
	uint32Size   = 4
	maxMsgLen    = 1 << 20
)

type MessageID byte

const (
	MessageChoke         MessageID = 0
	MessageUnchoke       MessageID = 1
	MessageInterested    MessageID = 2
	MessageNotInterested MessageID = 3
	MessageHave          MessageID = 4
	MessageBitfield      MessageID = 5
	MessageRequest       MessageID = 6
	MessagePiece         MessageID = 7
	MessageCancel        MessageID = 8
)

// Message holds a message ID and its unframed payload. Direct construction does
// not validate the payload; use the relevant constructor or parser for checks.
type Message struct {
	ID      MessageID
	Payload []byte
}

type frameKind byte

const (
	frameKeepAlive frameKind = iota
	frameMessage
	frameUnsupported
)

type Frame struct {
	kind    frameKind
	message Message
}

// WriteTo writes the length prefix, ID, and payload, completing short writes.
func (m Message) WriteTo(w io.Writer) (int64, error) {
	if len(m.Payload) >= maxMsgLen {
		return 0, fmt.Errorf("message length exceeds %d bytes", maxMsgLen)
	}
	length := 1 + len(m.Payload)

	buf := make([]byte, uint32Size+length)
	binary.BigEndian.PutUint32(buf[:uint32Size], uint32(length))

	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)

	return writeFull(w, buf)
}

// ReadFrame reads one frame, rejecting declarations above 1 MiB. It checks
// fixed-size core message lengths, the Bitfield length derived from pieceCount,
// and the Piece block limit before allocating the payload. Unknown message
// payloads are drained and returned as unsupported frames. Bitfield contents
// and Request fields require separate parsing.
//
// A rejected declaration may leave its payload unread. The caller must treat
// that error as terminal for the stream rather than attempt another frame read.
func ReadFrame(r io.Reader, pieceCount int) (Frame, error) {
	if err := validatePieceCount(pieceCount); err != nil {
		return Frame{}, err
	}
	var prefix [uint32Size]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return Frame{}, fmt.Errorf("read msg length: %w", err)
	}

	length := binary.BigEndian.Uint32(prefix[:])

	// keep-alive
	if length == 0 {
		return Frame{
			kind: frameKeepAlive,
		}, nil
	}

	if length > maxMsgLen {
		return Frame{}, fmt.Errorf("msg length %d exceeds max length %d", length, maxMsgLen)

	}

	var id [1]byte
	if _, err := io.ReadFull(r, id[:]); err != nil {
		return Frame{}, fmt.Errorf("read message ID: %w", err)
	}

	messageID := MessageID(id[0])

	// check message-specific lengths before allocating or reading the payload.
	switch messageID {
	case MessageChoke, MessageUnchoke,
		MessageInterested, MessageNotInterested:
		if length != 1 {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want 1",
				messageID,
				length,
			)
		}
	case MessageHave:
		if length != 5 {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want 5",
				messageID,
				length,
			)
		}
	case MessageBitfield:
		expectedBytes := bitfieldByteCount(pieceCount)
		if uint64(length) != uint64(expectedBytes)+1 { // include MessageID
			return Frame{}, fmt.Errorf(
				"message %v: bitfield length is %v, want %v",
				messageID,
				length-1,
				expectedBytes,
			)
		}
	case MessageRequest:
		if length != 13 {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want 13",
				messageID,
				length,
			)
		}
	case MessagePiece:
		if length < 9 {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want at least 9",
				messageID,
				length,
			)
		}

		if length > 9+MaxBlockSize {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want at most 9+%v",
				messageID,
				length,
				MaxBlockSize,
			)
		}
	case MessageCancel:
		if length != 13 {
			return Frame{}, fmt.Errorf(
				"message %v: length %v, want 13",
				messageID,
				length,
			)
		}
	// drain unsupported messages
	default:
		if _, err := io.CopyN(io.Discard, r, int64(length-1)); err != nil {
			return Frame{}, fmt.Errorf(
				"drain unsupported message: %w",
				err,
			)
		}
		return Frame{kind: frameUnsupported}, nil
	}

	payload := make([]byte, int(length-1)) // ID already read, decrement length
	if _, err := io.ReadFull(r, payload); err != nil {
		return Frame{}, fmt.Errorf("read msg body: %w", err)
	}

	return Frame{
		kind: frameMessage,
		message: Message{
			ID:      messageID,
			Payload: payload,
		},
	}, nil
}

// Message returns the enclosed message, or false for a non-message frame kind. The returned
// payload shares the frame's storage.
//
// Kept for handling unsupported frame kinds in the future.
func (f Frame) Message() (Message, bool) {
	if f.kind != frameMessage {
		return Message{}, false
	}
	return f.message, true
}

// WriteKeepAlive writes a zero-length keep-alive frame.
func WriteKeepAlive(w io.Writer) (int64, error) {
	var buf [uint32Size]byte
	return writeFull(w, buf[:])
}

func (f Frame) IsKeepAlive() bool {
	return f.kind == frameKeepAlive
}

func (f Frame) IsUnsupported() bool {
	return f.kind == frameUnsupported
}

func WriteChoke(w io.Writer) (int64, error) {
	return Message{ID: MessageChoke}.WriteTo(w)
}

func WriteUnchoke(w io.Writer) (int64, error) {
	return Message{ID: MessageUnchoke}.WriteTo(w)
}

func WriteInterested(w io.Writer) (int64, error) {
	return Message{ID: MessageInterested}.WriteTo(w)
}

func WriteNotInterested(w io.Writer) (int64, error) {
	return Message{ID: MessageNotInterested}.WriteTo(w)
}

// NewHave encodes an advertised piece index. Checking that the index exists in
// the torrent is the session's responsibility.
func NewHave(pieceIndex uint32) Message {
	payload := make([]byte, uint32Size)
	binary.BigEndian.PutUint32(payload[:], pieceIndex)
	return Message{
		ID:      MessageHave,
		Payload: payload,
	}
}

func WriteHave(w io.Writer, pieceIndex uint32) (int64, error) {
	return NewHave(pieceIndex).WriteTo(w)
}

// ParseHave requires a Have ID and exactly four payload bytes, then decodes the
// index. It does not check the index against a torrent's piece count.
func ParseHave(message Message) (pieceIndex uint32, err error) {
	if message.ID != MessageHave {
		return 0, fmt.Errorf(
			"parse have: message ID is %v, want %v",
			message.ID,
			MessageHave,
		)
	}

	if len(message.Payload) != uint32Size {
		return 0, fmt.Errorf(
			"parse have: payload length is %v, want %v",
			len(message.Payload),
			uint32Size,
		)
	}

	return binary.BigEndian.Uint32(message.Payload), nil
}

// NewRequest encodes a block request with a length from 1 through MaxBlockSize.
// begin is a byte offset within the piece. Torrent-specific index and range
// checks are left to the session.
func NewRequest(pieceIndex, begin, length uint32) (Message, error) {
	if length == 0 {
		return Message{}, errors.New("length cannot be zero")
	}
	if length > MaxBlockSize {
		return Message{}, fmt.Errorf("length %v greater than allowed: %v", length, MaxBlockSize)
	}

	payload := make([]byte, 3*uint32Size)
	binary.BigEndian.PutUint32(payload[:uint32Size], pieceIndex)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	binary.BigEndian.PutUint32(payload[2*uint32Size:3*uint32Size], length)

	return Message{
		ID:      MessageRequest,
		Payload: payload,
	}, nil
}

// WriteRequest validates through NewRequest before writing any bytes, then
// returns the framed byte count, including partial progress on a write error.
func WriteRequest(w io.Writer, pieceIndex, begin, length uint32) (int64, error) {
	message, err := NewRequest(pieceIndex, begin, length)
	if err != nil {
		return 0, err
	}
	return message.WriteTo(w)
}

// ParseRequest checks the Request ID, twelve-byte payload, and block-length
// limits before returning its fields. The session checks the torrent-specific
// piece index and byte range.
func ParseRequest(message Message) (pieceIndex, begin, length uint32, err error) {
	if message.ID != MessageRequest {
		return 0, 0, 0, fmt.Errorf(
			"parse request: message ID is %v, want %v",
			message.ID,
			MessageRequest,
		)
	}

	if len(message.Payload) != uint32Size*3 {
		return 0, 0, 0, fmt.Errorf(
			"parse request: payload length is %v, want %v",
			len(message.Payload),
			uint32Size*3,
		)
	}

	pieceIndex = binary.BigEndian.Uint32(message.Payload[:uint32Size])
	begin = binary.BigEndian.Uint32(message.Payload[uint32Size : uint32Size*2])
	length = binary.BigEndian.Uint32(message.Payload[uint32Size*2 : uint32Size*3])

	if length == 0 {
		return 0, 0, 0, errors.New("parse request: length cannot be zero")
	}

	if length > MaxBlockSize {
		return 0, 0, 0, fmt.Errorf(
			"parse request: length is %v, max allowed length %v",
			length,
			MaxBlockSize,
		)
	}

	return pieceIndex, begin, length, nil
}

// NewPiece encodes one block, copying its bytes so later changes to block do not
// affect the message. It rejects blocks larger than MaxBlockSize; piece indexes,
// offsets, and the expected response length are session-level checks.
func NewPiece(pieceIndex, begin uint32, block []byte) (Message, error) {
	if len(block) > MaxBlockSize {
		return Message{}, fmt.Errorf(
			"new piece: block size %v, max allowed block size %v",
			len(block),
			MaxBlockSize,
		)
	}

	payload := make([]byte, 2*uint32Size+len(block))
	binary.BigEndian.PutUint32(payload[:uint32Size], pieceIndex)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	copy(payload[2*uint32Size:], block)

	return Message{
		ID:      MessagePiece,
		Payload: payload,
	}, nil
}

// WritePiece validates through NewPiece before writing the framed block response.
func WritePiece(w io.Writer, pieceIndex, begin uint32, block []byte) (int64, error) {
	piece, err := NewPiece(pieceIndex, begin, block)
	if err != nil {
		return 0, err
	}

	return piece.WriteTo(w)
}

// ParsePiece checks the Piece ID, eight-byte header, and maximum block size.
// The returned block aliases message.Payload. Matching it to an outstanding
// request, including its exact length, is the session's responsibility.
func ParsePiece(message Message) (pieceIndex, begin uint32, block []byte, err error) {
	if message.ID != MessagePiece {
		return 0, 0, nil, fmt.Errorf(
			"parse piece: message ID is %v, want %v",
			message.ID,
			MessagePiece,
		)
	}

	if len(message.Payload) < 2*uint32Size {
		return 0, 0, nil, fmt.Errorf(
			"parse piece: payload length is %v, want at least %v",
			len(message.Payload),
			uint32Size*2,
		)
	}

	pieceIndex = binary.BigEndian.Uint32(message.Payload[:uint32Size])
	begin = binary.BigEndian.Uint32(message.Payload[uint32Size : uint32Size*2])
	block = message.Payload[uint32Size*2:]

	if len(block) > MaxBlockSize {
		return 0, 0, nil, fmt.Errorf(
			"parse piece: block size %v, max allowed block size: %v",
			len(block),
			MaxBlockSize,
		)
	}

	return pieceIndex, begin, block, nil
}
