package lsp

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/luispabon/steiner/internal/config"
)

func TestAbsWorkspacePath(t *testing.T) {
	tests := []struct {
		name      string
		workspace string
		file      string
		want      string
		wantErr   bool
	}{
		{name: "relative joins workspace", workspace: "/ws", file: "pkg/main.go", want: "/ws/pkg/main.go"},
		{name: "relative is cleaned", workspace: "/ws", file: "./pkg/../main.go", want: "/ws/main.go"},
		{name: "absolute is kept", workspace: "/ws", file: "/other/main.go", want: "/other/main.go"},
		{name: "absolute is cleaned", workspace: "/ws", file: "/other/./sub/../main.go", want: "/other/main.go"},
		{name: "empty file errors", workspace: "/ws", file: "  ", wantErr: true},
		{name: "relative without workspace errors", workspace: "", file: "main.go", wantErr: true},
		{name: "absolute without workspace is kept", workspace: "", file: "/main.go", want: "/main.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := absWorkspacePath(tt.workspace, tt.file)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("absWorkspacePath(%q, %q) = %q, want error", tt.workspace, tt.file, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("absWorkspacePath(%q, %q): %v", tt.workspace, tt.file, err)
			}
			if got != tt.want {
				t.Errorf("absWorkspacePath(%q, %q) = %q, want %q", tt.workspace, tt.file, got, tt.want)
			}
		})
	}
}

// opOutcome is the observable part of an operation's result, shared across the
// navigation and diagnostics shapes so both spellings can be compared directly.
type opOutcome struct {
	Locations     []Location
	Diagnostics   []Diagnostic
	WindowExpired bool
}

