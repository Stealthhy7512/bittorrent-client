// Command bt inspects single-file BitTorrent metainfo, announces to an HTTP
// tracker, handshakes with a discovered peer, or downloads one verified piece.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/metainfo"
	"github.com/Stealthhy7512/bittorrent-client/internal/peerconn"
	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
	"github.com/Stealthhy7512/bittorrent-client/internal/tracker"
)

var (
	ErrHashMismatch = errors.New("downloaded piece corrupted")
	ErrWriteBlock   = errors.New("failed to write block")
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bt <inspect|announce|handshake|download-piece> <.torrent>")
	}

	switch args[0] {
	case "inspect":
		return inspect(args[1:])
	case "announce":
		return announce(args[1:])
	case "handshake":
		return handshake(args[1:])
	case "download-piece":
		return downloadPiece(args[1:])
	default:
		return fmt.Errorf("unknown command %v", args[0])
	}
}

// inspect retrieves a torrent file's metadata info.
func inspect(args []string) error {
	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: bt inspect <.torrent>")
	}

	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("inspect requires exactly one .torrent file")
	}

	torrent, err := metainfo.Open(flags.Arg(0))
	if err != nil {
		return err
	}

	fmt.Printf("Name:         %s\n", torrent.Name)
	fmt.Printf("Info hash:    %x\n", torrent.InfoHash)
	fmt.Printf("Tracker:      %s\n", torrent.Announce)
	fmt.Printf("Length:       %d bytes\n", torrent.Length)
	fmt.Printf("Piece length: %d bytes\n", torrent.PieceLength)
	fmt.Printf("Pieces:       %d\n", len(torrent.PieceHashes))
	return nil
}

// announce validates command arguments, sends a started announce with a fresh
// peer ID, and prints the tracker interval and returned peer endpoints.
func announce(args []string) error {
	flags := flag.NewFlagSet("announce", flag.ContinueOnError)
	port := flags.Uint("port", 6881, "listening port advertised to the tracker")
	timeout := flags.Duration("timeout", 10*time.Second, "announce request timeout")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: bt announce [--port PORT] [--timeout DURATION] <.torrent>")
	}

	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("announce requires exactly one .torrent file")
	}
	if *port == 0 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}

	torrent, err := metainfo.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	if torrent.Length < 0 {
		return errors.New("torrent has a negative length")
	}

	peerID, err := newPeerID()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := tracker.Client{HTTPClient: &http.Client{Timeout: *timeout}}
	response, err := client.Announce(ctx, torrent.Announce, tracker.AnnounceRequest{
		InfoHash: torrent.InfoHash,
		PeerID:   peerID,
		Port:     uint16(*port),
		Left:     uint64(torrent.Length),
		Compact:  true,
		Event:    tracker.EventStarted,
	})

	if err != nil {
		return err
	}

	fmt.Printf("Tracker interval: %s\n", response.Interval)
	fmt.Printf("Peers: %d\n", len(response.Peers))
	for _, peer := range response.Peers {
		fmt.Println(peer.Addr)
	}
	return nil
}

// handshake announces to discover endpoints, then prints the first peer to
// complete a valid handshake. One overall timeout covers both the tracker
// request and peer discovery; the selected connection is closed before return.
func handshake(args []string) error {
	flags := flag.NewFlagSet("handshake", flag.ContinueOnError)
	port := flags.Uint("port", 6881, "listening port advertised to the tracker")
	timeout := flags.Duration("timeout", 10*time.Second, "handshake operation timeout")
	flags.Usage = func() {
		fmt.Fprintln(flags.Output(), "usage: bt handshake [--port PORT] [--timeout DURATION] <.torrent>")
	}

	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("handshake requires exactly one .torrent file")
	}
	if *port == 0 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	torrent, err := metainfo.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	if torrent.Length < 0 {
		return errors.New("torrent has a negative length")
	}

	peerID, err := newPeerID()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := tracker.Client{HTTPClient: &http.Client{Timeout: *timeout}}
	res, err := client.Announce(ctx, torrent.Announce, tracker.AnnounceRequest{
		InfoHash: torrent.InfoHash,
		PeerID:   peerID,
		Port:     uint16(*port),
		Left:     uint64(torrent.Length),
		Compact:  true,
		Event:    tracker.EventStarted,
	})
	if err != nil {
		return err
	}

	addrs := make([]netip.AddrPort, len(res.Peers))
	for i, peer := range res.Peers {
		addrs[i] = peer.Addr
	}

	result, err := peerconn.DialFirst(ctx, addrs, torrent.InfoHash, peerID, peerconn.DialOptions{
		MaxConcurrent:  4,
		AttemptTimeout: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("no tracker peer completed handshake: %w", err)
	}
	defer result.Conn.Close()

	fmt.Printf("Peer address: %s\nRemote peer ID: %x\n", result.Addr, result.Handshake.PeerID)
	return nil
}

