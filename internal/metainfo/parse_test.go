package metainfo

import (
	"crypto/sha1"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenHashesOriginalInfoBytes(t *testing.T) {
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces0:e"
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
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces0:7:privatei1ee"
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
