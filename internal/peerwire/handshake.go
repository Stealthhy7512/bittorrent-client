package peerwire

import (
	"errors"
	"fmt"
	"io"
)

const protocolName = "BitTorrent protocol"
const lenProtocolName = byte(len(protocolName))
const handshakeSize = 49 + lenProtocolName

type Handshake struct {
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

func (h Handshake) WriteTo(w io.Writer) (int64, error) {
	serialized := h.bytes()

	n, err := w.Write(serialized[:])
	if err == nil && n != len(serialized) {
		err = io.ErrShortWrite
	}

	return int64(n), err
}

func ReadHandshake(r io.Reader) (Handshake, error) {
	var length [1]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		return Handshake{}, err
	}

	if length[0] != byte(lenProtocolName) {
		return Handshake{}, fmt.Errorf("unexpected protocol length %d", length[0])
	}

	var rest [handshakeSize - 1]byte
	if _, err := io.ReadFull(r, rest[:]); err != nil {
		return Handshake{}, err
	}

	if string(rest[:lenProtocolName]) != protocolName {
		return Handshake{}, errors.New("unexpected protocol name")
	}

	offset := lenProtocolName
	var hs Handshake

	copy(hs.Reserved[:], rest[offset:offset+8])
	offset += 8

	copy(hs.InfoHash[:], rest[offset:offset+20])
	offset += 20

	copy(hs.PeerID[:], rest[offset:offset+20])

	return hs, nil
}

func (h Handshake) bytes() [handshakeSize]byte {
	var buf [handshakeSize]byte

	buf[0] = lenProtocolName

	offset := 1
	offset += copy(buf[offset:], protocolName)
	offset += copy(buf[offset:], h.Reserved[:])
	offset += copy(buf[offset:], h.InfoHash[:])
	copy(buf[offset:], h.PeerID[:])

	return buf
}
