package main

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestDownloadPieceRejectsCorruptPeerData(t *testing.T) {
	metainfoPath, peerDone := startPieceDownloadFixture(t,
		pieceMetainfo(4, 4, sha1.Sum([]byte("good"))),
		1, 0, 0, 4, []byte("evil"),
	)
	outputPath := filepath.Join(t.TempDir(), "piece.bin")

	stdout, err := runWithCapturedStdout(t, []string{
		"download-piece", "--piece", "0", "--output", outputPath,
		"--timeout", "5s", metainfoPath,
	})
	if err == nil {
		t.Fatal("run(download-piece) error = nil, want hash mismatch")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want no success line", stdout)
	}
	if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination after corrupt Block: stat error = %v, want not exist", err)
	}
	if err := waitForPiecePeer(t, peerDone); err != nil {
		t.Fatalf("fake Peer error = %v", err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), ".bt-piece-*")); err != nil {
		t.Fatal(err)
	} else if len(leftovers) != 0 {
		t.Fatalf("temporary files after failure = %v, want none", leftovers)
	}
}

func TestDownloadPieceRequestsActualFinalPieceLength(t *testing.T) {
	metainfoPath, peerDone := startPieceDownloadFixture(t,
		pieceMetainfo(11, 8, sha1.Sum([]byte("12345678")), sha1.Sum([]byte("xyz"))),
		2, 1, 1, 3, []byte("xyz"),
	)
	outputPath := filepath.Join(t.TempDir(), "last-piece.bin")

	_, err := runWithCapturedStdout(t, []string{
		"download-piece", "--piece", "1", "--output", outputPath,
		"--timeout", "5s", metainfoPath,
	})
	if err != nil {
		t.Fatalf("run(download-piece) error = %v", err)
	}
	if err := waitForPiecePeer(t, peerDone); err != nil {
		t.Fatalf("fake Peer error = %v", err)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "xyz" {
		t.Fatalf("published Piece = %q, want %q", got, "xyz")
	}
}

func TestDownloadPieceForceReplacesExistingOutput(t *testing.T) {
	metainfoPath, peerDone := startPieceDownloadFixture(t,
		pieceMetainfo(4, 4, sha1.Sum([]byte("data"))),
		1, 0, 0, 4, []byte("data"),
	)
	outputPath := filepath.Join(t.TempDir(), "piece.bin")
	if err := os.WriteFile(outputPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, err := runWithCapturedStdout(t, []string{
		"download-piece", "--piece", "0", "--output", outputPath,
		"--force", "--timeout", "5s", metainfoPath,
	})
	if err != nil {
		t.Fatalf("run(download-piece --force) error = %v", err)
	}
	if err := waitForPiecePeer(t, peerDone); err != nil {
		t.Fatalf("fake Peer error = %v", err)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "data" {
		t.Fatalf("replaced output = %q, want %q", got, "data")
	}
	if !strings.Contains(stdout, outputPath) || !strings.Contains(stdout, "0") {
		t.Fatalf("stdout = %q, want Piece index and output path", stdout)
	}
}

func TestDownloadPieceRejectsExistingOutputBeforeAnnounce(t *testing.T) {
	var announces atomic.Int32
	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		announces.Add(1)
		http.Error(w, "unexpected announce", http.StatusInternalServerError)
	}))
	t.Cleanup(trackerServer.Close)
	metainfoPath := writeMetainfo(t, trackerServer.URL,
		pieceMetainfo(4, 4, sha1.Sum([]byte("data"))))
	outputPath := filepath.Join(t.TempDir(), "piece.bin")
	if err := os.WriteFile(outputPath, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runWithCapturedStdout(t, []string{
		"download-piece", "--piece", "0", "--output", outputPath,
		"--timeout", "5s", metainfoPath,
	})
	if err == nil {
		t.Fatal("run(download-piece) error = nil, want existing-output error")
	}
	if got := announces.Load(); got != 0 {
		t.Fatalf("Tracker announces = %d, want 0 for existing output", got)
	}
	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "keep" {
		t.Fatalf("existing output = %q, want %q", got, "keep")
	}
}

func TestDownloadPieceForceRefusesToReplaceMetainfo(t *testing.T) {
	var announces atomic.Int32
	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		announces.Add(1)
		http.Error(w, "unexpected announce", http.StatusInternalServerError)
	}))
	t.Cleanup(trackerServer.Close)
	metainfoPath := writeMetainfo(t, trackerServer.URL,
		pieceMetainfo(4, 4, sha1.Sum([]byte("data"))))
	outputPath := filepath.Join(t.TempDir(), "metainfo-alias.torrent")
	if err := os.Link(metainfoPath, outputPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	original, err := os.ReadFile(metainfoPath)
	if err != nil {
		t.Fatal(err)
	}

	_, err = runWithCapturedStdout(t, []string{
		"download-piece", "--piece", "0", "--output", outputPath,
		"--force", "--timeout", "5s", metainfoPath,
	})
	if err == nil {
		t.Fatal("run(download-piece --force) error = nil, want Metainfo protection error")
	}
	if got := announces.Load(); got != 0 {
		t.Fatalf("Tracker announces = %d, want 0 when output is Metainfo", got)
	}
	got, err := os.ReadFile(metainfoPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("Metainfo changed after rejected output path")
	}
	got, err = os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatal("Metainfo alias changed after rejected output path")
	}
}

