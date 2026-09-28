package peerwire

import (
	"errors"
	"fmt"
	"io"
)

const protocolName = "BitTorrent protocol"
const lenProtocolName = byte(len(protocolName))
const handshakeSize = 49 + lenProtocolName

// Handshake carries feature bits, a torrent's info hash, and the sender's peer ID.
type Handshake struct {
	Reserved [8]byte
	InfoHash [20]byte
	PeerID   [20]byte
}

// WriteTo writes the fixed BitTorrent handshake, completing short writes and
// reporting the number of bytes written even on failure.
func (h Handshake) WriteTo(w io.Writer) (int64, error) {
	serialized := h.bytes()

	return writeFull(w, serialized[:])
}

// ReadHandshake reads one handshake and validates the protocol name and length.
// Reserved bits are preserved without interpretation. The caller must compare
// the returned info hash with the torrent it intended to join.
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

// bytes converts a `Handshake` object into a byte stream.
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
