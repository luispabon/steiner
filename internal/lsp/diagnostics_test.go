package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// TestDiagnosticsSinglePublication tests that a single publication for the
// requested file is returned correctly (scenario 1).
func TestDiagnosticsSinglePublication(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("5s"),
		MaxResults:        100,
	}

	// Create an entry with readiness.
	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	// Set up the fake server to publish diagnostics when the file is opened.
	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		// Publish diagnostics for the opened file, with a small delay to ensure
		// the collection loop is ready to receive (wrap in goroutine to avoid blocking).
		go func() {
			time.Sleep(5 * time.Millisecond)
			diagParams := &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 5},
						},
						Severity: protocol.DiagnosticSeverityError,
						Source:   protocol.NewOptional("test"),
						Message:  protocol.String("test error"),
					},
				},
			}
			// Use a non-cancellable context so the publish completes even if the
			// request context is cancelled.
			bgCtx := context.WithoutCancel(ctx)
			fs.notifyDiagnostics(bgCtx, t, diagParams)
		}()
	}

	// Call collectDiagnostics with the lock held (as production does).
	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	if len(result.Items) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(result.Items))
	}

	if result.Items[0].Message != "test error" {
		t.Errorf("expected message 'test error', got %q", result.Items[0].Message)
	}

	if result.Truncated {
		t.Error("expected Truncated=false")
	}

	if result.WindowExpired {
		t.Error("expected WindowExpired=false (server published promptly)")
	}
}

// TestDiagnosticsReplaceNotAppend tests that a second publication for the SAME
// file within the window REPLACES the first, not appended (scenario 2).
func TestDiagnosticsReplaceNotAppend(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("200ms"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	// Publish two diagnostics in sequence; the second should replace the first.
	publishCount := 0
	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		publishCount++
		if publishCount == 1 {
			go func() {
				// First publication: one error.
				time.Sleep(10 * time.Millisecond)
				diagParams := &protocol.PublishDiagnosticsParams{
					URI: params.TextDocument.URI,
					Diagnostics: []protocol.Diagnostic{
						{
							Range: protocol.Range{
								Start: protocol.Position{Line: 0, Character: 0},
								End:   protocol.Position{Line: 0, Character: 5},
							},
							Severity: protocol.DiagnosticSeverityError,
							Message:  protocol.String("first error"),
						},
					},
				}
				bgCtx := context.WithoutCancel(ctx)
				fs.notifyDiagnostics(bgCtx, t, diagParams)

				// Publish again shortly after: one warning (replaces the error).
				time.Sleep(30 * time.Millisecond)
				diagParams2 := &protocol.PublishDiagnosticsParams{
					URI: params.TextDocument.URI,
					Diagnostics: []protocol.Diagnostic{
						{
							Range: protocol.Range{
								Start: protocol.Position{Line: 1, Character: 0},
								End:   protocol.Position{Line: 1, Character: 5},
							},
							Severity: protocol.DiagnosticSeverityWarning,
							Message:  protocol.String("second warning"),
						},
					},
				}
				fs.notifyDiagnostics(bgCtx, t, diagParams2)
			}()
		}
	}

	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	// Should have only the second publication's diagnostic.
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(result.Items))
	}

	if result.Items[0].Message != "second warning" {
		t.Errorf("expected message 'second warning' (replacement), got %q", result.Items[0].Message)
	}

	if result.Items[0].Severity != "warning" {
		t.Errorf("expected severity 'warning', got %q", result.Items[0].Severity)
	}
}

// TestDiagnosticsOtherFilesExcluded tests that publications for unrelated files
// are excluded from the result (scenario 3).
func TestDiagnosticsOtherFilesExcluded(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	otherFile := filepath.Join(tmpdir, "other.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write testFile: %v", err)
	}
	if err := os.WriteFile(otherFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write otherFile: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("5s"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			// Publish diagnostics for the opened file.
			diagParams := &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 5},
						},
						Severity: protocol.DiagnosticSeverityError,
						Message:  protocol.String("opened file error"),
					},
				},
			}
			fs.notifyDiagnostics(bgCtx, t, diagParams)

			// Also publish diagnostics for the OTHER file; these should be discarded.
			diagParams2 := &protocol.PublishDiagnosticsParams{
				URI: uri.File(otherFile),
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 5},
						},
						Severity: protocol.DiagnosticSeverityWarning,
						Message:  protocol.String("other file warning"),
					},
				},
			}
			fs.notifyDiagnostics(bgCtx, t, diagParams2)
		}()
	}

	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	// Should have only the diagnostic for testFile, not otherFile.
	if len(result.Items) != 1 {
		t.Fatalf("expected 1 diagnostic (for testFile), got %d", len(result.Items))
	}

	if result.Items[0].Message != "opened file error" {
		t.Errorf("expected message 'opened file error', got %q", result.Items[0].Message)
	}
}

