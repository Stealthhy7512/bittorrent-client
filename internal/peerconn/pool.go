package peerconn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

const (
	maxConcurrentDials   = 4
	maxWaitingCandidates = 4
)

// ErrNoMoreCandidates reports that every unique endpoint has been attempted.
var ErrNoMoreCandidates = errors.New("no more peer candidates")

// Pool discovers handshaked peer candidates while bounding open connections.
type Pool struct {
	ctx        context.Context
	cancel     context.CancelFunc
	candidates chan DialResult
	done       chan struct{}
	slots      chan struct{}
	closeOnce  sync.Once
	exhaustErr error
}

type dialFunc func(
	context.Context,
	netip.AddrPort,
	[20]byte,
	[20]byte,
) (net.Conn, peerwire.Handshake, error)

// dialResult wraps DialResult with an attempt error for pool processing.
type dialResult struct {
	DialResult
	err error
}

// NewPool starts candidate discovery. The caller must close the returned pool.
func NewPool(
	ctx context.Context,
	addrs []netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
	options DialOptions,
) (*Pool, error) {
	return newPoolWithDial(ctx, addrs, infoHash, peerID, options, dial)
}

func newPoolWithDial(
	ctx context.Context,
	addrs []netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
	options DialOptions,
	dialPeer dialFunc,
) (*Pool, error) {
	if err := validatePoolArguments(ctx, addrs, options, dialPeer); err != nil {
		return nil, err
	}

	uniqueAddrs := uniqueEndpoints(addrs)
	poolCtx, cancel := context.WithCancel(ctx)
	p := &Pool{
		ctx:        poolCtx,
		cancel:     cancel,
		candidates: make(chan DialResult, min(maxWaitingCandidates, len(uniqueAddrs))),
		done:       make(chan struct{}),
		slots:      make(chan struct{}, maxWaitingCandidates),
	}

	jobs := make(chan netip.AddrPort)
	results := make(chan dialResult)
	workerCount := min(options.MaxConcurrent, len(uniqueAddrs))
	var workers sync.WaitGroup

	for range workerCount {
		workers.Go(func() {
			p.runWorker(jobs, results, infoHash, peerID, options.AttemptTimeout, dialPeer)
		})
	}

	go p.feedJobs(jobs, uniqueAddrs)
	go func() {
		workers.Wait()
		close(results)
	}()
	go p.collect(results)

	return p, nil
}

// Next returns the next successfully handshaked candidate in completion order.
// Ownership of the candidate connection transfers to the caller.
func (p *Pool) Next() (DialResult, error) {
	if err := p.ctx.Err(); err != nil {
		return DialResult{}, err
	}

	candidate, ok := <-p.candidates
	if !ok {
		if err := p.ctx.Err(); err != nil {
			return DialResult{}, err
		}
		return DialResult{}, errors.Join(ErrNoMoreCandidates, p.exhaustErr)
	}

	p.releaseSlot()
	if err := p.ctx.Err(); err != nil {
		_ = candidate.Conn.Close()
		return DialResult{}, err
	}
	return candidate, nil
}

// Close stops discovery, closes unclaimed connections, and waits for workers.
// It is safe to call Close more than once.
func (p *Pool) Close() {
	p.closeOnce.Do(func() {
		p.cancel()
		<-p.done
		for candidate := range p.candidates {
			_ = candidate.Conn.Close()
			p.releaseSlot()
		}
	})
}

func validatePoolArguments(
	ctx context.Context,
	addrs []netip.AddrPort,
	options DialOptions,
	dialPeer dialFunc,
) error {
	switch {
	case ctx == nil:
		return errors.New("peer pool context is nil")

	case len(addrs) == 0:
		return errors.New("no peer endpoints")

	case options.MaxConcurrent <= 0:
		return errors.New("maximum concurrent dials must be positive")

	case options.MaxConcurrent > maxConcurrentDials:
		return fmt.Errorf(
			"maximum concurrent dials is %d, limit is %d",
			options.MaxConcurrent,
			maxConcurrentDials,
		)

	case options.AttemptTimeout <= 0:
		return errors.New("peer attempt timeout must be positive")

	case dialPeer == nil:
		return errors.New("peer dial function is nil")
	}

	return ctx.Err()
}

// uniqueEndpoints builds a set of endpoints and returns a slice of its elements.
func uniqueEndpoints(addrs []netip.AddrPort) []netip.AddrPort {
	// `map[T]struct{}` instead of `map[T]bool` because empty struct consumes no memory
	seen := make(map[netip.AddrPort]struct{}, len(addrs))
	unique := make([]netip.AddrPort, 0, len(addrs))

	for _, addr := range addrs {
		addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
		if _, exists := seen[addr]; exists {
			continue
		}
		seen[addr] = struct{}{}
		unique = append(unique, addr)
	}
	return unique
}

func (p *Pool) feedJobs(jobs chan<- netip.AddrPort, addrs []netip.AddrPort) {
	defer close(jobs)
	for _, addr := range addrs {
		select {
		case jobs <- addr:
		case <-p.ctx.Done():
			return
		}
	}
}

func (p *Pool) runWorker(
	jobs <-chan netip.AddrPort,
	results chan<- dialResult,
	infoHash [20]byte,
	peerID [20]byte,
	attemptTimeout time.Duration,
	dialPeer dialFunc,
) {
	for {
		select {
		case <-p.ctx.Done():
			return
		case addr, ok := <-jobs:
			if !ok {
				return
			}
			if !p.acquireSlot() {
				return
			}

			attemptCtx, cancelAttempt := context.WithTimeout(p.ctx, attemptTimeout)
			conn, handshake, err := dialPeer(attemptCtx, addr, infoHash, peerID)
			cancelAttempt()

			if err != nil {
				if conn != nil {
					_ = conn.Close()
				}
				p.releaseSlot()
			}

			result := dialResult{
				DialResult: DialResult{Conn: conn, Addr: addr, Handshake: handshake},
				err:        err,
			}
			select {
			case results <- result:
			case <-p.ctx.Done():
				if err == nil {
					_ = conn.Close()
					p.releaseSlot()
				}
				return
			}
		}
	}
}

func (p *Pool) collect(results <-chan dialResult) {
	defer close(p.done)
	defer close(p.candidates)

	var failures []error
	for result := range results {
		if result.err != nil {
			if !errors.Is(result.err, context.Canceled) {
				failures = append(
					failures,
					fmt.Errorf("%s: %w", result.Addr, result.err),
				)
			}
			continue
		}

		select {
		case p.candidates <- result.DialResult:
		case <-p.ctx.Done():
			_ = result.Conn.Close()
			p.releaseSlot()
		}
	}
	p.exhaustErr = errors.Join(failures...)
}

// acquireSlot sends an empty struct into slots chan to occupy.
func (p *Pool) acquireSlot() bool {
	select {
	case p.slots <- struct{}{}:
		return true
	case <-p.ctx.Done():
		return false
	}
}

func (p *Pool) releaseSlot() {
	<-p.slots
}
