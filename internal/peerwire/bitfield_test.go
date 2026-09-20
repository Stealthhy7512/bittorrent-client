package peerwire

import (
	"fmt"
	"testing"
)

func TestBitfieldHasPieceUsesHighBitFirst(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0b10100001}, 8)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	tests := []struct {
		pieceIndex int
		want       bool
	}{
		{pieceIndex: 0, want: true},
		{pieceIndex: 1, want: false},
		{pieceIndex: 2, want: true},
		{pieceIndex: 6, want: false},
		{pieceIndex: 7, want: true},
		{pieceIndex: 8, want: false},
	}

	for _, test := range tests {
		if got := bitfield.HasPiece(test.pieceIndex); got != test.want {
			t.Errorf("HasPiece(%d) = %v, want %v", test.pieceIndex, got, test.want)
		}
	}
}

func TestBitfieldHasPieceCrossesByteBoundary(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0, 0b10000000}, 9)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	if !bitfield.HasPiece(8) {
		t.Fatal("HasPiece(8) = false, want true")
	}
	if bitfield.HasPiece(9) {
		t.Fatal("HasPiece(9) = true, want false")
	}
}

func TestBitfieldHasPieceRejectsNegativeIndex(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0b10000000}, 1)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	for _, test := range []struct {
		name       string
		pieceIndex int
	}{
		{name: "immediately below zero", pieceIndex: -1},
		{name: "negative byte index", pieceIndex: -8},
	} {
		t.Run(test.name, func(t *testing.T) {
			if bitfield.HasPiece(test.pieceIndex) {
				t.Fatalf("HasPiece(%d) = true, want false", test.pieceIndex)
			}
		})
	}
}

func TestZeroValueBitfieldHasNoPieces(t *testing.T) {
	var bitfield Bitfield

	if bitfield.HasPiece(0) {
		t.Fatal("HasPiece(0) = true, want false")
	}
}

func TestParseBitfieldAcceptsTwentyPiecePayload(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0, 0, 0b11110000}, 20)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	for pieceIndex := 16; pieceIndex < 20; pieceIndex++ {
		if !bitfield.HasPiece(pieceIndex) {
			t.Errorf("HasPiece(%d) = false, want true", pieceIndex)
		}
	}
	if bitfield.HasPiece(20) {
		t.Fatal("HasPiece(20) = true, want false")
	}
}

func TestParseBitfieldRejectsIncorrectLength(t *testing.T) {
	tests := []struct {
		name       string
		payload    []byte
		pieceCount int
	}{
		{name: "too short", payload: []byte{0, 0}, pieceCount: 20},
		{name: "too long", payload: []byte{0, 0, 0, 0}, pieceCount: 20},
		{name: "data for zero pieces", payload: []byte{0}, pieceCount: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseBitfield(test.payload, test.pieceCount); err == nil {
				t.Fatal("ParseBitfield() error = nil, want invalid-length error")
			}
		})
	}
}

func TestParseBitfieldRejectsNonzeroSpareBits(t *testing.T) {
	tests := []struct {
		name       string
		payload    []byte
		pieceCount int
	}{
		{name: "one piece", payload: []byte{0b11000000}, pieceCount: 1},
		{name: "nine pieces", payload: []byte{0, 0b11000000}, pieceCount: 9},
		{name: "twenty pieces", payload: []byte{0, 0, 0b11110001}, pieceCount: 20},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseBitfield(test.payload, test.pieceCount); err == nil {
				t.Fatal("ParseBitfield() error = nil, want nonzero-spare-bits error")
			}
		})
	}
}

func TestParseBitfieldAllowsAllBitsWhenByteAligned(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0xff}, 8)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	for pieceIndex := 0; pieceIndex < 8; pieceIndex++ {
		if !bitfield.HasPiece(pieceIndex) {
			t.Errorf("HasPiece(%d) = false, want true", pieceIndex)
		}
	}
}

