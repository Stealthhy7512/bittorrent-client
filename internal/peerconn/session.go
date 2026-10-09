package peerconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

// Session tracks one remote peer's choke state and advertised pieces.
// Its methods are intended for sequential use, not concurrent access.
type Session struct {
	conn            net.Conn
	choked          bool
	pieceCount      int
	peerPieces      peerwire.Bitfield
	bitfieldAllowed bool
}

// PieceSpec identifies the requested piece and its actual byte length.
// The caller derives both fields from validated metainfo.
type PieceSpec struct {
	Index  uint32
	Length uint64
}

var (
	// ErrPieceUnavailable means the peer did not advertise the requested piece.
	ErrPieceUnavailable = errors.New("peer does not have the requested piece")
	// ErrPeerProtocol means the peer sent an invalid or unexpected block.
	ErrPeerProtocol = errors.New("peer protocol violation")
	// ErrPeerChoked means this single-block transfer stopped after a choke.
	ErrPeerChoked = errors.New("peer choked before completing the piece")
)

// NewSession wraps an already handshaked connection, initially treating the peer
// as choked with no advertised pieces. It performs no I/O. The caller owns
// connection deadlines and cleanup, including when construction fails.
func NewSession(conn net.Conn, pieceCount int) (*Session, error) {
	if conn == nil {
		return nil, errors.New("connection does not exist")
	}

	bitfield, err := peerwire.NewBitfield(pieceCount)
	if err != nil {
		return nil, err
	}

	return &Session{
		conn:            conn,
		choked:          true,
		pieceCount:      pieceCount,
		peerPieces:      bitfield,
		bitfieldAllowed: true,
	}, nil
}

// Read skips keep-alives and returns the next message after applying Choke,
// Unchoke, Bitfield, or Have updates. It validates advertised piece indexes and
// accepts a Bitfield only before any other non-keep-alive supported message.
//
// Other message IDs are currently returned without session-level processing.
// A read or protocol error ends the usable session: the caller should close the
// connection, since an invalid frame can leave unread bytes in the stream.
func (s *Session) Read() (peerwire.Message, error) {
	for {
		frame, err := peerwire.ReadFrame(s.conn, s.pieceCount)
		if err != nil {
			return peerwire.Message{}, err
		}

		if frame.IsKeepAlive() {
			continue
		}

		if frame.IsUnsupported() {
			continue
		}

		message, ok := frame.Message()
		if !ok {
			return peerwire.Message{}, errors.New("invalid peer frame")
		}

		if message.ID == peerwire.MessageBitfield {
			if !s.bitfieldAllowed {
				return peerwire.Message{}, errors.New(
					"bitfield must be the first peer message",
				)
			}
		}

		// any non keep-alive message closes the bitfield window
		s.bitfieldAllowed = false

		switch message.ID {
		case peerwire.MessageChoke:
			s.choked = true
		case peerwire.MessageUnchoke:
			s.choked = false
		case peerwire.MessageBitfield:
			bitfield, err := peerwire.ParseBitfield(
				message.Payload,
				s.pieceCount,
			)
			if err != nil {
				return peerwire.Message{}, err
			}
			s.peerPieces = bitfield
		case peerwire.MessageHave:
			index, err := peerwire.ParseHave(message)
			if err != nil {
				return peerwire.Message{}, err
			}
			if uint64(index) >= uint64(s.pieceCount) {
				return peerwire.Message{}, fmt.Errorf("have piece index %d is out of range %d", index, s.pieceCount)
			}
			if err := s.peerPieces.SetPiece(int(index)); err != nil {
				return peerwire.Message{}, fmt.Errorf("apply have: %w", err)
			}
		}
		return message, nil
	}
}

// FetchPiece obtains a single-block piece from this peer. It calls onBlock
// synchronously after validating the response; the caller verifies the piece
// hash. Multi-block pipelining and mid-transfer choke recovery are added later.
func (s *Session) FetchPiece(
	ctx context.Context,
	piece PieceSpec,
	onBlock func(offset uint32, block []byte) error,
) error {
	if ctx == nil {
		return errors.New("context does not exist")
	}
	if onBlock == nil {
		return errors.New("block callback does not exist")
	}
	if uint64(piece.Index) >= uint64(s.pieceCount) {
		return fmt.Errorf("piece index %d is out of range %d", piece.Index, s.pieceCount)
	}
	pieceIndex := int(piece.Index)
	if piece.Length == 0 {
		return errors.New("piece length must be positive")
	}
	if piece.Length > peerwire.MaxBlockSize {
		return fmt.Errorf("single-block piece length %d exceeds %d", piece.Length, peerwire.MaxBlockSize)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// A single-block transfer has no intermediate Block progress that could
	// refresh its inactivity budget. The parent context may expire sooner.
	sessionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stopWatching, err := watchContext(sessionCtx, s.conn)
	if err != nil {
		return fmt.Errorf("watch session context: %w", err)
	}
	defer stopWatching()

	read := func() (peerwire.Message, error) {
		message, err := s.Read()
		if err != nil {
			if ctxErr := sessionCtx.Err(); ctxErr != nil {
				return peerwire.Message{}, ctxErr
			}
		}
		return message, err
	}
	writeError := func(err error) error {
		if ctxErr := sessionCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}

	// check if piece is read before
	if !s.HasPiece(pieceIndex) {
		message, err := read()
		if err != nil {
			return fmt.Errorf("read piece advertisement: %w", err)
		}
		if (message.ID != peerwire.MessageBitfield && message.ID != peerwire.MessageHave) ||
			!s.HasPiece(pieceIndex) {
			return ErrPieceUnavailable
		}
	}

	if _, err := peerwire.WriteInterested(s.conn); err != nil {
		return fmt.Errorf("write interested: %w", writeError(err))
	}

	// wait for unchoke
	for s.Choked() {
		message, err := read()
		if err != nil {
			return fmt.Errorf("wait for unchoke: %w", err)
		}
		if message.ID == peerwire.MessagePiece {
			return fmt.Errorf("%w: received block before requesting it", ErrPeerProtocol)
		}
	}

	// request the piece
	if _, err := peerwire.WriteRequest(s.conn, piece.Index, 0, uint32(piece.Length)); err != nil {
		return fmt.Errorf("write request: %w", writeError(err))
	}
	for {
		message, err := read()
		if err != nil {
			return fmt.Errorf("read requested block: %w", err)
		}

		switch message.ID {
		case peerwire.MessageChoke:
			return ErrPeerChoked
		case peerwire.MessagePiece:
			index, offset, block, err := peerwire.ParsePiece(message)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrPeerProtocol, err)
			}
			if index != piece.Index || offset != 0 || uint64(len(block)) != piece.Length {
				return fmt.Errorf("%w: block (%d, %d, %d), want (%d, 0, %d)",
					ErrPeerProtocol, index, offset, len(block), piece.Index, piece.Length)
			}
			if ctxErr := sessionCtx.Err(); ctxErr != nil {
				return ctxErr
			}

			// finally handle request response
			if err := onBlock(0, block); err != nil {
				return fmt.Errorf("accept block: %w", err)
			}
			return nil
		default:
			// TODO: check other messages
		}
	}
}

func (s *Session) Choked() bool {
	return s.choked
}

func (s *Session) HasPiece(index int) bool {
	return s.peerPieces.HasPiece(index)
}
