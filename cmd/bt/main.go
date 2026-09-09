package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/download"
	"github.com/Stealthhy7512/bittorrent-client/internal/metainfo"
	"github.com/Stealthhy7512/bittorrent-client/internal/tracker"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: bt <inspect|announce|handshake> <.torrent>")
	}

	switch args[0] {
	case "inspect":
		return inspect(args[1:])
	case "announce":
		return announce(args[1:])
	case "handshake":
		return handshake(args[1:])
	default:
		return fmt.Errorf("unknown command %v", args[0])
	}
}

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

func newPeerID() ([20]byte, error) {
	var peerID [20]byte
	copy(peerID[:], "-BT0001-")
	if _, err := rand.Read(peerID[8:]); err != nil {
		return [20]byte{}, fmt.Errorf("generate peer ID: %w", err)
	}
	return peerID, nil
}

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

	result, err := download.DialFirst(ctx, addrs, torrent.InfoHash, peerID, download.DialOptions{
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
