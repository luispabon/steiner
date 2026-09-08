package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/protocol"

	"github.com/luispabon/steiner/internal/config"
)

func mutateDiagnosticsTestConfig() config.LSPConfig {
	return config.LSPConfig{
		DiagnosticsWindow: config.MustDuration("200ms"),
		MaxResults:        100,
		Servers: map[string]config.LSPServerConfig{
			"go": {
				Enabled:        true,
				FileExtensions: []string{".go"},
			},
		},
	}
}

func TestReadyForFileNoServerForExtension(t *testing.T) {
	tmpdir := t.TempDir()
	m := NewManager(mutateDiagnosticsTestConfig(), tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	if m.ReadyForFile(filepath.Join(tmpdir, "main.rs")) {
		t.Error("expected false for an extension with no configured server")
	}
}

func TestReadyForFileNoServerConfigured(t *testing.T) {
	tmpdir := t.TempDir()
	m := NewManager(config.LSPConfig{Servers: map[string]config.LSPServerConfig{}}, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	if m.ReadyForFile(filepath.Join(tmpdir, "main.go")) {
		t.Error("expected false when no servers are configured at all")
	}
}

func TestReadyForFileNoSessionYet(t *testing.T) {
	tmpdir := t.TempDir()
	m := NewManager(mutateDiagnosticsTestConfig(), tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	// The extension is configured, but no session has ever been started.
	if m.ReadyForFile(filepath.Join(tmpdir, "main.go")) {
		t.Error("expected false when the entry does not exist yet")
	}
}

func TestReadyForFileReadySession(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()
	m.sessions[sessionKey{server: "go", root: tmpdir}] = ent

	file := filepath.Join(tmpdir, "main.go")
	if !m.ReadyForFile(file) {
		t.Error("expected true for a session with ServerStatusReady (absolute path)")
	}
	if !m.ReadyForFile("main.go") {
		t.Error("expected true for a session with ServerStatusReady (workspace-relative path)")
	}
}

func TestReadyForFileNonReadyStatus(t *testing.T) {
	tests := []struct {
		name   string
		status ServerStatus
	}{
		{name: "starting", status: ServerStatusStarting},
		{name: "failed", status: ServerStatusFailed},
		{name: "stopped", status: ServerStatusStopped},
		{name: "declared", status: ServerStatusDeclared},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpdir := t.TempDir()
			cfg := mutateDiagnosticsTestConfig()
			m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
			defer func() { _ = m.Close() }()

			m.sessions[sessionKey{server: "go", root: tmpdir}] = &entry{
				state: ServerState{Status: tt.status},
			}

			file := filepath.Join(tmpdir, "main.go")
			if m.ReadyForFile(file) {
				t.Errorf("expected false for status %q", tt.status)
			}
		})
	}
}

// TestReadyForFileDoesNotSpawn confirms that calling ReadyForFile on a file
// with no matching entry does not cause a new *session* to be created. It does
// not prove zero I/O of any kind, only that no spawn was triggered.
func TestReadyForFileDoesNotSpawn(t *testing.T) {
	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	before := m.ServerStates()

	m.ReadyForFile(filepath.Join(tmpdir, "main.go"))

	after := m.ServerStates()

	if len(before) != 0 || len(after) != 0 {
		t.Fatalf("expected no sessions before or after, got before=%d after=%d", len(before), len(after))
	}
}

// installReadyFakeSession spawns a fake LSP session and registers it directly
// as a ready entry for the "go" server rooted at workspace, bypassing the real
// spawn path.
func installReadyFakeSession(ctx context.Context, t *testing.T, m *Manager, fs *fakeServer, workspace string) {
	t.Helper()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	ent := &entry{
		state:     ServerState{Status: ServerStatusReady},
		session:   sess,
		readiness: newReadiness(m.cfg),
	}
	ent.readiness.markReady()
	m.sessions[sessionKey{server: "go", root: workspace}] = ent
}

func writeTestGoFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func TestPostMutateDiagnosticsReturnsFormattedDiagnostics(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	installReadyFakeSession(ctx, t, m, fs, tmpdir)

	testFile := filepath.Join(tmpdir, "main.go")
	writeTestGoFile(t, testFile)

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			fs.notifyDiagnostics(bgCtx, t, &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{
					{
						Range: protocol.Range{
							Start: protocol.Position{Line: 0, Character: 0},
							End:   protocol.Position{Line: 0, Character: 5},
						},
						Severity: protocol.DiagnosticSeverityError,
						Message:  protocol.String("undefined: foo"),
					},
				},
			})
		}()
	}

	// Pass a workspace-relative path to also exercise absWorkspacePath normalization.
	got := PostMutateDiagnostics(ctx, m, []string{"main.go"})

	want := "Diagnostics after mutation:\nmain.go:1:1 error: undefined: foo"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPostMutateDiagnosticsNoDiagnosticsReturnsEmpty(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	installReadyFakeSession(ctx, t, m, fs, tmpdir)

	testFile := filepath.Join(tmpdir, "main.go")
	writeTestGoFile(t, testFile)

	// No onDidOpen hook: the fake server never publishes diagnostics.

	got := PostMutateDiagnostics(ctx, m, []string{testFile})

	if got != "" {
		t.Fatalf("expected empty string when no diagnostics are published, got %q", got)
	}
}

