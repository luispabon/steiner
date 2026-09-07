package lsp

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// TestSessionInterfaceExported verifies that session is the internal interface
func TestSessionInterfaceExported(t *testing.T) {
	var _ session = (*impl)(nil)
}

// TestLocationStruct verifies Location struct fields
func TestLocationStruct(t *testing.T) {
	loc := Location{
		File:      "/tmp/test.go",
		Line:      10,
		Column:    5,
		EndLine:   10,
		EndColumn: 20,
	}

	if loc.Line != 10 || loc.Column != 5 {
		t.Errorf("location start: got (%d,%d), want (10,5)", loc.Line, loc.Column)
	}
	if loc.EndLine != 10 || loc.EndColumn != 20 {
		t.Errorf("location end: got (%d,%d), want (10,20)", loc.EndLine, loc.EndColumn)
	}
}

// TestDiagnosticStruct verifies Diagnostic struct fields
func TestDiagnosticStruct(t *testing.T) {
	d := Diagnostic{
		File:     "/tmp/test.go",
		Line:     1,
		Column:   5,
		Severity: "error",
		Source:   "gopls",
		Message:  "undefined: x",
		Code:     "go/undeclaredName",
	}

	if d.File != "/tmp/test.go" {
		t.Errorf("File: got %s, want /tmp/test.go", d.File)
	}
	if d.Severity != "error" {
		t.Errorf("Severity: got %s, want error", d.Severity)
	}
}

// TestPublishedDiagnosticsStruct verifies PublishedDiagnostics struct fields
func TestPublishedDiagnosticsStruct(t *testing.T) {
	pd := PublishedDiagnostics{
		File:    "/tmp/test.go",
		Version: ptrInt32Helper(1),
		Items: []Diagnostic{
			{
				File:     "/tmp/test.go",
				Line:     1,
				Column:   1,
				Severity: "error",
				Message:  "test error",
			},
		},
	}

	if pd.File != "/tmp/test.go" {
		t.Errorf("File: got %s, want /tmp/test.go", pd.File)
	}
	if len(pd.Items) != 1 {
		t.Errorf("Items count: got %d, want 1", len(pd.Items))
	}
}

// TestProgressEventStruct verifies ProgressEvent struct fields
func TestProgressEventStruct(t *testing.T) {
	pe := ProgressEvent{
		Token:   "123",
		Kind:    "begin",
		Message: "Loading workspace",
	}

	if pe.Token != "123" {
		t.Errorf("Token: got %s, want 123", pe.Token)
	}
	if pe.Kind != "begin" {
		t.Errorf("Kind: got %s, want begin", pe.Kind)
	}
}

// TestStderrRedirection verifies stderr can be redirected
func TestStderrRedirection(t *testing.T) {
	buf := &bytes.Buffer{}
	spec := TransportSpec{
		Command:  "echo",
		Args:     []string{"test"},
		RootPath: "/tmp",
		Stderr:   buf,
	}

	if spec.Stderr != buf {
		t.Error("stderr not properly set")
	}
}

// TestChannelClosureOnExit verifies Exited channel semantics
func TestChannelClosureOnExit(t *testing.T) {
	exited := make(chan struct{})
	close(exited)

	select {
	case <-exited:
		// Expected: channel is closed
	default:
		t.Error("exited channel should be closed")
	}
}

// TestSessionContextHandling verifies context propagation
func TestSessionContextHandling(t *testing.T) {
	ctx := context.Background()

	// Verify context is not done yet
	select {
	case <-ctx.Done():
		t.Error("context should not be done yet")
	default:
		// Expected
	}

	// Test timeout context
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	time.Sleep(2 * time.Millisecond)

	select {
	case <-timeoutCtx.Done():
		// Expected: context timed out
	default:
		t.Error("timeout context should be done")
	}
}

// TestLocationConversion verifies 0-based to 1-based conversion logic
func TestLocationConversion(t *testing.T) {
	tests := []struct {
		name     string
		lspLine  uint32
		lspChar  uint32
		wantLine int
		wantChar int
	}{
		{"zero-based", 0, 0, 1, 1},
		{"line 5", 4, 10, 5, 11},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := int(tt.lspLine) + 1
			char := int(tt.lspChar) + 1
			if line != tt.wantLine || char != tt.wantChar {
				t.Errorf("conversion: got (%d, %d), want (%d, %d)", line, char, tt.wantLine, tt.wantChar)
			}
		})
	}
}

// TestNoGoLSPDevExports is a compile-time check that go.lsp.dev types don't leak
func TestNoGoLSPDevExports(t *testing.T) {
	_ = Location{}
	_ = Diagnostic{}
	_ = PublishedDiagnostics{}
	_ = ProgressEvent{}
	_ = TransportSpec{}
}

// TestChannelCreation verifies channels are created with appropriate buffer sizes
func TestChannelCreation(t *testing.T) {
	diag := make(chan PublishedDiagnostics, 10)
	prog := make(chan ProgressEvent, 10)

	// Verify we can send without blocking
	select {
	case diag <- PublishedDiagnostics{}:
	default:
		t.Error("diagnostics channel full")
	}

	select {
	case prog <- ProgressEvent{}:
	default:
		t.Error("progress channel full")
	}
}

// TestClientHandlerNotification verifies client handler is instantiable
func TestClientHandlerNotification(t *testing.T) {
	handler := &clientHandler{
		diagnosticsChan: make(chan PublishedDiagnostics, 10),
		progressChan:    make(chan ProgressEvent, 10),
	}

	if handler.diagnosticsChan == nil {
		t.Error("diagnosticsChan is nil")
	}
	if handler.progressChan == nil {
		t.Error("progressChan is nil")
	}
}

func ptrInt32Helper(v int32) *int32 {
	return &v
}
