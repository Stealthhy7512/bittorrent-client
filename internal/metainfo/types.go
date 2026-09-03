package metainfo

type MetaInfo struct {
	Announce string `bencode:"announce"`
	Info     Info   `bencode:"info"`
}

type Info struct {
	Length      int64  `bencode:"length"`
	Name        string `bencode:"name"`
	PieceLength int64  `bencode:"piece length"`
	Pieces      string `bencode:"pieces"`
}

type TorrentFile struct {
	Announce    string
	InfoHash    [20]byte
	PieceHashes [][20]byte
	PieceLength int64
	Length      int64
	Name        string
}