func TestPostMutateDiagnosticsCapsFileCount(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	// No RootMarkers configured, so every file under tmpdir resolves to the
	// same session key/root, sharing one ready fake session.
	installReadyFakeSession(ctx, t, m, fs, tmpdir)

	const totalFiles = maxMutateDiagnosticsFiles + 2
	files := make([]string, totalFiles)
	for i := 0; i < totalFiles; i++ {
		files[i] = filepath.Join(tmpdir, "file"+string(rune('a'+i))+".go")
		writeTestGoFile(t, files[i])
	}

	var mu sync.Mutex
	opened := make(map[string]bool)
	fs.onDidOpen = func(_ context.Context, params *protocol.DidOpenTextDocumentParams) {
		mu.Lock()
		opened[params.TextDocument.URI.FsPath()] = true
		mu.Unlock()
		// No diagnostics published; we only care which files were opened.
	}

	PostMutateDiagnostics(ctx, m, files)

	mu.Lock()
	defer mu.Unlock()

	for i, file := range files {
		if i < maxMutateDiagnosticsFiles {
			if !opened[file] {
				t.Errorf("expected file %d (%s) within the cap to be queried", i, file)
			}
			continue
		}
		if opened[file] {
			t.Errorf("expected file %d (%s) beyond the cap to never be queried", i, file)
		}
	}
}

func TestPostMutateDiagnosticsNilManager(t *testing.T) {
	ctx := context.Background()
	got := PostMutateDiagnostics(ctx, nil, []string{"whatever.go"})
	if got != "" {
		t.Fatalf("expected empty string for nil manager, got %q", got)
	}
}

func TestPostMutateDiagnosticsCancelledContext(t *testing.T) {
	fs := newFakeServer()
	spawnCtx, spawnCancel := context.WithTimeout(context.Background(), testTimeout)
	defer spawnCancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	installReadyFakeSession(spawnCtx, t, m, fs, tmpdir)

	testFile := filepath.Join(tmpdir, "main.go")
	writeTestGoFile(t, testFile)

	var didOpenCalled atomic.Bool
	fs.onDidOpen = func(context.Context, *protocol.DidOpenTextDocumentParams) {
		didOpenCalled.Store(true)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := PostMutateDiagnostics(ctx, m, []string{testFile})

	if got != "" {
		t.Fatalf("expected empty string for an already-cancelled context, got %q", got)
	}
	if didOpenCalled.Load() {
		t.Error("expected no didOpen RPC to be sent for an already-cancelled context")
	}
}

// TestPostMutateDiagnosticsCapsLinesAndReportsOmitted verifies the 40-line
// rendering cap and its trailer, using a single file that publishes more
// diagnostics than the cap.
func TestPostMutateDiagnosticsCapsLinesAndReportsOmitted(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	cfg.MaxResults = 100
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	installReadyFakeSession(ctx, t, m, fs, tmpdir)

	testFile := filepath.Join(tmpdir, "main.go")
	writeTestGoFile(t, testFile)

	const totalDiags = maxMutateDiagnosticsLines + 5

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			diags := make([]protocol.Diagnostic, totalDiags)
			for i := 0; i < totalDiags; i++ {
				diags[i] = protocol.Diagnostic{
					Range: protocol.Range{
						Start: protocol.Position{Line: uint32(i), Character: 0},
						End:   protocol.Position{Line: uint32(i), Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String(fmt.Sprintf("error %d", i)),
				}
			}
			fs.notifyDiagnostics(bgCtx, t, &protocol.PublishDiagnosticsParams{
				URI:         params.TextDocument.URI,
				Diagnostics: diags,
			})
		}()
	}

	got := PostMutateDiagnostics(ctx, m, []string{testFile})

	lines := strings.Split(got, "\n")
	// Header + maxMutateDiagnosticsLines diagnostic lines + trailer.
	wantLineCount := 1 + maxMutateDiagnosticsLines + 1
	if len(lines) != wantLineCount {
		t.Fatalf("got %d lines, want %d; output:\n%s", len(lines), wantLineCount, got)
	}

	wantTrailer := "... 5 more diagnostics omitted"
	if lines[len(lines)-1] != wantTrailer {
		t.Errorf("trailer = %q, want %q", lines[len(lines)-1], wantTrailer)
	}
}

// TestPostMutateDiagnosticsSortsAcrossFiles verifies that diagnostics for
// multiple files are interleaved in file-path order, independent of the
// order the files were passed in or the order the server published them.
func TestPostMutateDiagnosticsSortsAcrossFiles(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	tmpdir := t.TempDir()
	cfg := mutateDiagnosticsTestConfig()
	m := NewManager(cfg, tmpdir, nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	// No RootMarkers, so both files share one session/entry.
	installReadyFakeSession(ctx, t, m, fs, tmpdir)

	fileA := filepath.Join(tmpdir, "a.go")
	fileB := filepath.Join(tmpdir, "b.go")
	writeTestGoFile(t, fileA)
	writeTestGoFile(t, fileB)

	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			bgCtx := context.WithoutCancel(ctx)
			var msg string
			switch params.TextDocument.URI.FsPath() {
			case fileA:
				msg = "issue in a"
			case fileB:
				msg = "issue in b"
			default:
				return
			}
			fs.notifyDiagnostics(bgCtx, t, &protocol.PublishDiagnosticsParams{
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
			})
		}()
	}

	// Pass B before A; same session key means requests run sequentially in
	// this given order, but the rendered output must still sort by file path.
	got := PostMutateDiagnostics(ctx, m, []string{fileB, fileA})

	idxA := strings.Index(got, "a.go")
	idxB := strings.Index(got, "b.go")
	if idxA == -1 || idxB == -1 {
		t.Fatalf("expected both files' diagnostics in output, got %q", got)
	}
	if idxA > idxB {
		t.Errorf("expected a.go's diagnostic before b.go's, got %q", got)
	}
}