// TestDiagnosticsEmptyWindow tests that an empty window (server never publishes)
// returns zero items, WindowExpired=true, and a nil error (scenario 4).
func TestDiagnosticsEmptyWindow(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("50ms"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	// Don't set onDidOpen, so no diagnostics are published.

	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	if len(result.Items) != 0 {
		t.Fatalf("expected 0 diagnostics, got %d", len(result.Items))
	}

	if !result.WindowExpired {
		t.Error("expected WindowExpired=true (window timed out)")
	}

	if result.Truncated {
		t.Error("expected Truncated=false")
	}
}

// TestDiagnosticsCapAtMaxResults tests that over-cap results are truncated to
// MaxResults with Truncated=true (scenario 5).
func TestDiagnosticsCapAtMaxResults(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("100ms"),
		MaxResults:        3,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			// Publish 5 diagnostics (over the cap of 3).
			diags := make([]protocol.Diagnostic, 5)
			for i := 0; i < 5; i++ {
				diags[i] = protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String(fmt.Sprintf("error %d", i)),
				}
			}

			diagParams := &protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diags,
			}
			fs.notifyDiagnostics(bgCtx, t, diagParams)
		}()
	}

	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	if len(result.Items) != 3 {
		t.Fatalf("expected 3 diagnostics (capped), got %d", len(result.Items))
	}

	if !result.Truncated {
		t.Error("expected Truncated=true")
	}
}

// TestDiagnosticsSeverityOrdering tests that diagnostics of different severities
// sort in the documented rank order (error < warning < information < hint < unknown)
// (scenario 6).
func TestDiagnosticsSeverityOrdering(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("5s"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			// Publish diagnostics in reverse severity order; they should be sorted.
			diagParams := &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 1},
						},
						Severity: protocol.DiagnosticSeverityHint,
						Message:  protocol.String("hint"),
					},
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 1},
						},
						Severity: protocol.DiagnosticSeverityWarning,
						Message:  protocol.String("warning"),
					},
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 1},
						},
						Severity: protocol.DiagnosticSeverityError,
						Message:  protocol.String("error"),
					},
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 1},
						},
						Severity: protocol.DiagnosticSeverityInformation,
						Message:  protocol.String("information"),
					},
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 1},
						},
						Severity: protocol.DiagnosticSeverity(7), // unknown
						Message:  protocol.String("unknown"),
					},
				},
			}
			fs.notifyDiagnostics(bgCtx, t, diagParams)
		}()
	}

	ent.cycleMu.Lock()
	result, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("collectDiagnostics: %v", err)
	}

	if len(result.Items) != 5 {
		t.Fatalf("expected 5 diagnostics, got %d", len(result.Items))
	}

	expected := []string{"error", "warning", "information", "hint", "unknown"}
	for i, item := range result.Items {
		if item.Message != expected[i] {
			t.Errorf("diagnostic %d: expected message %q, got %q", i, expected[i], item.Message)
		}
	}
}

// TestDiagnosticsStaleNotificationsNotLeaking tests that multiple consecutive
// calls to collectDiagnostics don't interfere with each other. The drain at the
// beginning of each cycle ensures stale notifications don't leak (scenario 7).
// This is tested implicitly: if drain didn't work, earlier tests would have
// stale diagnostics interfere with later results, causing other tests to fail.
// Here we just verify that sequential calls work correctly.
func TestDiagnosticsStaleNotificationsNotLeaking(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("100ms"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	// First collectDiagnostics call - should get "first" diagnostic.
	firstCallDone := false
	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		if !firstCallDone {
			firstCallDone = true
			go func() {
				time.Sleep(5 * time.Millisecond)
				bgCtx := context.WithoutCancel(ctx)
				diagParams := &protocol.PublishDiagnosticsParams{
					URI: params.TextDocument.URI,
					Diagnostics: []protocol.Diagnostic{
						{
							Range: protocol.Range{
								Start: protocol.Position{Line: 0, Character: 0},
								End:   protocol.Position{Line: 0, Character: 5},
							},
							Severity: protocol.DiagnosticSeverityError,
							Message:  protocol.String("first"),
						},
					},
				}
				bgCtx = context.WithoutCancel(ctx)
				fs.notifyDiagnostics(bgCtx, t, diagParams)
			}()
		} else {
			// Second call - publish "second" diagnostic.
			go func() {
				time.Sleep(5 * time.Millisecond)
				bgCtx := context.WithoutCancel(ctx)
				diagParams := &protocol.PublishDiagnosticsParams{
					URI: params.TextDocument.URI,
					Diagnostics: []protocol.Diagnostic{
						{
							Range: protocol.Range{
								Start: protocol.Position{Line: 0, Character: 0},
								End:   protocol.Position{Line: 0, Character: 5},
							},
							Severity: protocol.DiagnosticSeverityWarning,
							Message:  protocol.String("second"),
						},
					},
				}
				fs.notifyDiagnostics(bgCtx, t, diagParams)
			}()
		}
	}

	// First call
	ent.cycleMu.Lock()
	result1, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("first collectDiagnostics: %v", err)
	}

	if len(result1.Items) != 1 {
		t.Fatalf("first call: expected 1 diagnostic, got %d", len(result1.Items))
	}
	if result1.Items[0].Message != "first" {
		t.Errorf("first call: expected 'first', got %q", result1.Items[0].Message)
	}

	// Second call - should get "second", not "first" (no leak from first call)
	ent.cycleMu.Lock()
	result2, err := (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, testFile)
	ent.cycleMu.Unlock()

	if err != nil {
		t.Fatalf("second collectDiagnostics: %v", err)
	}

	if len(result2.Items) != 1 {
		t.Fatalf("second call: expected 1 diagnostic, got %d", len(result2.Items))
	}
	if result2.Items[0].Message != "second" {
		t.Errorf("second call: expected 'second', got %q", result2.Items[0].Message)
	}
	if result2.Items[0].Severity != "warning" {
		t.Errorf("second call: expected severity 'warning', got %q", result2.Items[0].Severity)
	}
}

