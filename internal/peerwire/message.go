package peerwire

import (
	"encoding/binary"
	"fmt"
	"io"
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

const uint32Size = 4

type Frame struct {
	kind    frameKind
	message Message
}

type frameKind byte

const (
	frameKeepAlive frameKind = iota
	frameMessage
)

func (m Message) WriteTo(w io.Writer) (int64, error) {
	length := 1 + len(m.Payload)

	buf := make([]byte, uint32Size+length)
	binary.BigEndian.PutUint32(buf[:uint32Size], uint32(length))
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)

	return writeFull(w, buf)
}

func (f Frame) IsKeepAlive() bool {
	return f.kind == frameKeepAlive
}

func (f Frame) Message() (Message, bool) {
	if f.kind != frameMessage {
		return Message{}, false
	}
	return f.message, true
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

func WriteKeepAlive(w io.Writer) (int64, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0)

	return writeFull(w, buf)
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
func newHave(idx uint32) Message {
	payload := make([]byte, uint32Size)
	binary.BigEndian.PutUint32(payload[:], idx)
	return Message{
		ID:      MessageHave,
		Payload: payload,
	}
}

func newRequest(idx uint32, begin uint32, length uint32) Message {
	payload := make([]byte, 3*uint32Size)
	binary.BigEndian.PutUint32(payload[:uint32Size], idx)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	binary.BigEndian.PutUint32(payload[2*uint32Size:3*uint32Size], length)

	return Message{
		ID:      MessageRequest,
		Payload: payload,
	}
}

func newPiece(idx uint32, begin uint32, block []byte) Message {
	payload := make([]byte, 2*uint32Size+len(block))
	binary.BigEndian.PutUint32(payload[:uint32Size], idx)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	copy(payload[2*uint32Size:], block)

	return Message{
		ID:      MessagePiece,
		Payload: payload,
	}
}
