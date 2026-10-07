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

// might be changed later on
const (
	maxConcurrentDials   = 4
	maxWaitingCandidates = 4
)

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

// NewPool starts candidate discovery over deduplicated endpoints, with at most
// four concurrent attempts.
//
// A shared four-slot budget covers both attempts and unclaimed successful connections.
// Each attempt has its own timeout, capped by ctx. The caller must close the returned pool.
func NewPool(
	ctx context.Context,
	addrs []netip.AddrPort,
	infoHash [20]byte,
	peerID [20]byte,
	options DialOptions,
) (*Pool, error) {
	return newPoolWithDial(ctx, addrs, infoHash, peerID, options, dial)
}

// newPoolWithDial wires the job feeder, dial workers, and result collector.
//
// It's wrapped around exported NewPool because it is the test seam for deterministic
// connection outcomes.
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
// It blocks until a candidate is available or discovery ends. Ownership of the
// candidate connection transfers to the caller, releasing its pool slot.
// Exhaustion joins ErrNoMoreCandidates with recorded attempt errors; cancellation
// returns the pool context's error instead.
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
// Connections already returned by Next remain the caller's responsibility.
//
// It is idempotent.
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

// uniqueEndpoints preserves first-seen order while deduplicating endpoints.
func uniqueEndpoints(addrs []netip.AddrPort) []netip.AddrPort {
	// `map[T]struct{}` instead of `map[T]bool` because empty struct consumes no memory
	seen := make(map[netip.AddrPort]struct{}, len(addrs))
	unique := make([]netip.AddrPort, 0, len(addrs))

	for _, addr := range addrs {
		addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()) // normalize IPv6 addresses to IPv4
		if _, exists := seen[addr]; exists {
			continue
		}
		seen[addr] = struct{}{}
		unique = append(unique, addr)
	}
	return unique
}

// feedJobs offers each unique endpoint once and closes jobs on completion or
// cancellation. The unbuffered channel keeps dispatch tied to worker readiness.
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

// runWorker reserves a slot before each dial and handshake attempt. Failures
// release it immediately; successes retain it until claimed by Next or closed
// during cleanup. A result that cannot be delivered on cancellation is cleaned
// up by the worker itself.
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

// collect queues successful candidates and records non-cancellation failures.
// After cancellation it continues draining worker results, closing connections
// that it does not queue. exhaustErr is finalized before candidates and done
// close, so exhaustion readers and Close observe completed collection.
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

// acquireSlot waits for capacity shared by attempts and unclaimed connections.
// Cancellation lets a worker stop even while every slot is occupied.
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
