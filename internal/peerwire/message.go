package peerwire

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// MaxBlockSize is the largest block allowed in `Request` and `Piece` messages.
	MaxBlockSize = 16 * 1024
	uint32Size   = 4
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

type Message struct {
	ID      MessageID
	Payload []byte
}

type frameKind byte

const (
	frameKeepAlive frameKind = iota
	frameMessage
)

type Frame struct {
	kind    frameKind
	message Message
}

func (m Message) WriteTo(w io.Writer) (int64, error) {
	length := 1 + len(m.Payload)

	buf := make([]byte, uint32Size+length)
	binary.BigEndian.PutUint32(buf[:uint32Size], uint32(length))
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)

	return writeFull(w, buf)
}

func ReadFrame(r io.Reader) (Frame, error) {
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

	const maxMsgLen uint32 = 1 << 20
	if length > maxMsgLen {
		return Frame{}, fmt.Errorf("msg length %d exceeds max length %d", length, maxMsgLen)

	}

	body := make([]byte, int(length))
	if _, err := io.ReadFull(r, body); err != nil {
		return Frame{}, fmt.Errorf("read msg body: %w", err)
	}

	return Frame{
		kind: frameMessage,
		message: Message{
			ID:      MessageID(body[0]),
			Payload: body[1:],
		},
	}, nil
}

func (f Frame) Message() (Message, bool) {
	if f.kind != frameMessage {
		return Message{}, false
	}
	return f.message, true
}

func WriteKeepAlive(w io.Writer) (int64, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0)

	return writeFull(w, buf)
}

func (f Frame) IsKeepAlive() bool {
	return f.kind == frameKeepAlive
}

func newHave(pieceIndex uint32) Message {
	payload := make([]byte, uint32Size)
	binary.BigEndian.PutUint32(payload[:], pieceIndex)
	return Message{
		ID:      MessageHave,
		Payload: payload,
	}
}

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

func newRequest(pieceIndex uint32, begin uint32, length uint32) Message {
	payload := make([]byte, 3*uint32Size)
	binary.BigEndian.PutUint32(payload[:uint32Size], pieceIndex)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	binary.BigEndian.PutUint32(payload[2*uint32Size:3*uint32Size], length)

	return Message{
		ID:      MessageRequest,
		Payload: payload,
	}
}

func newPiece(pieceIndex uint32, begin uint32, block []byte) Message {
	payload := make([]byte, 2*uint32Size+len(block))
	binary.BigEndian.PutUint32(payload[:uint32Size], pieceIndex)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	copy(payload[2*uint32Size:], block)

	return Message{
		ID:      MessagePiece,
		Payload: payload,
	}
}

func ParsePiece(message Message) (pieceIndex uint32, begin uint32, block []byte, err error) {
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
