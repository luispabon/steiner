package lsp

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
)

// helperEnv selects the behaviour of the child process spawned by
// TestNewTransportRejectsProcessesThatDoNotSpeakLSP.
const helperEnv = "STEINER_LSP_TEST_HELPER"

// TestLSPHelperProcess is not a test: it is the entry point of the child
// process the transport test spawns, and does nothing in a normal test run.
func TestLSPHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	switch mode {
	case "":
	case "exit":
		os.Exit(0)
	case "silent":
		// Never speak LSP, and outlive the test so the transport has to give up
		// on its own and terminate the process.
		time.Sleep(time.Minute)
		os.Exit(1)
	case "lsp":
		// Speak LSP over stdio: accept initialize and exit cleanly.
		runLSPServerHelper()
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

// runLSPServerHelper implements an in-process LSP server for testing.
// It speaks the LSP protocol over stdin/stdout.
func runLSPServerHelper() {
	ctx := context.Background()
	stream := jsonrpc2.NewStream(&readWriteCloser{r: os.Stdin, w: os.Stdout})
	fs := fakeServerForHelper()
	_, serverConn, _ := protocol.NewServer(ctx, fs, stream)
	defer func() { _ = serverConn.Close() }()

	<-fs.exited
}

// fakeServerForHelper creates a minimal LSP server for helper process testing.
func fakeServerForHelper() *fakeServer {
	fs := &fakeServer{exited: make(chan struct{})}
	fs.onExit = func() {
		close(fs.exited)
	}
	return fs
}

// TestNewTransportAbortsInitializeBlockedOnPipe pins that the handshake deadline
// ends a spawn whose initialize request cannot be written: the silent helper never
// reads its stdin, so a payload larger than the pipe buffer blocks the write and
// only terminating the process releases it.
func TestNewTransportAbortsInitializeBlockedOnPipe(t *testing.T) {
	t.Parallel()

	spec := TransportSpec{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestLSPHelperProcess"},
		Env:     append(os.Environ(), helperEnv+"=silent"),
		// Far larger than any pipe buffer, so the write cannot complete while the
		// helper leaves stdin unread.
		InitializationOptions: map[string]any{"padding": strings.Repeat("x", 256*1024)},
	}

	handshakeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := newTransport(handshakeCtx, context.Background(), spec)
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("newTransport error = %v, want the handshake deadline", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("newTransport did not return after the initialize write blocked")
	}
}

func TestNewTransportRejectsProcessesThatDoNotSpeakLSP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mode string
		// timeout bounds the handshake; a silent server can only be given up on
		// once the deadline passes.
		timeout time.Duration
	}{
		{name: "process exits immediately", mode: "exit", timeout: testTimeout},
		{name: "process never answers initialize", mode: "silent", timeout: 300 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			start := time.Now()
			s, err := newTransport(ctx, ctx, TransportSpec{
				Command:  os.Args[0],
				Args:     []string{"-test.run=TestLSPHelperProcess"},
				Env:      append(os.Environ(), helperEnv+"="+tt.mode),
				RootPath: t.TempDir(),
			})
			if err == nil {
				_ = s.Close(ctx)
				t.Fatal("expected the handshake to fail against a process that does not speak LSP")
			}
			// Allow reasonable scheduling margin above the case's own context
			// timeout, rather than the unrelated (and much larger) testTimeout.
			bound := tt.timeout + 500*time.Millisecond
			if elapsed := time.Since(start); elapsed >= bound {
				t.Errorf("newTransport took %v, want it bounded by the context deadline (%v)", elapsed, bound)
			}
		})
	}
}