// downloadPiece retrieves a single-block piece from one peer and publishes it
// only after its SHA-1 digest matches metainfo. It does not retry another peer.
func downloadPiece(args []string) error {
	flags := flag.NewFlagSet("download-piece", flag.ContinueOnError)
	port := flags.Uint("port", 6881, "listening port advertised to the tracker")
	piece := flags.Int("piece", -1, "piece index to download")
	output := flags.String("output", "", "download output path")
	timeout := flags.Duration("timeout", 2*time.Minute, "download timeout")
	force := flags.Bool("force", false, "force output name if a file with same name is present")
	flags.Usage = func() {
		fmt.Fprintln(
			flags.Output(),
			"usage: bt download-piece [--port PORT] [--piece PIECE] [--output PATH] [--timeout DURATION] [--force] <.torrent>",
		)
	}

	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return errors.New("download-piece requires exactly one .torrent file")
	}
	if *port == 0 || *port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if *piece < 0 {
		return errors.New("piece index must be non-negative")
	}
	if *timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	if *output == "" {
		return errors.New("output path must be given")
	}

	torrent, err := metainfo.Open(flags.Arg(0))
	if err != nil {
		return err
	}
	if torrent.Length < 0 {
		return errors.New("torrent has a negative length")
	}

	if *piece >= len(torrent.PieceHashes) {
		return fmt.Errorf("piece index %d out of range for %d pieces", *piece, len(torrent.PieceHashes))
	}
	if uint64(*piece) >= 1<<32 {
		return fmt.Errorf("piece index %d exceeds peer-wire index range", *piece)
	}

	peerID, err := newPeerID()
	if err != nil {
		return err
	}

	// the final piece may be shorter than the declared piece length
	start := int64(*piece) * torrent.PieceLength
	length := min(torrent.PieceLength, torrent.Length-start)
	if length <= 0 || uint64(length) > 1<<32 {
		return fmt.Errorf("piece length %d is outside peer-wire offset range", length)
	}

	if length > peerwire.MaxBlockSize {
		return fmt.Errorf("piece size %v exceeds allowed maximum: %v", length, peerwire.MaxBlockSize)
	}

	// reject an existing destination before announcing to the tracker
	destination, err := os.Lstat(*output)
	switch {
	case err == nil:
		if destination.IsDir() {
			return fmt.Errorf("output path %q is a directory", *output)
		}
		if !*force {
			return fs.ErrExist
		}
		inputInfo, err := os.Stat(flags.Arg(0))
		if err != nil {
			return fmt.Errorf("stat Metainfo input: %w", err)
		}
		outputInfo, err := os.Stat(*output)
		if err == nil && os.SameFile(inputInfo, outputInfo) {
			return errors.New("output path resolves to the Metainfo input")
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("stat output path: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		// temporary file creation below checks that the parent is writable
	default:
		return fmt.Errorf("stat output path: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(*output), ".bt-piece-*")
	if err != nil {
		return fmt.Errorf("error when writing file: %w", err)
	}
	defer os.Remove(temp.Name())
	defer temp.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := tracker.Client{HTTPClient: &http.Client{Timeout: *timeout}}
	response, err := client.Announce(ctx, torrent.Announce, tracker.AnnounceRequest{
		InfoHash: torrent.InfoHash,
		PeerID:   peerID,
		Port:     uint16(*port),
		Left:     uint64(torrent.Length),
		Compact:  true,
		Event:    tracker.EventStarted,
	})

	if err != nil {
		return err
	}

	addrs := make([]netip.AddrPort, len(response.Peers))
	for i, peer := range response.Peers {
		addrs[i] = peer.Addr
	}

	result, err := peerconn.DialFirst(ctx, addrs, torrent.InfoHash, peerID, peerconn.DialOptions{
		MaxConcurrent:  4,
		AttemptTimeout: 5 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("no tracker peer completed handshake: %w", err)
	}
	defer result.Conn.Close()

	session, err := peerconn.NewSession(result.Conn, len(torrent.PieceHashes))
	if err != nil {
		return fmt.Errorf("error when creating session: %w", err)
	}

	err = session.FetchPiece(
		ctx,
		peerconn.PieceSpec{Index: uint32(*piece), Length: uint64(length)},
		func(offset uint32, block []byte) error {
			n, err := temp.WriteAt(block, int64(offset))
			if err != nil {
				return fmt.Errorf("%w at offset %v: %w", ErrWriteBlock, offset, err)
			}
			if n != len(block) {
				return fmt.Errorf("%w at offset %v: %w", ErrWriteBlock, offset, io.ErrShortWrite)
			}

			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("error when fetching piece: %w", err)
	}

	// hash the exact piece bytes written to the temporary file
	if _, err := temp.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek temporary file: %w", err)
	}

	hasher := sha1.New()
	if _, err := io.CopyN(hasher, temp, length); err != nil {
		return fmt.Errorf("hash temporary file: %w", err)
	}

	if !bytes.Equal(hasher.Sum(nil), torrent.PieceHashes[*piece][:]) {
		return ErrHashMismatch
	}

	// close the verified temporary file before publishing it
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	switch *force {
	case true:
		if err := os.Rename(temp.Name(), *output); err != nil {
			return fmt.Errorf("force publish Verified Piece: %w", err)
		}
	case false:
		if err := os.Link(temp.Name(), *output); err != nil {
			return fmt.Errorf("publish Verified Piece: %w", err)
		}
		if err := os.Remove(temp.Name()); err != nil {
			return fmt.Errorf("remove temporary file: %w", err)
		}
	}

	fmt.Printf("downloaded piece %v to %v\n", *piece, *output)
	return nil
}

// newPeerID combines the client/version prefix with cryptographically random
// bytes. A caller should reuse this ID throughout one torrent participation.
func newPeerID() ([20]byte, error) {
	var peerID [20]byte
	copy(peerID[:], "-BT0001-")
	if _, err := rand.Read(peerID[8:]); err != nil {
		return [20]byte{}, fmt.Errorf("generate peer ID: %w", err)
	}
	return peerID, nil
}