func pieceMetainfo(contentLength, pieceLength int, hashes ...[sha1.Size]byte) string {
	pieces := make([]byte, 0, len(hashes)*sha1.Size)
	for _, hash := range hashes {
		pieces = append(pieces, hash[:]...)
	}
	return fmt.Sprintf("d6:lengthi%de4:name4:test12:piece lengthi%de6:pieces%d:%se",
		contentLength, pieceLength, len(pieces), string(pieces))
}

func waitForPiecePeer(t *testing.T, peerDone <-chan error) error {
	t.Helper()
	select {
	case err := <-peerDone:
		return err
	case <-time.After(time.Second):
		t.Fatal("fake Peer did not finish after command returned")
		return nil
	}
}

func startPieceDownloadFixture(
	t *testing.T,
	rawInfo string,
	pieceCount, availableIndex int,
	wantIndex, wantLength uint32,
	block []byte,
) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	infoHash := sha1.Sum([]byte(rawInfo))
	peerDone := make(chan error, 1)
	go func() {
		peerDone <- func() error {
			conn, err := listener.Accept()
			if err != nil {
				return err
			}
			defer conn.Close()
			if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				return err
			}
			handshake, err := peerwire.ReadHandshake(conn)
			if err != nil {
				return fmt.Errorf("read Handshake: %w", err)
			}
			if handshake.InfoHash != infoHash {
				return fmt.Errorf("Handshake info hash = %x, want %x", handshake.InfoHash, infoHash)
			}
			if _, err := (peerwire.Handshake{InfoHash: infoHash, PeerID: [20]byte{9}}).WriteTo(conn); err != nil {
				return fmt.Errorf("write Handshake: %w", err)
			}
			bitfield := make([]byte, (pieceCount+7)/8)
			bitfield[availableIndex/8] = 1 << (7 - availableIndex%8)
			if _, err := (peerwire.Message{ID: peerwire.MessageBitfield, Payload: bitfield}).WriteTo(conn); err != nil {
				return fmt.Errorf("advertise Piece: %w", err)
			}
			frame, err := peerwire.ReadFrame(conn, pieceCount)
			if err != nil {
				return fmt.Errorf("read Interested: %w", err)
			}
			message, ok := frame.Message()
			if !ok || message.ID != peerwire.MessageInterested {
				return fmt.Errorf("received %#v, want Interested", frame)
			}
			if _, err := peerwire.WriteUnchoke(conn); err != nil {
				return fmt.Errorf("write Unchoke: %w", err)
			}
			frame, err = peerwire.ReadFrame(conn, pieceCount)
			if err != nil {
				return fmt.Errorf("read Request: %w", err)
			}
			message, ok = frame.Message()
			if !ok {
				return fmt.Errorf("received %#v, want Request", frame)
			}
			index, offset, length, err := peerwire.ParseRequest(message)
			if err != nil {
				return fmt.Errorf("parse Request: %w", err)
			}
			if index != wantIndex || offset != 0 || length != wantLength {
				return fmt.Errorf("Request = (%d, %d, %d), want (%d, 0, %d)",
					index, offset, length, wantIndex, wantLength)
			}
			if _, err := peerwire.WritePiece(conn, wantIndex, 0, block); err != nil {
				return fmt.Errorf("write Block: %w", err)
			}
			return nil
		}()
	}()

	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("event") {
		case "started":
			response := []byte("d8:intervali60e5:peers6:")
			response = appendCompactAddr(response, listener.Addr().(*net.TCPAddr))
			response = append(response, 'e')
			_, _ = w.Write(response)
		case "stopped":
			_, _ = io.WriteString(w, "d8:intervali60e5:peers0:e")
		default:
			http.Error(w, "expected started or stopped announce", http.StatusBadRequest)
		}
	}))
	t.Cleanup(trackerServer.Close)
	return writeMetainfo(t, trackerServer.URL, rawInfo), peerDone
}

func runWithCapturedStdout(t *testing.T, args []string) (string, error) {
	t.Helper()
	previousStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = previousStdout
		_ = reader.Close()
		_ = writer.Close()
	}()

	runErr := run(args)
	os.Stdout = previousStdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(stdout), runErr
}
