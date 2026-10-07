package peerconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

// DialOptions controls how peers are attempted.
type DialOptions struct {
	// MaxConcurrent must be between one and four.
	MaxConcurrent int
	// AttemptTimeout limits one endpoint's dial and handshake attempt.
	AttemptTimeout time.Duration
}

// DialResult is a peer connection that completed a valid handshake.
type DialResult struct {
	Conn      net.Conn
	Addr      netip.AddrPort
	Handshake peerwire.Handshake
}

// DialFirst returns the first successfully handshaked candidate from a Pool.
// Before returning it stops discovery and closes unclaimed connections. The
// caller owns the returned connection and must close it.
func DialFirst(
	ctx context.Context,
	addrs []netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
	options DialOptions,
) (DialResult, error) {
	pool, err := NewPool(ctx, addrs, infoHash, peerID, options)
	if err != nil {
		return DialResult{}, err
	}
	defer pool.Close()
	return pool.Next()
}

// dial opens TCP and exchanges a handshake under ctx, closing the connection on
// failure. On success it stops watching ctx and clears the attempt deadline so
// the next owner can establish its own I/O lifetime.
func dial(
	ctx context.Context,
	addr netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
) (net.Conn, peerwire.Handshake, error) {
	var d net.Dialer

	conn, err := d.DialContext(ctx, "tcp", addr.String())
	if err != nil {
		return nil, peerwire.Handshake{}, err
	}

	stopWatching, err := watchContext(ctx, conn)
	if err != nil {
		conn.Close()
		return nil, peerwire.Handshake{}, err
	}
	defer stopWatching()

	remote, err := exchangeHandshake(conn, infoHash, peerID)
	if err != nil {
		conn.Close()
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, peerwire.Handshake{}, ctxErr
		}
		return nil, peerwire.Handshake{}, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		conn.Close()
		return nil, peerwire.Handshake{}, ctxErr
	}

	return conn, remote, nil
}

// watchContext applies ctx's deadline and interrupts pending I/O on cancellation,
// including when ctx has no deadline, by setting an immediate connection deadline.
// The returned function must be called exactly once: it waits for the watcher
// to exit before clearing deadlines, preventing a late cancellation from
// affecting the next owner. It does not restore any previous deadline.
func watchContext(ctx context.Context, conn net.Conn) (func(), error) {
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			_ = conn.SetDeadline(time.Now())
		case <-stop:
		}
	}()

	return func() {
		close(stop)
		<-stopped
		_ = conn.SetDeadline(time.Time{})
	}, nil
}

// exchangeHandshake sends the local handshake and requires the response to name
// the same info hash. It preserves the remote peer ID and reserved bits without
// interpreting them. The caller controls cancellation and connection deadlines.
func exchangeHandshake(
	conn io.ReadWriter,
	infoHash [20]byte,
	peerID [20]byte,
) (peerwire.Handshake, error) {
	local := peerwire.Handshake{
		InfoHash: infoHash,
		PeerID:   peerID,
	}

	if _, err := local.WriteTo(conn); err != nil {
		return peerwire.Handshake{}, fmt.Errorf("write handshake: %w", err)
	}

	remote, err := peerwire.ReadHandshake(conn)
	if err != nil {
		return peerwire.Handshake{}, fmt.Errorf("read handshake: %w", err)
	}

	if remote.InfoHash != infoHash {
		return peerwire.Handshake{}, errors.New("peer returned a different hash")
	}

	return remote, nil
}
