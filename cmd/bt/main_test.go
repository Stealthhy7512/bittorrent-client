package main

import (
	"crypto/sha1"
	"encoding/binary"
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
