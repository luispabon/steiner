package lsp

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// stalledWriteStream wraps a client stream and can stall its writes, modelling a
// language server that stops draining its stdin pipe. A stalled write is released
// by peerGone or Close, which is what process termination does to a blocked pipe
// write in a real session.
type stalledWriteStream struct {
	inner jsonrpc2.Stream

	stalled  atomic.Bool
	entered  chan struct{}
	returned chan struct{}
	release  chan struct{}
	closed   chan struct{}

	enterOnce   sync.Once
	returnOnce  sync.Once
	releaseOnce sync.Once
	closeOnce   sync.Once
}

func newStalledWriteStream(inner jsonrpc2.Stream) *stalledWriteStream {
	return &stalledWriteStream{
		inner:    inner,
		entered:  make(chan struct{}),
		returned: make(chan struct{}),
		release:  make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

// stall makes every later write block until the peer is gone or the stream closes.
func (s *stalledWriteStream) stall() { s.stalled.Store(true) }

// peerGone releases a stalled write the way the server process dying does.
func (s *stalledWriteStream) peerGone() { s.releaseOnce.Do(func() { close(s.release) }) }

func (s *stalledWriteStream) Read(ctx context.Context) (jsonrpc2.Message, int64, error) {
	return s.inner.Read(ctx)
}

func (s *stalledWriteStream) Write(ctx context.Context, msg jsonrpc2.Message) (int64, error) {
	if !s.stalled.Load() {
		return s.inner.Write(ctx, msg)
	}

	s.enterOnce.Do(func() { close(s.entered) })
	select {
	case <-s.release:
	case <-s.closed:
	}
	s.returnOnce.Do(func() { close(s.returned) })
	return 0, io.ErrClosedPipe
}

func (s *stalledWriteStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return s.inner.Close()
}

// killableProcess is a childProcess double whose Kill tears down the peer the way
// a real process exit does, releasing a blocked pipe write with EPIPE.
type killableProcess struct {
	exited chan struct{}
	once   sync.Once
	onKill func()
}

func (p *killableProcess) Exited() <-chan struct{} { return p.exited }

func (p *killableProcess) Kill() {
	p.once.Do(func() {
		if p.onKill != nil {
			p.onKill()
		}
		close(p.exited)
	})
}

func (p *killableProcess) wasKilled() bool {
	select {
	case <-p.exited:
		return true
	default:
		return false
	}
}

// TestSessionCloseUnblocksStalledPipeWrite pins that Close returns once its
// deadline passes even when the shutdown request's stream write is blocked. The
// write is released by terminating the process and closing the connection, and
// Close joins that teardown, so no goroutine is left behind.
func TestSessionCloseUnblocksStalledPipeWrite(t *testing.T) {
	t.Parallel()

	fs := newFakeServer()
	clientPipe, serverPipe := net.Pipe()
	_, serverConn, client := protocol.NewServer(context.Background(), fs, jsonrpc2.NewStream(serverPipe))
	fs.client = client

	stalled := newStalledWriteStream(jsonrpc2.NewStream(clientPipe))
	proc := &killableProcess{
		exited: make(chan struct{}),
		onKill: func() {
			// A dead server drops its end of the pipe, so the pending write fails.
			stalled.peerGone()
			_ = clientPipe.Close()
		},
	}
	t.Cleanup(func() {
		_ = clientPipe.Close()
		_ = serverPipe.Close()
		_ = serverConn.Close()
	})

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	s, err := newSession(ctx, stalled, t.TempDir(), nil, proc)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	// Only the caller's deadline may end this shutdown: the graceful path itself
	// must not give up.
	s.shutdownTimeout = time.Hour

	stalled.stall()

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer closeCancel()

	closeErr := make(chan error, 1)
	go func() { closeErr <- s.Close(closeCtx) }()

	select {
	case <-stalled.entered:
	case <-time.After(testTimeout):
		t.Fatal("shutdown request never reached the stalled stream write")
	}

	select {
	case err := <-closeErr:
		if err == nil {
			t.Error("close returned nil, want an error for a stalled shutdown")
		}
	case <-time.After(testTimeout):
		t.Fatal("Close hung on a stalled stream write")
	}

	if !proc.wasKilled() {
		t.Error("a stalled shutdown did not terminate the process")
	}
	select {
	case <-stalled.returned:
	default:
		t.Error("the stalled write was still running when Close returned")
	}
}
