package main

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestHandshakeUsageDocumentsTimeout(t *testing.T) {
	previousStderr := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	t.Cleanup(func() {
		os.Stderr = previousStderr
		reader.Close()
		writer.Close()
	})

	err = handshake(nil)
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	os.Stderr = previousStderr
	usage, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}

	if err == nil {
		t.Fatal("handshake() error = nil, want missing metainfo error")
	}
	if !strings.Contains(string(usage), "[--timeout DURATION]") {
		t.Fatalf("usage = %q, want documented timeout flag", usage)
	}
}

func TestHandshakeCommandDoesNotWaitForSlowPeer(t *testing.T) {
	slowListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slowListener.Close() })
	go func() {
		conn, err := slowListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := peerwire.ReadHandshake(conn); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, conn)
	}()

	fastListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fastListener.Close() })

	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces20:abcdefghijklmnopqrste"
	infoHash := sha1.Sum([]byte(rawInfo))
	peerDone := make(chan error, 1)
	go func() {
		conn, err := fastListener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer conn.Close()

		if _, err := peerwire.ReadHandshake(conn); err != nil {
			peerDone <- err
			return
		}
		_, err = (peerwire.Handshake{InfoHash: infoHash, PeerID: [20]byte{9}}).WriteTo(conn)
		peerDone <- err
	}()

	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := []byte("d8:intervali60e5:peers12:")
		response = appendCompactAddr(response, slowListener.Addr().(*net.TCPAddr))
		response = appendCompactAddr(response, fastListener.Addr().(*net.TCPAddr))
		response = append(response, 'e')
		_, _ = w.Write(response)
	}))
	t.Cleanup(trackerServer.Close)

	path := writeMetainfo(t, trackerServer.URL, rawInfo)
	if err := run([]string{"handshake", "--timeout", "200ms", path}); err != nil {
		t.Fatalf("run(handshake) error = %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("fake peer error = %v", err)
	}
}

func TestHandshakeCommandCompletesAgainstLocalTrackerAndPeer(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces20:abcdefghijklmnopqrste"
	infoHash := sha1.Sum([]byte(rawInfo))
	remoteID := [20]byte{1, 2, 3, 4}
	peerDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			peerDone <- err
			return
		}
		defer conn.Close()

		received, err := peerwire.ReadHandshake(conn)
		if err != nil {
			peerDone <- err
			return
		}
		if received.InfoHash != infoHash {
			peerDone <- fmt.Errorf("info hash = %x, want %x", received.InfoHash, infoHash)
			return
		}
		_, err = (peerwire.Handshake{InfoHash: infoHash, PeerID: remoteID}).WriteTo(conn)
		peerDone <- err
	}()

	trackerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("event"); got != "started" {
			http.Error(w, fmt.Sprintf("event = %q, want started", got), http.StatusBadRequest)
			return
		}

		port := uint16(listener.Addr().(*net.TCPAddr).Port)
		response := []byte("d8:intervali60e5:peers6:")
		response = append(response, 127, 0, 0, 1, 0, 0)
		binary.BigEndian.PutUint16(response[len(response)-2:], port)
		response = append(response, 'e')
		_, _ = w.Write(response)
	}))
	t.Cleanup(trackerServer.Close)

	path := writeMetainfo(t, trackerServer.URL, rawInfo)

	if err := run([]string{"handshake", "--timeout", time.Second.String(), path}); err != nil {
		t.Fatalf("run(handshake) error = %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatalf("fake peer error = %v", err)
	}
}

func TestDownloadPieceCommandPublishesVerifiedSingleBlock(t *testing.T) {
	const pieceData = "data"
	pieceHash := sha1.Sum([]byte(pieceData))
	rawInfo := "d6:lengthi4e4:name4:test12:piece lengthi4e6:pieces20:" + string(pieceHash[:]) + "e"
	infoHash := sha1.Sum([]byte(rawInfo))

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	outputPath := filepath.Join(t.TempDir(), "piece.bin")
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
			if _, err := (peerwire.Message{ID: peerwire.MessageBitfield, Payload: []byte{0x80}}).WriteTo(conn); err != nil {
				return fmt.Errorf("advertise Piece: %w", err)
			}

			frame, err := peerwire.ReadFrame(conn, 1)
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

			frame, err = peerwire.ReadFrame(conn, 1)
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
			if index != 0 || offset != 0 || length != uint32(len(pieceData)) {
				return fmt.Errorf("Request = (%d, %d, %d), want (0, 0, %d)", index, offset, length, len(pieceData))
			}
			if _, err := os.Stat(outputPath); !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("destination existed before Piece response: %v", err)
			}
			if _, err := peerwire.WritePiece(conn, 0, 0, []byte(pieceData)); err != nil {
				return fmt.Errorf("write Piece: %w", err)
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
	metainfoPath := writeMetainfo(t, trackerServer.URL, rawInfo)

	previousStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() {
		os.Stdout = previousStdout
		reader.Close()
		writer.Close()
	})
	runErr := run([]string{
		"download-piece", "--piece", "0", "--output", outputPath,
		"--timeout", "5s", metainfoPath,
	})
	os.Stdout = previousStdout
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if runErr != nil {
		t.Fatalf("run(download-piece) error = %v", runErr)
	}
	select {
	case peerErr := <-peerDone:
		if peerErr != nil {
			t.Fatalf("fake peer error = %v", peerErr)
		}
	case <-time.After(time.Second):
		t.Fatal("download command returned before the fake peer finished")
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read published Piece: %v", err)
	}
	if string(got) != pieceData {
		t.Fatalf("published Piece = %q, want %q", got, pieceData)
	}
	line := string(stdout)
	if !strings.HasSuffix(line, "\n") || strings.Count(line, "\n") != 1 ||
		!strings.Contains(strings.Replace(line, outputPath, "", 1), "0") ||
		!strings.Contains(line, outputPath) {
		t.Fatalf("stdout = %q, want one success line with Piece index and output path", line)
	}
}

func appendCompactAddr(response []byte, addr *net.TCPAddr) []byte {
	ip := addr.IP.To4()
	response = append(response, ip...)
	response = append(response, 0, 0)
	binary.BigEndian.PutUint16(response[len(response)-2:], uint16(addr.Port))
	return response
}

func writeMetainfo(t *testing.T, trackerURL, rawInfo string) string {
	t.Helper()
	torrent := fmt.Sprintf("d8:announce%d:%s4:info%se", len(trackerURL), trackerURL, rawInfo)
	path := filepath.Join(t.TempDir(), "local.torrent")
	if err := os.WriteFile(path, []byte(torrent), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
