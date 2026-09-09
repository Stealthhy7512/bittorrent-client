package metainfo

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRejectsNonPositivePieceLength(t *testing.T) {
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi0e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	torrent := "d8:announce14:http://tracker4:info" + rawInfo + "e"
	path := filepath.Join(t.TempDir(), "invalid-piece-length.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "piece length must be positive") {
		t.Fatalf("Open() error = %v, want positive piece length error", err)
	}
}

func TestOpenRejectsInconsistentPieceCount(t *testing.T) {
	rawInfo := "d6:lengthi5e4:name4:test12:piece lengthi4e6:pieces20:aaaaaaaaaaaaaaaaaaaae"
	torrent := "d8:announce14:http://tracker4:info" + rawInfo + "e"
	path := filepath.Join(t.TempDir(), "invalid-piece-count.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "piece hash count") {
		t.Fatalf("Open() error = %v, want piece hash count error", err)
	}
}

func TestOpenRejectsNegativeContentLength(t *testing.T) {
	rawInfo := "d6:lengthi-1e4:name4:test12:piece lengthi4e6:pieces0:e"
	torrent := "d8:announce14:http://tracker4:info" + rawInfo + "e"
	path := filepath.Join(t.TempDir(), "negative-length.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	if err == nil || !strings.Contains(err.Error(), "length must not be negative") {
		t.Fatalf("Open() error = %v, want negative length error", err)
	}
}

func TestOpenHashesOriginalInfoBytes(t *testing.T) {
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces20:abcdefghijklmnopqrste"
	torrent := "d8:announce14:http://tracker4:info" + rawInfo + "e"
	path := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantHash := sha1.Sum([]byte(rawInfo))
	if got.InfoHash != wantHash {
		t.Fatalf("InfoHash = %x, want %x", got.InfoHash, wantHash)
	}
}

func TestOpenHashesInfoBytesWithUnknownFields(t *testing.T) {
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces20:abcdefghijklmnopqrst7:privatei1ee"
	torrent := "d8:announce14:http://tracker4:info" + rawInfo + "e"
	path := filepath.Join(t.TempDir(), "private.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantHash := sha1.Sum([]byte(rawInfo))
	if got.InfoHash != wantHash {
		t.Fatalf("InfoHash = %x, want %x", got.InfoHash, wantHash)
	}
}
