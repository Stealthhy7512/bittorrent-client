package metainfo

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

const maxBencodeDepth = 512

// Open reads and parses a .torrent metainfo file.
func Open(path string) (TorrentFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TorrentFile{}, fmt.Errorf("read torrent file: %w", err)
	}

	rawInfo, err := extractInfoBytes(data)
	if err != nil {
		return TorrentFile{}, fmt.Errorf("extract info dictionary: %w", err)
	}

	meta, err := decodeMetaInfo(data)
	if err != nil {
		return TorrentFile{}, err
	}

	return meta.toTorrentFile(sha1.Sum(rawInfo))
}

func decodeMetaInfo(data []byte) (MetaInfo, error) {
	var meta MetaInfo
	if err := bencode.Unmarshal(bytes.NewReader(data), &meta); err != nil {
		return MetaInfo{}, fmt.Errorf("decode torrent file: %w", err)
	}
	return meta, nil
}

func (meta MetaInfo) toTorrentFile(infoHash [20]byte) (TorrentFile, error) {
	pieceHashes, err := meta.Info.splitPieceHashes()
	if err != nil {
		return TorrentFile{}, err
	}

	return TorrentFile{
		Announce:    meta.Announce,
		InfoHash:    infoHash,
		PieceHashes: pieceHashes,
		PieceLength: meta.Info.PieceLength,
		Length:      meta.Info.Length,
		Name:        meta.Info.Name,
	}, nil
}

func (info Info) splitPieceHashes() ([][20]byte, error) {
	const hashLength = 20

	pieces := []byte(info.Pieces)
	if len(pieces)%hashLength != 0 {
		return nil, fmt.Errorf("pieces field is %d bytes, not a multiple of %d", len(pieces), hashLength)
	}

	hashes := make([][20]byte, len(pieces)/hashLength)
	for i := range hashes {
		copy(hashes[i][:], pieces[i*hashLength:(i+1)*hashLength])
	}
	return hashes, nil
}

// extractInfoBytes returns the exact encoded bytes of the top-level info value.
// It walks Bencode boundaries only; normal field decoding remains the job of
// bencode-go. The original encoding is necessary for a v1 info hash.
func extractInfoBytes(data []byte) ([]byte, error) {
	s := rawScanner{data: data}
	if !s.consume('d') {
		return nil, fmt.Errorf("top-level value is not a dictionary")
	}

	var rawInfo []byte
	for {
		if s.atEnd() {
			return nil, fmt.Errorf("unterminated top-level dictionary")
		}

		if s.consume('e') {
			break
		}

		key, err := s.readByteString()
		if err != nil {
			return nil, fmt.Errorf("read dictionary key: %w", err)
		}

		valueStart := s.pos

		if err := s.skipValue(1); err != nil {
			return nil, fmt.Errorf("skip value for key %q: %w", key, err)
		}

		if string(key) != "info" {
			continue
		}

		if rawInfo != nil {
			return nil, fmt.Errorf("duplicate info key")
		}

		if data[valueStart] != 'd' {
			return nil, fmt.Errorf("info value is not a dictionary")
		}

		rawInfo = data[valueStart:s.pos]
	}

	if rawInfo == nil {
		return nil, fmt.Errorf("missing info key")
	}

	return rawInfo, nil
}

type rawScanner struct {
	data []byte
	pos  int
}

func (s *rawScanner) skipValue(depth int) error {
	if depth > maxBencodeDepth {
		return fmt.Errorf("bencode nesting exceeds %d levels", maxBencodeDepth)
	}
	if s.atEnd() {
		return fmt.Errorf("unexpected end of input")
	}

	switch s.data[s.pos] {
	case 'i':
		return s.skipInteger()
	case 'l':
		return s.skipList(depth)
	case 'd':
		return s.skipDictionary(depth)
	default:
		if isDigit(s.data[s.pos]) {
			_, err := s.readByteString()
			return err
		}
		return fmt.Errorf("invalid value prefix %q", s.data[s.pos])
	}
}

func (s *rawScanner) skipInteger() error {
	s.pos++ // i
	start := s.pos
	for !s.atEnd() && s.data[s.pos] != 'e' {
		s.pos++
	}

	if s.atEnd() {
		return fmt.Errorf("unterminated integer")
	}

	if s.pos == start {
		return fmt.Errorf("empty integer")
	}

	s.pos++ // e
	return nil
}

func (s *rawScanner) skipList(depth int) error {
	s.pos++ // l
	for {
		if s.atEnd() {
			return fmt.Errorf("unterminated list")
		}

		if s.consume('e') {
			return nil
		}

		if err := s.skipValue(depth + 1); err != nil {
			return err
		}
	}
}

func (s *rawScanner) skipDictionary(depth int) error {
	s.pos++ // d
	for {
		if s.atEnd() {
			return fmt.Errorf("unterminated dictionary")
		}

		if s.consume('e') {
			return nil
		}

		if _, err := s.readByteString(); err != nil {
			return fmt.Errorf("read dictionary key: %w", err)
		}

		if err := s.skipValue(depth + 1); err != nil {
			return err
		}
	}
}

func (s *rawScanner) readByteString() ([]byte, error) {
	if s.atEnd() || !isDigit(s.data[s.pos]) {
		return nil, fmt.Errorf("expected byte-string length")
	}

	var length uint64
	for !s.atEnd() && isDigit(s.data[s.pos]) {
		digit := uint64(s.data[s.pos] - '0')
		if length > (^uint64(0)-digit)/10 {
			return nil, fmt.Errorf("byte-string length overflows")
		}

		length = length*10 + digit
		s.pos++
	}

	if s.atEnd() || !s.consume(':') {
		return nil, fmt.Errorf("byte-string length is missing ':'")
	}

	if length > uint64(len(s.data)-s.pos) {
		return nil, fmt.Errorf("byte string exceeds input")
	}

	end := s.pos + int(length)
	value := s.data[s.pos:end]
	s.pos = end

	return value, nil
}

func (s *rawScanner) atEnd() bool { return s.pos >= len(s.data) }

func (s *rawScanner) consume(want byte) bool {
	if s.atEnd() || s.data[s.pos] != want {
		return false
	}

	s.pos++

	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
