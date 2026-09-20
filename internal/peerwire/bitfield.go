package peerwire

import (
	"errors"
	"fmt"
	"slices"
)

type Bitfield struct {
	bits       []byte
	pieceCount int
}

func ParseBitfield(payload []byte, pieceCount int) (Bitfield, error) {
	if pieceCount < 0 {
		return Bitfield{}, fmt.Errorf("piece count cannot be negative: %v", pieceCount)
	}

	expectedPayloadSize := (pieceCount + 7) / 8

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