func TestParseBitfieldAcceptsZeroPieces(t *testing.T) {
	bitfield, err := ParseBitfield(nil, 0)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}
	if bitfield.HasPiece(0) {
		t.Fatal("HasPiece(0) = true, want false")
	}
}

func TestParseBitfieldRejectsNegativePieceCount(t *testing.T) {
	if _, err := ParseBitfield(nil, -1); err == nil {
		t.Fatal("ParseBitfield() error = nil, want negative-piece-count error")
	}
}

func TestParseBitfieldCopiesPayload(t *testing.T) {
	payload := []byte{0b10000000}
	bitfield, err := ParseBitfield(payload, 1)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	payload[0] = 0
	if !bitfield.HasPiece(0) {
		t.Fatal("HasPiece(0) = false after input mutation, want true")
	}
}

func TestSetPieceModifiesBitfieldCorrectly(t *testing.T) {
	for _, pieceIndex := range []int{0, 7, 8} {
		t.Run(fmt.Sprintf("piece %d", pieceIndex), func(t *testing.T) {
			bitfield, err := ParseBitfield([]byte{0, 0}, 9)
			if err != nil {
				t.Fatalf("ParseBitfield() error = %v", err)
			}
			if err := bitfield.SetPiece(pieceIndex); err != nil {
				t.Fatalf("SetPiece(%d) error = %v", pieceIndex, err)
			}

			for candidate := range 9 {
				if got, want := bitfield.HasPiece(candidate), candidate == pieceIndex; got != want {
					t.Fatalf("HasPiece(%d) = %t after SetPiece(%d), want %t", candidate, got, pieceIndex, want)
				}
			}
		})
	}
}

func TestSetPieceSetsLastPieceInPartialByte(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0, 0, 0}, 20)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}
	if err := bitfield.SetPiece(19); err != nil {
		t.Fatalf("SetPiece(19) error = %v", err)
	}

	if !bitfield.HasPiece(19) {
		t.Fatal("HasPiece(19) = false after SetPiece(19), want true")
	}
	if bitfield.HasPiece(18) {
		t.Fatal("HasPiece(18) = true after SetPiece(19), want false")
	}
}

func TestSetPieceIsIdempotent(t *testing.T) {
	bitfield, err := ParseBitfield([]byte{0}, 8)
	if err != nil {
		t.Fatalf("ParseBitfield() error = %v", err)
	}

	for range 2 {
		if err := bitfield.SetPiece(3); err != nil {
			t.Fatalf("SetPiece(3) error = %v", err)
		}
	}
	for pieceIndex := range 8 {
		if got, want := bitfield.HasPiece(pieceIndex), pieceIndex == 3; got != want {
			t.Fatalf("HasPiece(%d) = %t after setting Piece 3 twice, want %t", pieceIndex, got, want)
		}
	}
}

func TestSetPieceRejectsInvalidIndexWithoutMutation(t *testing.T) {
	tests := []struct {
		name       string
		pieceIndex int
	}{
		{name: "negative", pieceIndex: -1},
		{name: "equal to piece count", pieceIndex: 9},
		{name: "greater than piece count", pieceIndex: 10},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bitfield, err := ParseBitfield([]byte{0b10100000, 0}, 9)
			if err != nil {
				t.Fatalf("ParseBitfield() error = %v", err)
			}

			if err := bitfield.SetPiece(test.pieceIndex); err == nil {
				t.Fatalf("SetPiece(%d) error = nil, want invalid-index error", test.pieceIndex)
			}
			for pieceIndex := range 9 {
				got := bitfield.HasPiece(pieceIndex)
				want := pieceIndex == 0 || pieceIndex == 2
				if got != want {
					t.Fatalf("HasPiece(%d) = %t after rejected SetPiece(%d), want %t", pieceIndex, got, test.pieceIndex, want)
				}
			}
		})
	}
}

func TestSetPieceRejectsIndexOnZeroValueBitfield(t *testing.T) {
	var bitfield Bitfield

	if err := bitfield.SetPiece(0); err == nil {
		t.Fatal("SetPiece(0) error = nil for zero-value Bitfield, want invalid-index error")
	}
}
