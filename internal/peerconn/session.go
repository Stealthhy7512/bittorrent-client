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
	Length uint32
}

var (
	// ErrPieceUnavailable means the peer's Bitfield excludes the requested piece
	// for this transfer attempt. The peer may announce it later with Have.
	ErrPieceUnavailable = errors.New("peer does not have the requested piece")
	// ErrPeerProtocol reports a well-formed message that violates session state
	// or does not match the outstanding request.
	ErrPeerProtocol = errors.New("peer protocol violation")
	// ErrPeerChoked means the peer choked before completing the piece.
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
// Other supported message IDs are returned without session-level processing;
// bounded unsupported IDs are skipped.
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
				return peerwire.Message{}, fmt.Errorf(
					"%w: bitfield must be the first peer message",
					ErrPeerProtocol,
				)
			}
		}

		// any supported non-keep-alive message closes the Bitfield window
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
				return peerwire.Message{}, fmt.Errorf(
					"%w: have piece index %d is out of range %d",
					ErrPeerProtocol,
					index,
					s.pieceCount,
				)
			}
			if err := s.peerPieces.SetPiece(int(index)); err != nil {
				return peerwire.Message{}, fmt.Errorf("apply have: %w", err)
			}
		}
		return message, nil
	}
}

// FetchPiece obtains a piece from this peer in blocks of at most 16 KiB. It
// waits for a Have or Bitfield advertising the piece; a Bitfield that excludes
// it ends this attempt. It calls onBlock after validating each response; the
// caller verifies the piece hash after all blocks are delivered.
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
		return fmt.Errorf(
			"piece index %d is out of range %d",
			piece.Index,
			s.pieceCount,
		)
	}
	if piece.Length == 0 {
		return errors.New("piece length must be positive")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := s.waitUntilReady(ctx, int(piece.Index)); err != nil {
		return err
	}

	// request each block
	for blockOffset := uint32(0); blockOffset < piece.Length; {
		length := min(peerwire.MaxBlockSize, piece.Length-blockOffset)

		// manage contexts and watcher per block
		err := func() error {
			blockCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()

			stopWatching, err := watchContext(blockCtx, s.conn)
			if err != nil {
				return fmt.Errorf("watch block context: %w", err)
			}
			defer stopWatching()

			if err := blockCtx.Err(); err != nil {
				return err
			}
			if _, err := peerwire.WriteRequest(s.conn, piece.Index, blockOffset, length); err != nil {
				return fmt.Errorf("write request: %w", contextIOError(blockCtx, err))
			}

		InnerLoop:
			for {
				message, err := s.readWithContext(blockCtx)
				if err != nil {
					return fmt.Errorf("read block context error: %w", err)
				}

				switch message.ID {
				case peerwire.MessageChoke:
					return ErrPeerChoked
				case peerwire.MessagePiece:
					index, begin, block, err := peerwire.ParsePiece(message)
					if err != nil {
						return fmt.Errorf("parse piece: %w", err)
					}
					if index != piece.Index || begin != uint32(blockOffset) || len(block) != int(length) {
						return fmt.Errorf("%w: block (%v, %v, %v), want (%v, %v, %v)",
							ErrPeerProtocol, index, begin, len(block), piece.Index, blockOffset, length)
					}
					if err := blockCtx.Err(); err != nil {
						return err
					}
					if err := onBlock(blockOffset, block); err != nil {
						return fmt.Errorf("accept block: %w", err)
					}

					break InnerLoop
				default:
					// other messages do not complete the outstanding request,
					// keep reading
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}

		// update block offset for writing
		blockOffset += length
	}

	return nil
}

func (s *Session) Choked() bool {
	return s.choked
}

func (s *Session) HasPiece(index int) bool {
	return s.peerPieces.HasPiece(index)
}

// waitUntilReady waits for the peer to advertise pieceIndex, then sends
// Interested and waits for Unchoke.
func (s *Session) waitUntilReady(ctx context.Context, pieceIndex int) error {
	setupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	stopWatching, err := watchContext(setupCtx, s.conn)
	if err != nil {
		return fmt.Errorf("watch setup context: %w", err)
	}
	defer stopWatching()

	// Wait for the peer to advertise the requested piece.
	for !s.HasPiece(pieceIndex) {
		message, err := s.readWithContext(setupCtx)
		if err != nil {
			return fmt.Errorf("read piece advertisement: %w", err)
		}
		switch message.ID {
		case peerwire.MessageBitfield:
			if !s.HasPiece(pieceIndex) {
				return ErrPieceUnavailable
			}
		case peerwire.MessagePiece:
			return fmt.Errorf("%w: received block before requesting", ErrPeerProtocol)
		}
	}

	if _, err := peerwire.WriteInterested(s.conn); err != nil {
		return fmt.Errorf("write interested: %w", contextIOError(setupCtx, err))
	}

	// Wait for Unchoke before requesting any blocks.
	for s.Choked() {
		message, err := s.readWithContext(setupCtx)
		if err != nil {
			return fmt.Errorf("wait for unchoke: %w", err)
		}
		if message.ID == peerwire.MessagePiece {
			return fmt.Errorf("%w: received block before requesting it", ErrPeerProtocol)
		}
	}

	return setupCtx.Err()
}

func (s *Session) readWithContext(ctx context.Context) (peerwire.Message, error) {
	message, err := s.Read()
	if err != nil {
		return peerwire.Message{}, contextIOError(ctx, err)
	}
	return message, nil
}

func contextIOError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return err
}
