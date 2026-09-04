package metainfo

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"

	"github.com/jackpal/bencode-go"
)

type rawMetaInfo struct {
	Announce string             `bencode:"announce"`
	Info     bencode.RawMessage `bencode:"info"`
}

// Open reads and parses a .torrent metainfo file.
func Open(path string) (TorrentFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return TorrentFile{}, fmt.Errorf("read torrent file: %w", err)
	}

	var raw rawMetaInfo
	if err := bencode.Unmarshal(bytes.NewReader(data), &raw); err != nil {
		return TorrentFile{}, fmt.Errorf("decode torrent file: %w", err)
	}

	var info Info
	if err := bencode.Unmarshal(bytes.NewReader(raw.Info), &info); err != nil {
		return TorrentFile{}, fmt.Errorf("decode info dict: %w", err)
	}

	meta := MetaInfo{
		Announce: raw.Announce,
		Info:     info,
	}

	infoHash := sha1.Sum(raw.Info)

	return meta.toTorrentFile(infoHash)
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
