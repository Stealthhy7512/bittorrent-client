package peerconn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/Stealthhy7512/bittorrent-client/internal/peerwire"
)

func TestNewPoolRejectsInvalidArguments(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:6881")
	validOptions := DialOptions{MaxConcurrent: 1, AttemptTimeout: time.Second}
	dialPeer := func(
		context.Context,
		netip.AddrPort,
		[20]byte,
		[20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		return nil, peerwire.Handshake{}, errors.New("not called")
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name     string
		ctx      context.Context
		addrs    []netip.AddrPort
		options  DialOptions
		dialPeer dialFunc
	}{
		{
			name:     "nil context",
			addrs:    []netip.AddrPort{addr},
			options:  validOptions,
			dialPeer: dialPeer,
		},
		{
			name:     "no endpoints",
			ctx:      context.Background(),
			options:  validOptions,
			dialPeer: dialPeer,
		},
		{
			name:     "zero concurrency",
			ctx:      context.Background(),
			addrs:    []netip.AddrPort{addr},
			options:  DialOptions{AttemptTimeout: time.Second},
			dialPeer: dialPeer,
		},
		{
			name:     "concurrency above limit",
			ctx:      context.Background(),
			addrs:    []netip.AddrPort{addr},
			options:  DialOptions{MaxConcurrent: maxConcurrentDials + 1, AttemptTimeout: time.Second},
			dialPeer: dialPeer,
		},
		{
			name:     "zero attempt timeout",
			ctx:      context.Background(),
			addrs:    []netip.AddrPort{addr},
			options:  DialOptions{MaxConcurrent: 1},
			dialPeer: dialPeer,
		},
		{
			name:    "nil dial function",
			ctx:     context.Background(),
			addrs:   []netip.AddrPort{addr},
			options: validOptions,
		},
		{
			name:     "canceled context",
			ctx:      canceledCtx,
			addrs:    []netip.AddrPort{addr},
			options:  validOptions,
			dialPeer: dialPeer,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool, err := newPoolWithDial(
				test.ctx,
				test.addrs,
				[20]byte{},
				[20]byte{},
				test.options,
				test.dialPeer,
			)
			if err == nil {
				pool.Close()
				t.Fatal("newPool() error = nil, want invalid-argument error")
			}
		})
	}
}