// TestDiagnosticsConcurrentNonInterleaving tests that two concurrent Diagnostics
// calls on one session, each for a different file, never interleave and each gets
// only its own file's results (scenario 8 — THE CONCURRENCY TEST).
func TestDiagnosticsConcurrentNonInterleaving(t *testing.T) {
	fs := newFakeServer()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	fileA := filepath.Join(tmpdir, "a.go")
	fileB := filepath.Join(tmpdir, "b.go")
	if err := os.WriteFile(fileA, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write fileA: %v", err)
	}
	if err := os.WriteFile(fileB, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write fileB: %v", err)
	}

	cfg := config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("200ms"),
		MaxResults:        100,
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()

	// Track which file is being opened.
	var currentOpenedFile string
	var currentOpenedMu sync.Mutex

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		currentOpenedMu.Lock()
		currentOpenedFile = params.TextDocument.URI.FsPath()
		currentOpenedMu.Unlock()

		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			// Publish diagnostics specific to the opened file.
			var msg string
			if currentOpenedFile == fileA {
				msg = "diagnostic for A"
			} else if currentOpenedFile == fileB {
				msg = "diagnostic for B"
			} else {
				return
			}

			diagParams := &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 5},
						},
						Severity: protocol.DiagnosticSeverityError,
						Message:  protocol.String(msg),
					},
				},
			}
			fs.notifyDiagnostics(bgCtx, t, diagParams)
		}()
	}

	var wg sync.WaitGroup
	var resultA, resultB DiagResult
	var errA, errB error

	// Launch two concurrent collectDiagnostics calls on the same entry and session,
	// one for each file. They should serialize at the cycleMu level.
	wg.Add(2)
	go func() {
		defer wg.Done()
		ent.cycleMu.Lock()
		defer ent.cycleMu.Unlock()
		resultA, errA = (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, fileA)
	}()
	go func() {
		defer wg.Done()
		ent.cycleMu.Lock()
		defer ent.cycleMu.Unlock()
		resultB, errB = (&Manager{cfg: cfg}).collectDiagnostics(ctx, ent, sess, fileB)
	}()

	wg.Wait()

	if errA != nil {
		t.Fatalf("collectDiagnostics for A: %v", errA)
	}
	if errB != nil {
		t.Fatalf("collectDiagnostics for B: %v", errB)
	}

	// Verify that resultA contains only A's diagnostic.
	if len(resultA.Items) != 1 {
		t.Fatalf("resultA: expected 1 diagnostic, got %d", len(resultA.Items))
	}
	if resultA.Items[0].Message != "diagnostic for A" {
		t.Errorf("resultA: expected message 'diagnostic for A', got %q", resultA.Items[0].Message)
	}

	// Verify that resultB contains only B's diagnostic.
	if len(resultB.Items) != 1 {
		t.Fatalf("resultB: expected 1 diagnostic, got %d", len(resultB.Items))
	}
	if resultB.Items[0].Message != "diagnostic for B" {
		t.Errorf("resultB: expected message 'diagnostic for B', got %q", resultB.Items[0].Message)
	}

	// Verify that the recorded method sequence shows two complete non-interleaved cycles.
	// Each cycle is: didOpen → didClose (definition is not used here).
	methods := fs.recorded()
	if !isNonInterleavedDiagnosticsCycles(methods) {
		t.Errorf("methods are interleaved (concurrency bug): %v", methods)
	}
}

// isNonInterleavedDiagnosticsCycles checks that the method sequence shows two
// complete open→close cycles without interleaving. For diagnostics tests, a cycle
// is didOpen → didClose (no definition in between).
func isNonInterleavedDiagnosticsCycles(methods []string) bool {
	// Find the first didOpen.
	i := 0
	for i < len(methods) && methods[i] != "textDocument/didOpen" {
		i++
	}

	if i >= len(methods) {
		return false
	}

	// Extract two cycles: each cycle is didOpen → didClose.
	cycles := 0
	for i < len(methods) {
		if i+2 <= len(methods) &&
			methods[i] == "textDocument/didOpen" &&
			methods[i+1] == "textDocument/didClose" {
			cycles++
			i += 2
		} else if i < len(methods) && methods[i] == "textDocument/didOpen" {
			// Found a didOpen at position i, but it's not followed by didClose.
			return false
		} else {
			// Skip non-cycle methods.
			i++
		}
	}

	return cycles == 2
}