// TestOperationsResolveRelativePaths asserts that every operation resolves its
// file against the manager's workspace, so a workspace-relative path and its
// absolute twin route to the same session, open the same document and return
// the same results.
func TestOperationsResolveRelativePaths(t *testing.T) {
	definitionTarget := protocol.Location{
		URI: uri.File("/target/defined.go"),
		Range: protocol.Range{
			Start: protocol.Position{Line: 4, Character: 9},
			End:   protocol.Position{Line: 4, Character: 15},
		},
	}

	ops := []struct {
		name    string
		prepare func(fs *fakeServer)
		publish func(ctx context.Context, t *testing.T, fs *fakeServer, params *protocol.DidOpenTextDocumentParams)
		run     func(ctx context.Context, m *Manager, file string) (opOutcome, error)
	}{
		{
			name: "definitions",
			prepare: func(fs *fakeServer) {
				fs.definitionResult = &definitionTarget
			},
			run: func(ctx context.Context, m *Manager, file string) (opOutcome, error) {
				res, err := m.Definitions(ctx, file, 1, 1)
				return opOutcome{Locations: res.Locations}, err
			},
		},
		{
			name: "references",
			prepare: func(fs *fakeServer) {
				fs.referencesResult = []protocol.Location{definitionTarget}
			},
			run: func(ctx context.Context, m *Manager, file string) (opOutcome, error) {
				res, err := m.References(ctx, file, 1, 1, true)
				return opOutcome{Locations: res.Locations}, err
			},
		},
		{
			name: "diagnostics",
			publish: func(ctx context.Context, t *testing.T, fs *fakeServer, params *protocol.DidOpenTextDocumentParams) {
				bgCtx := context.WithoutCancel(ctx)
				go func() {
					fs.notifyDiagnostics(bgCtx, t, &protocol.PublishDiagnosticsParams{
						URI: params.TextDocument.URI,
						Diagnostics: []protocol.Diagnostic{
							{
								Range: protocol.Range{
									Start: protocol.Position{Line: 0, Character: 0},
									End:   protocol.Position{Line: 0, Character: 5},
								},
								Severity: protocol.DiagnosticSeverityError,
								Message:  protocol.String("boom"),
							},
						},
					})
				}()
			},
			run: func(ctx context.Context, m *Manager, file string) (opOutcome, error) {
				res, err := m.Diagnostics(ctx, file)
				return opOutcome{Diagnostics: res.Items, WindowExpired: res.WindowExpired}, err
			},
		},
	}

	for _, op := range ops {
		t.Run(op.name, func(t *testing.T) {
			var outcomes []opOutcome

			// Each spelling gets its own manager and server so the second run is
			// a genuine round trip rather than a result-cache hit.
			for _, spelling := range []string{"absolute", "relative"} {
				ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
				defer cancel()

				workspace := t.TempDir()
				projDir := filepath.Join(workspace, "proj")
				if err := os.MkdirAll(projDir, 0o755); err != nil {
					t.Fatalf("mkdir proj: %v", err)
				}
				if err := os.WriteFile(filepath.Join(projDir, "go.mod"), []byte("module proj\n"), 0o644); err != nil {
					t.Fatalf("write marker: %v", err)
				}
				absFile := filepath.Join(projDir, "sample_target.go")
				if err := os.WriteFile(absFile, []byte("package proj\n"), 0o644); err != nil {
					t.Fatalf("write file: %v", err)
				}

				fs := newFakeServer()
				if op.prepare != nil {
					op.prepare(fs)
				}

				var openedMu sync.Mutex
				var opened []string
				fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
					openedMu.Lock()
					opened = append(opened, params.TextDocument.URI.FsPath())
					openedMu.Unlock()
					if op.publish != nil {
						op.publish(ctx, t, fs, params)
					}
				}

				m := newManagerWithFakeSession(ctx, t, fs, workspace, projDir)

				file := absFile
				if spelling == "relative" {
					file = filepath.Join("proj", "sample_target.go")
				}

				outcome, err := op.run(ctx, m, file)
				if err != nil {
					t.Fatalf("%s path: %v", spelling, err)
				}
				// Each run gets its own temp workspace, so compare paths relative
				// to it rather than the tmpdir-specific absolute form.
				for i := range outcome.Diagnostics {
					outcome.Diagnostics[i].File = makeRelative(workspace, outcome.Diagnostics[i].File)
				}
				outcomes = append(outcomes, outcome)

				openedMu.Lock()
				gotOpened := append([]string(nil), opened...)
				openedMu.Unlock()
				if len(gotOpened) != 1 || gotOpened[0] != absFile {
					t.Fatalf("%s path: opened documents = %v, want [%s]", spelling, gotOpened, absFile)
				}

				// A misresolved path would miss the pre-registered session and
				// register a second one under a different root.
				states := m.ServerStates()
				if len(states) != 1 {
					t.Fatalf("%s path: %d server states, want 1: %+v", spelling, len(states), states)
				}
				if states[0].Root != projDir {
					t.Errorf("%s path: session root = %q, want %q", spelling, states[0].Root, projDir)
				}
			}

			if !reflect.DeepEqual(outcomes[0], outcomes[1]) {
				t.Errorf("relative path outcome %+v differs from absolute %+v", outcomes[1], outcomes[0])
			}
			if outcomes[0].WindowExpired {
				t.Error("expected WindowExpired=false (server published for the requested file)")
			}
		})
	}
}

// newManagerWithFakeSession returns a Manager whose only session is fs, already
// registered as ready under root, so operations run without spawning a process.
func newManagerWithFakeSession(ctx context.Context, t *testing.T, fs *fakeServer, workspace, root string) *Manager {
	t.Helper()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	cfg := config.LSPConfig{
		Enabled:           true,
		IdleTimeout:       config.MustDuration("30s"),
		RequestTimeout:    config.MustDuration("2s"),
		ReadyTimeout:      config.MustDuration("2s"),
		ReadyGracePeriod:  config.MustDuration("50ms"),
		DiagnosticsWindow: config.MustDuration("500ms"),
		MaxResults:        100,
		Servers: map[string]config.LSPServerConfig{
			"go": {
				Enabled:        true,
				Command:        "/nonexistent/lsp",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
			},
		},
	}

	m := NewManager(cfg, workspace, nil, func(string) {}, nil)
	t.Cleanup(func() { _ = m.Close() })

	ent := &entry{
		state: ServerState{
			Name:      "go",
			Root:      root,
			Status:    ServerStatusReady,
			StartedAt: time.Now(),
			LastUsed:  time.Now(),
		},
		session:   sess,
		readiness: newReadiness(cfg),
	}
	ent.readiness.markReady()
	m.sessions[sessionKey{server: "go", root: root}] = ent

	return m
}
