package peerconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

// DialOptions controls how peers are attempted.
type DialOptions struct {
	MaxConcurrent  int
	AttemptTimeout time.Duration
}

// DialResult is the first peer connection that completes a valid handshake.
type DialResult struct {
	Conn      net.Conn
	Addr      netip.AddrPort
	Handshake peerwire.Handshake
}

// dialResult wraps `DialResult` with local errors for worker processing.
type dialResult struct {
	DialResult
	err error
}

func DialFirst(
	ctx context.Context,
	addrs []netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
	options DialOptions,
) (DialResult, error) {
	if len(addrs) == 0 {
		return DialResult{}, errors.New("no peers returned")
	}
	if options.MaxConcurrent <= 0 {
		return DialResult{}, errors.New("maximum concurrent dials must be positive")
	}
	if options.AttemptTimeout <= 0 {
		return DialResult{}, errors.New("peer attempt timeout must be positive")
	}

	workerCount := min(options.MaxConcurrent, len(addrs))
	dialCtx, cancelDials := context.WithCancel(ctx)
	defer cancelDials()

	jobs := make(chan netip.AddrPort)
	results := make(chan dialResult)
	var workers sync.WaitGroup

	for range workerCount {
		workers.Go(func() {
			for addr := range jobs {
				attemptCtx, cancelAttempt := context.WithTimeout(dialCtx, options.AttemptTimeout)
				conn, handshake, err := dial(attemptCtx, addr, infoHash, peerID)
				cancelAttempt()
				results <- dialResult{
					DialResult: DialResult{Conn: conn, Addr: addr, Handshake: handshake},
					err:        err,
				}
			}
		})
	}

	go func() {
		defer close(jobs)
		for _, addr := range addrs {
			select {
			case jobs <- addr:
			case <-dialCtx.Done():
				return
			}
		}
	}()

	go func() {
		workers.Wait()
		close(results)
	}()

	var winner *DialResult
	var dialErrors []error
	for result := range results {
		if result.err == nil {
			if winner == nil {
				won := result.DialResult
				winner = &won
				cancelDials()
			} else {
				result.Conn.Close()
			}
			continue
		}
		if !errors.Is(result.err, context.Canceled) {
			dialErrors = append(dialErrors, fmt.Errorf("%s: %w", result.Addr, result.err))
		}
	}

	if winner != nil {
		return *winner, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return DialResult{}, ctxErr
	}
	return DialResult{}, errors.Join(dialErrors...)
}

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
