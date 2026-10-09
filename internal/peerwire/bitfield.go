package peerwire

import (
	"errors"
	"fmt"
	"io"
	"slices"
)

// Bitfield stores piece availability in high-bit-first wire order. Its zero
// value represents zero pieces.
type Bitfield struct {
	bits       []byte
	pieceCount int
}

func NewBitfield(pieceCount int) (Bitfield, error) {
	if err := validatePieceCount(pieceCount); err != nil {
		return Bitfield{}, err
	}

	return Bitfield{
		bits:       make([]byte, bitfieldByteCount(pieceCount)),
		pieceCount: pieceCount,
	}, nil
}

func validatePieceCount(pieceCount int) error {
	if pieceCount < 0 {
		return fmt.Errorf("piece count cannot be negative: %d", pieceCount)
	}
	if uint64(pieceCount) > 1<<32 {
		return fmt.Errorf("piece count %d exceeds peer-wire index range", pieceCount)
	}
	return nil
}

// bitfieldByteCount rounds a validated, non-negative Piece count up to bytes
// without adding to pieceCount, which could overflow int.
func bitfieldByteCount(pieceCount int) int {
	bytes := pieceCount / 8
	if pieceCount%8 != 0 {
		bytes++
	}
	return bytes
}

// WriteBitfield writes the supplied availability set as a framed Bitfield
// message. It neither changes the set nor decides when advertising is allowed.
func WriteBitfield(w io.Writer, bitfield Bitfield) (int64, error) {
	return Message{
		ID:      MessageBitfield,
		Payload: bitfield.bits,
	}.WriteTo(w)
}

// ParseBitfield reads a bitstream containing a bitfield, validates and returns a
// `Bitfield` object with copied underlying bits.
//
// Exact piece count is needed to parse correctly.
func ParseBitfield(payload []byte, pieceCount int) (Bitfield, error) {
	if err := validatePieceCount(pieceCount); err != nil {
		return Bitfield{}, err
	}

	expectedPayloadSize := bitfieldByteCount(pieceCount)

	if len(payload) != expectedPayloadSize {
		return Bitfield{}, fmt.Errorf(
			"bitfield length is %v, want %v",
			len(payload),
			expectedPayloadSize,
		)
	}

	usedBitsOfFinalByte := pieceCount % 8
	if usedBitsOfFinalByte != 0 {
		spareBits := 8 - usedBitsOfFinalByte
		spareMask := byte((1 << spareBits) - 1)

		if payload[len(payload)-1]&spareMask != 0 {
			return Bitfield{}, errors.New("bitfield has spare bits set")
		}
	}

	return Bitfield{
		bits:       slices.Clone(payload),
		pieceCount: pieceCount,
	}, nil
}

// HasPiece reports availability, returning false for an out-of-range index.
func (b Bitfield) HasPiece(pieceIndex int) bool {
	if pieceIndex < 0 {
		return false
	}

	if pieceIndex >= b.pieceCount {
		return false
	}

	byteIndex := pieceIndex / 8
	bitIndex := pieceIndex % 8
	mask := byte(1 << (7 - bitIndex))

	return (b.bits[byteIndex] & mask) == mask
}

// SetPiece marks an in-range piece available.
//
// SetPiece is idempotent.
func (b *Bitfield) SetPiece(pieceIndex int) error {
	if pieceIndex < 0 {
		return errors.New("piece index cannot be negative")
	}

	if pieceIndex >= b.pieceCount {
		return fmt.Errorf("piece index %v is out of range %v", pieceIndex, b.pieceCount)
	}

	byteIndex := pieceIndex / 8
	bitIndex := pieceIndex % 8
	mask := byte(1 << (7 - bitIndex))

	b.bits[byteIndex] |= mask
	return nil
}
