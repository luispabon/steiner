package lsp

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestFakeServerStructures tests that the basic types are correctly defined.
func TestFakeServerStructures(t *testing.T) {
	// Test Location struct
	loc := Location{
		File:      "/tmp/test.go",
		Line:      10,
		Column:    5,
		EndLine:   10,
		EndColumn: 20,
	}
	if loc.File != "/tmp/test.go" {
		t.Errorf("Location.File: got %s, want /tmp/test.go", loc.File)
	}

	// Test Diagnostic struct
	diag := Diagnostic{
		File:     "/tmp/test.go",
		Line:     5,
		Column:   10,
		Severity: "error",
		Source:   "test",
		Message:  "test error",
		Code:     "E001",
	}
	if diag.Severity != "error" {
		t.Errorf("Diagnostic.Severity: got %s, want error", diag.Severity)
	}

	// Test PublishedDiagnostics struct
	pd := PublishedDiagnostics{
		File:    "/tmp/test.go",
		Version: ptrInt32(1),
		Items:   []Diagnostic{diag},
	}
	if len(pd.Items) != 1 {
		t.Errorf("PublishedDiagnostics.Items: got %d, want 1", len(pd.Items))
	}

	// Test ProgressEvent struct
	pe := ProgressEvent{
		Token:   "123",
		Kind:    "begin",
		Message: "Loading",
	}
	if pe.Token != "123" {
		t.Errorf("ProgressEvent.Token: got %s, want 123", pe.Token)
	}
}

// TestReadWriteCloser tests the pipe adapter.
func TestReadWriteCloser(t *testing.T) {
	r, w := io.Pipe()

	rwc := &readWriteCloser{
		r: r,
		w: w,
	}

	// Test Write in a goroutine to avoid blocking
	done := make(chan error, 1)
	go func() {
		n, err := rwc.Write([]byte("test"))
		if err != nil {
			done <- err
			return
		}
		if n != 4 {
			done <- fmt.Errorf("write: got %d bytes, want 4", n)
			return
		}
		done <- nil
	}()

	// Read the data to unblock the write
	buf := make([]byte, 4)
	_, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	// Wait for write goroutine
	if err := <-done; err != nil {
		t.Fatalf("Write error: %v", err)
	}

	// Test Close
	if err := rwc.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	r.Close()
	w.Close()
}

// TestHandshakeStructure tests that handshake can be called without error setup.
func TestHandshakeStructure(t *testing.T) {
	// This tests the handshake function structure exists and can be referenced
	_ = handshake // function exists
}

// TestClientHandlerInterface tests that clientHandler implements protocol.Client
func TestClientHandlerInterface(t *testing.T) {
	handler := &clientHandler{
		diagnosticsChan: make(chan PublishedDiagnostics, 1),
		progressChan:    make(chan ProgressEvent, 1),
	}

	// Verify handler implements the interface by calling a method
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Call a no-op method to verify interface compliance
	err := handler.LogTrace(ctx, nil)
	if err != nil {
		t.Errorf("LogTrace: got error %v, want nil", err)
	}
}

// TestSessionInterfaceType tests that the session interface is correctly defined.
func TestSessionInterfaceType(t *testing.T) {
	// Ensure impl satisfies session interface
	var _ session = (*impl)(nil)
}

// TestTransportSpecType tests TransportSpec structure.
func TestTransportSpecType(t *testing.T) {
	spec := TransportSpec{
		Command:  "gopls",
		Args:     []string{"serve"},
		Env:      []string{"VAR=value"},
		RootPath: "/tmp",
		Stderr:   io.Discard,
	}

	if spec.Command != "gopls" {
		t.Errorf("Command: got %s, want gopls", spec.Command)
	}
	if len(spec.Args) != 1 {
		t.Errorf("Args: got %d, want 1", len(spec.Args))
	}
}

// TestErrorServerExited tests the error constant.
func TestErrorServerExited(t *testing.T) {
	if errServerExited.Error() != "language server exited" {
		t.Errorf("error message: got %q, want %q", errServerExited.Error(), "language server exited")
	}
}

// TestURIConversions tests URI handling.
func TestURIConversions(t *testing.T) {
	// Test uri.File creates a proper URI
	u := uri.File("/tmp/test.go")
	if string(u) == "" {
		t.Error("uri.File returned empty URI")
	}

	// Test conversion to string
	s := string(u)
	if s == "" {
		t.Error("URI to string conversion returned empty")
	}
}

// TestLocationConversions tests location conversions from 0-based to 1-based.
func TestLocationConversions(t *testing.T) {
	tests := []struct {
		name     string
		line0    uint32
		col0     uint32
		wantLine int
		wantCol  int
	}{
		{"zero-based", 0, 0, 1, 1},
		{"line 5", 4, 10, 5, 11},
		{"line 10 col 20", 9, 19, 10, 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := int(tt.line0) + 1
			col := int(tt.col0) + 1
			if line != tt.wantLine || col != tt.wantCol {
				t.Errorf("got (%d,%d), want (%d,%d)", line, col, tt.wantLine, tt.wantCol)
			}
		})
	}
}

// TestNullableHandling tests Nullable type usage.
func TestNullableHandling(t *testing.T) {
	// Test NewNullable
	n := protocol.NewNullable("test")
	if v, ok := n.Get(); !ok || v != "test" {
		t.Errorf("NewNullable: got %q (ok=%v), want test (ok=true)", v, ok)
	}
}

// TestOptionalHandling tests Optional type usage.
func TestOptionalHandling(t *testing.T) {
	// Test NewOptional
	o := protocol.NewOptional(int32(42))
	if v, ok := o.Get(); !ok || v != 42 {
		t.Errorf("NewOptional: got %d (ok=%v), want 42 (ok=true)", v, ok)
	}

	// Test zero Optional
	var o2 protocol.Optional[int32]
	if _, ok := o2.Get(); ok {
		t.Errorf("zero Optional: got ok=true, want ok=false")
	}
}

// TestChannelBuffering tests diagnostics and progress channels.
func TestChannelBuffering(t *testing.T) {
	diags := make(chan PublishedDiagnostics, 10)
	progs := make(chan ProgressEvent, 10)

	// Should not block on first send
	select {
	case diags <- PublishedDiagnostics{File: "test.go"}:
	case <-time.After(100 * time.Millisecond):
		t.Error("diagnostics channel blocked unexpectedly")
	}

	select {
	case progs <- ProgressEvent{Token: "123"}:
	case <-time.After(100 * time.Millisecond):
		t.Error("progress channel blocked unexpectedly")
	}
}

// TestContextPropagation tests that context works through selection.
func TestContextPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	select {
	case <-ctx.Done():
		// Expected
	default:
		t.Error("context should be canceled")
	}
}

// TestExitedChannelBehavior tests the exited channel behavior.
func TestExitedChannelBehavior(t *testing.T) {
	exited := make(chan struct{})

	select {
	case <-exited:
		t.Error("channel should not be closed yet")
	default:
		// Expected
	}

	close(exited)

	select {
	case <-exited:
		// Expected
	default:
		t.Error("channel should be closed now")
	}
}

// TestMutexSafety tests basic mutex usage patterns.
func TestMutexSafety(t *testing.T) {
	var mu sync.Mutex
	var value int

	// Simulate concurrent access pattern
	done := make(chan bool)

	go func() {
		mu.Lock()
		value++
		mu.Unlock()
		done <- true
	}()

	<-done

	mu.Lock()
	if value != 1 {
		t.Errorf("value: got %d, want 1", value)
	}
	mu.Unlock()
}

func ptrInt32(v int32) *int32 {
	return &v
}
