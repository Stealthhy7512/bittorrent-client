package tracker

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/jackpal/bencode-go"
)

func decodeAnnounceResponse(body io.Reader) (AnnounceResponse, error) {
	rawPeers, err := bencode.Decode(body)
	if err != nil {
		return AnnounceResponse{}, err
	}

	dict, ok := rawPeers.(map[string]any)
	if !ok {
		return AnnounceResponse{}, errors.New("tracker response is not a valid dictionary")
	}

	// if tracker returns `failure reason`
	if failure, ok := dict["failure reason"].(string); ok {
		return AnnounceResponse{}, fmt.Errorf("tracker failure: %s", failure)
	}

	interval, err := parseInterval(dict["interval"])
	if err != nil {
		return AnnounceResponse{}, err
	}

	res := AnnounceResponse{
		Interval: interval,
	}

	switch rawPeers := dict["peers"].(type) {
	case string:
		// Compact 6-byte peers
		peers, err := parseCompactPeers(rawPeers)
		if err != nil {
			return AnnounceResponse{}, err
		}

		res.Peers = peers

	case []any:
		// Regular peer dict form
		peers, err := parseDictPeers(rawPeers)
		if err != nil {
			return AnnounceResponse{}, err
		}

		res.Peers = peers

	default:
		return AnnounceResponse{}, errors.New("invalid peers")
	}

	return res, nil
}

func parseCompactPeers(rawPeers string) ([]Peer, error) {
	if len(rawPeers)%6 != 0 {
		return nil, errors.New("peers length not a multiple of 6-bytes")
	}

	peers := make([]Peer, 0, len(rawPeers)/6)
	for offset := 0; offset < len(rawPeers); offset += 6 {
		chunk := rawPeers[offset : offset+6]

		var ip [4]byte
		copy(ip[:], chunk[:4])

		port := binary.BigEndian.Uint16([]byte(chunk[4:6]))
		addr := netip.AddrPortFrom(netip.AddrFrom4(ip), port)

		peers = append(peers, Peer{Addr: addr})
	}

	return peers, nil
}

func parseDictPeers(rawPeers []any) ([]Peer, error) {
	peers := make([]Peer, 0, len(rawPeers))

	for i, rawPeer := range rawPeers {
		dict, ok := rawPeer.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("peer %d is not a dictionary", i)
		}

		ip, ok := dict["ip"].(string)
		if !ok {
			return nil, fmt.Errorf("peer %d has invalid ip", i)
		}
		addr, err := netip.ParseAddr(ip)
		if err != nil {
			return nil, fmt.Errorf("peer %d has non-IP address %q: %w", i, ip, err)
		}

		port, err := parsePort(dict["port"])
		if err != nil {
			return nil, fmt.Errorf("peer %d: %w", i, err)
		}

		peer := Peer{Addr: netip.AddrPortFrom(addr, port)}
		if rawID, exists := dict["peer id"]; exists {
			id, err := parsePeerID(rawID)
			if err != nil {
				return nil, fmt.Errorf("peer %d: %w", i, err)
			}
			peer.PeerID = id
		}
		peers = append(peers, peer)
	}
	return peers, nil
}

func parsePort(rawPort any) (uint16, error) {
	var port uint64

	switch value := rawPort.(type) {
	case int64:
		if value <= 0 {
			return 0, errors.New("port must be between 1 and 65535")
		}
		port = uint64(value)
	case uint64:
		port = value
	default:
		return 0, fmt.Errorf("port has invalid type %T", rawPort)
	}

	if port == 0 || port > 65535 {
		return 0, errors.New("port must be between 1 and 65535")
	}
	return uint16(port), nil
}

func parsePeerID(rawID any) (*[20]byte, error) {
	idString, ok := rawID.(string)
	if !ok {
		return nil, errors.New("peer id has invalid type")
	}
	if len(idString) != 20 {
		return nil, fmt.Errorf("peer id is %d bytes, want 20", len(idString))
	}

	var id [20]byte
	copy(id[:], idString)
	return &id, nil
}

func parseInterval(rawInterval any) (time.Duration, error) {
	var seconds uint64
	switch value := rawInterval.(type) {
	case int64:
		if value <= 0 {
			return 0, errors.New("tracker response has invalid interval")
		}
		seconds = uint64(value)
	case uint64:
		if value == 0 {
			return 0, errors.New("tracker response has invalid interval")
		}
		seconds = value
	default:
		return 0, errors.New("tracker response has invalid interval")
	}

	const maxDuration = time.Duration(1<<63 - 1)
	if seconds > uint64(maxDuration/time.Second) {
		return 0, errors.New("tracker response interval is too large")
	}
	return time.Duration(seconds) * time.Second, nil
}
