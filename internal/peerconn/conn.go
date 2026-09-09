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

func Dial(
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
