package peerwire

import (
	"encoding/binary"
	"fmt"
	"io"
)

type MessageID byte

const (
	Choke         MessageID = 0
	Unchoke       MessageID = 1
	Interested    MessageID = 2
	NotInterested MessageID = 3
	Have          MessageID = 4
	Bitfield      MessageID = 5
	Request       MessageID = 6
	Piece         MessageID = 7
	Cancel        MessageID = 8
)

type Message struct {
	ID      MessageID
	Payload []byte
}

const uint32Size = 4

func (m Message) WriteTo(w io.Writer) (int64, error) {
	length := 1 + len(m.Payload)

	buf := make([]byte, uint32Size+length)
	binary.BigEndian.PutUint32(buf[:uint32Size], uint32(length))
	buf[4] = byte(m.ID)
	copy(buf[5:], m.Payload)

	return writeFull(w, buf)
}

func ReadMessage(r io.Reader) (*Message, error) {
	var pref [uint32Size]byte
	if _, err := io.ReadFull(r, pref[:]); err != nil {
		return nil, fmt.Errorf("read msg length: %w", err)
	}

	length := binary.BigEndian.Uint32(pref[:])

	// keep-alive
	if length == 0 {
		return nil, nil
	}

	const maxMsgLen uint32 = 1 << 20
	if length > maxMsgLen {
		return nil, fmt.Errorf("msg length %d exceeds max length %d", length, maxMsgLen)

	}

	body := make([]byte, int(length))
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("read msg body: %w", err)
	}

	return &Message{
		ID:      MessageID(body[0]),
		Payload: body[1:],
	}, nil
}

func WriteKeepAlive(w io.Writer) (int64, error) {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0)

	return writeFull(w, buf)
}

func newHave(idx uint32) Message {
	payload := make([]byte, uint32Size)
	binary.BigEndian.PutUint32(payload[:], idx)
	return Message{
		ID:      Have,
		Payload: payload,
	}
}

func newRequest(idx uint32, begin uint32, length uint32) Message {
	payload := make([]byte, 3*uint32Size)
	binary.BigEndian.PutUint32(payload[:uint32Size], idx)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	binary.BigEndian.PutUint32(payload[2*uint32Size:3*uint32Size], length)

	return Message{
		ID:      Request,
		Payload: payload,
	}
}

func newPiece(idx uint32, begin uint32, block []byte) Message {
	payload := make([]byte, 2*uint32Size+len(block))
	binary.BigEndian.PutUint32(payload[:uint32Size], idx)
	binary.BigEndian.PutUint32(payload[uint32Size:2*uint32Size], begin)
	copy(payload[2*uint32Size:], block)

	return Message{
		ID:      Piece,
		Payload: payload,
	}
}
