package lsp

import (
	"context"
	"os"
	"testing"
	"time"
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
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func TestNewTransportRejectsProcessesThatDoNotSpeakLSP(t *testing.T) {
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
			s, err := newTransport(ctx, TransportSpec{
				Command:  os.Args[0],
				Args:     []string{"-test.run=TestLSPHelperProcess"},
				Env:      []string{helperEnv + "=" + tt.mode},
				RootPath: t.TempDir(),
			})
			if err == nil {
				_ = s.Close(ctx)
				t.Fatal("expected the handshake to fail against a process that does not speak LSP")
			}
			if elapsed := time.Since(start); elapsed >= testTimeout {
				t.Errorf("newTransport took %v, want it bounded by the context deadline", elapsed)
			}
		})
	}
}