func TestPoolReturnsCandidatesInCompletionOrderAndDeduplicatesEndpoints(t *testing.T) {
	slowAddr := netip.MustParseAddrPort("127.0.0.1:6881")
	fastAddr := netip.MustParseAddrPort("127.0.0.1:6882")
	started := make(chan netip.AddrPort, 3)
	releases := map[netip.AddrPort]chan struct{}{
		slowAddr: make(chan struct{}),
		fastAddr: make(chan struct{}),
	}
	dialPeer := func(
		ctx context.Context,
		addr netip.AddrPort,
		_ [20]byte,
		_ [20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		started <- addr
		select {
		case <-releases[addr]:
			return newPoolTestConn(), peerwire.Handshake{PeerID: [20]byte{byte(addr.Port())}}, nil
		case <-ctx.Done():
			return nil, peerwire.Handshake{}, ctx.Err()
		}
	}

	pool, err := newPoolWithDial(
		context.Background(),
		[]netip.AddrPort{slowAddr, fastAddr, slowAddr},
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: 2, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	seen := map[netip.AddrPort]bool{}
	for range 2 {
		select {
		case addr := <-started:
			seen[addr] = true
		case <-time.After(time.Second):
			t.Fatal("workers did not start both unique endpoints")
		}
	}
	if !seen[slowAddr] || !seen[fastAddr] {
		t.Fatalf("started endpoints = %v, want both unique endpoints", seen)
	}
	select {
	case addr := <-started:
		t.Fatalf("duplicate endpoint was dialed: %s", addr)
	default:
	}

	close(releases[fastAddr])
	fast, err := pool.Next()
	if err != nil {
		t.Fatalf("first Next() error = %v", err)
	}
	if fast.Addr != fastAddr {
		t.Fatalf("first candidate = %s, want %s", fast.Addr, fastAddr)
	}
	t.Cleanup(func() { fast.Conn.Close() })

	close(releases[slowAddr])
	slow, err := pool.Next()
	if err != nil {
		t.Fatalf("second Next() error = %v", err)
	}
	if slow.Addr != slowAddr {
		t.Fatalf("second candidate = %s, want %s", slow.Addr, slowAddr)
	}
	t.Cleanup(func() { slow.Conn.Close() })

	if _, err := pool.Next(); !errors.Is(err, ErrNoMoreCandidates) {
		t.Fatalf("exhausted Next() error = %v, want ErrNoMoreCandidates", err)
	}
}

func TestPoolContinuesAfterFailedEndpoint(t *testing.T) {
	failedAddr := netip.MustParseAddrPort("127.0.0.1:6881")
	successAddr := netip.MustParseAddrPort("127.0.0.1:6882")
	dialFailure := errors.New("connection refused")
	dialPeer := func(
		_ context.Context,
		addr netip.AddrPort,
		_ [20]byte,
		_ [20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		if addr == failedAddr {
			return nil, peerwire.Handshake{}, dialFailure
		}
		return newPoolTestConn(), peerwire.Handshake{}, nil
	}

	pool, err := newPoolWithDial(
		context.Background(),
		[]netip.AddrPort{failedAddr, successAddr},
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: 1, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	candidate, err := pool.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if candidate.Addr != successAddr {
		t.Fatalf("candidate address = %s, want %s", candidate.Addr, successAddr)
	}
	candidate.Conn.Close()

	if _, err := pool.Next(); !errors.Is(err, dialFailure) || !errors.Is(err, ErrNoMoreCandidates) {
		t.Fatalf("exhausted Next() error = %v, want joined exhaustion and dial errors", err)
	}
}

func TestPoolLimitsConcurrentDials(t *testing.T) {
	addrs := make([]netip.AddrPort, 6)
	for i := range addrs {
		addrs[i] = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), uint16(6881+i))
	}
	started := make(chan netip.AddrPort, len(addrs))
	release := make(chan struct{})
	dialPeer := func(
		ctx context.Context,
		addr netip.AddrPort,
		_ [20]byte,
		_ [20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		started <- addr
		select {
		case <-release:
			return nil, peerwire.Handshake{}, errors.New("dial failed")
		case <-ctx.Done():
			return nil, peerwire.Handshake{}, ctx.Err()
		}
	}

	pool, err := newPoolWithDial(
		context.Background(),
		addrs,
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: maxConcurrentDials, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	for range maxConcurrentDials {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("expected all dial workers to start")
		}
	}
	select {
	case addr := <-started:
		t.Fatalf("started fifth concurrent dial for %s", addr)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
}

func TestPoolCloseClosesUnclaimedCandidate(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:6881")
	created := make(chan *poolTestConn, 1)
	dialPeer := func(
		context.Context,
		netip.AddrPort,
		[20]byte,
		[20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		conn := newPoolTestConn()
		created <- conn
		return conn, peerwire.Handshake{}, nil
	}

	pool, err := newPoolWithDial(
		context.Background(),
		[]netip.AddrPort{addr},
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: 1, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}
	conn := <-created
	pool.Close()
	if !conn.isClosed() {
		t.Fatal("Close() left an unclaimed candidate connection open")
	}
}

func TestPoolTransfersCandidateConnectionOwnership(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:6881")
	conn := newPoolTestConn()
	dialPeer := func(
		context.Context,
		netip.AddrPort,
		[20]byte,
		[20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		return conn, peerwire.Handshake{}, nil
	}

	pool, err := newPoolWithDial(
		context.Background(),
		[]netip.AddrPort{addr},
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: 1, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}

	candidate, err := pool.Next()
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if conn.isClosed() {
		t.Fatal("Close() closed a connection returned by Next()")
	}
	if err := candidate.Conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPoolCancellationStopsDialAndUnblocksNext(t *testing.T) {
	addr := netip.MustParseAddrPort("127.0.0.1:6881")
	dialStarted := make(chan struct{})
	dialCanceled := make(chan struct{})
	dialPeer := func(
		ctx context.Context,
		_ netip.AddrPort,
		_ [20]byte,
		_ [20]byte,
	) (net.Conn, peerwire.Handshake, error) {
		close(dialStarted)
		<-ctx.Done()
		close(dialCanceled)
		return nil, peerwire.Handshake{}, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	pool, err := newPoolWithDial(
		ctx,
		[]netip.AddrPort{addr},
		[20]byte{},
		[20]byte{},
		DialOptions{MaxConcurrent: 1, AttemptTimeout: time.Second},
		dialPeer,
	)
	if err != nil {
		t.Fatal(err)
	}

	nextDone := make(chan error, 1)
	go func() {
		_, err := pool.Next()
		nextDone <- err
	}()

	select {
	case <-dialStarted:
	case <-time.After(time.Second):
		pool.Close()
		t.Fatal("dial did not start")
	}
	cancel()

	select {
	case err := <-nextDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Next() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		pool.Close()
		t.Fatal("Next() did not unblock after cancellation")
	}

	select {
	case <-dialCanceled:
	case <-time.After(time.Second):
		pool.Close()
		t.Fatal("active dial did not observe cancellation")
	}

	closeDone := make(chan struct{})
	go func() {
		pool.Close()
		pool.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close() did not finish after cancellation")
	}
}

type poolTestConn struct {
	closed chan struct{}
	once   sync.Once
}

func newPoolTestConn() *poolTestConn {
	return &poolTestConn{closed: make(chan struct{})}
}

func (c *poolTestConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *poolTestConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *poolTestConn) LocalAddr() net.Addr              { return nil }
func (c *poolTestConn) RemoteAddr() net.Addr             { return nil }
func (c *poolTestConn) SetDeadline(time.Time) error      { return nil }
func (c *poolTestConn) SetReadDeadline(time.Time) error  { return nil }
func (c *poolTestConn) SetWriteDeadline(time.Time) error { return nil }

func (c *poolTestConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *poolTestConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}
