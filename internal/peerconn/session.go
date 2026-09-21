package peerconn

import (
	"errors"
	"fmt"
	"net"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

type Session struct {
	conn            net.Conn
	choked          bool
	pieceCount      int
	peerPieces      peerwire.Bitfield
	bitfieldAllowed bool
}

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

func (s *Session) Read() (peerwire.Message, error) {
	for {
		frame, err := peerwire.ReadFrame(s.conn)
		if err != nil {
			return peerwire.Message{}, err
		}

		if frame.IsKeepAlive() {
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
			if len(message.Payload) != 0 {
				return peerwire.Message{}, errors.New("choke message has payload")
			}
			s.choked = true

		case peerwire.MessageUnchoke:
			if len(message.Payload) != 0 {
				return peerwire.Message{}, errors.New("unchoke message has payload")
			}
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
			if err := s.peerPieces.SetPiece(int(index)); err != nil {
				return peerwire.Message{}, fmt.Errorf("apply have: %w", err)
			}
		}
		return message, nil
	}
}

func (s *Session) Choked() bool {
	return s.choked
}

func (s *Session) HasPiece(index int) bool {
	return s.peerPieces.HasPiece(index)
}
