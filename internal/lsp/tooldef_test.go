package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.lsp.dev/protocol"

	"github.com/luispabon/steiner/internal/config"
)

func TestToolDefsCount(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	if len(defs) != 4 {
		t.Fatalf("ToolDefs returned %d tools, want 4", len(defs))
	}
}

func TestToolDefsNames(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	expectedNames := []string{"lsp_definitions", "lsp_references", "lsp_diagnostics", "lsp_hover"}

	for i, expected := range expectedNames {
		if i >= len(defs) {
			t.Fatalf("ToolDefs returned %d tools, expected at least %d", len(defs), i+1)
		}
		if defs[i].Name != expected {
			t.Errorf("ToolDefs[%d].Name = %q, want %q", i, defs[i].Name, expected)
		}
	}
}

func TestToolDefsOrdering(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LSPConfig
	}{
		{
			name: "empty servers",
			cfg:  config.LSPConfig{},
		},
		{
			name: "servers in reverse order",
			cfg: config.LSPConfig{
				Servers: map[string]config.LSPServerConfig{
					"z-server": {Enabled: true},
					"a-server": {Enabled: true},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m1 := NewManager(tt.cfg, "/workspace", nil, func(string) {}, nil)
			defer func() { _ = m1.Close() }()
			defs1 := ToolDefs(m1)

			m2 := NewManager(tt.cfg, "/workspace", nil, func(string) {}, nil)
			defer func() { _ = m2.Close() }()
			defs2 := ToolDefs(m2)

			if len(defs1) != len(defs2) {
				t.Errorf("ToolDefs count mismatch: %d vs %d", len(defs1), len(defs2))
				return
			}

			for i := range defs1 {
				if defs1[i].Name != defs2[i].Name {
					t.Errorf("ToolDefs[%d].Name mismatch: %q vs %q", i, defs1[i].Name, defs2[i].Name)
				}
			}

			expectedOrder := []string{"lsp_definitions", "lsp_references", "lsp_diagnostics", "lsp_hover"}
			for i, expected := range expectedOrder {
				if i >= len(defs1) {
					break
				}
				if defs1[i].Name != expected {
					t.Errorf("Tool at position %d: got %q, want %q", i, defs1[i].Name, expected)
				}
			}
		})
	}
}

func TestToolDefsParallelSafe(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	for _, def := range defs {
		if !def.ParallelSafe {
			t.Errorf("Tool %q: ParallelSafe = false, want true", def.Name)
		}
	}
}

func TestToolDefsSchemas(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)

	tests := []struct {
		name       string
		toolIdx    int
		properties []string
		required   []string
	}{
		{
			name:       "definitions schema",
			toolIdx:    0,
			properties: []string{"file", "line", "column", "symbol"},
			required:   []string{"file"},
		},
		{
			name:       "references schema",
			toolIdx:    1,
			properties: []string{"file", "line", "column", "symbol", "include_declaration"},
			required:   []string{"file"},
		},
		{
			name:       "diagnostics schema",
			toolIdx:    2,
			properties: []string{"file"},
			required:   []string{"file"},
		},
		{
			name:       "hover schema",
			toolIdx:    3,
			properties: []string{"file", "line", "column", "symbol"},
			required:   []string{"file"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := defs[tt.toolIdx].ParameterSchema
			props, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema has no properties object")
			}

			for _, prop := range tt.properties {
				if _, ok := props[prop]; !ok {
					t.Errorf("property %q not in properties", prop)
				}
			}

			reqList, ok := schema["required"].([]string)
			if !ok {
				t.Fatalf("schema has no required array")
			}
			for _, req := range tt.required {
				found := false
				for _, r := range reqList {
					if r == req {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("required field %q not in required list", req)
				}
			}
		})
	}
}

func TestReferencesIncludeDeclarationDefault(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"test": {
				Enabled:        true,
				FileExtensions: []string{".test"},
				Command:        "echo",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	refTool := defs[1]

	schema := refTool.ParameterSchema
	props := schema["properties"].(map[string]any)
	decl := props["include_declaration"].(map[string]any)

	defaultVal, ok := decl["default"]
	if !ok {
		t.Errorf("include_declaration schema missing default")
	}
	if v, ok := defaultVal.(bool); ok && !v {
		t.Errorf("include_declaration default = %v, want true", v)
	}
}

func TestNoServerError(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)

	input := map[string]any{
		"file":   "test.unknown",
		"line":   1.0,
		"column": 1.0,
	}

	result, err := defs[0].Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler returned error: %v", err)
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "No language server is configured") {
		t.Errorf("message does not contain expected unavailability text: %q", msg)
	}
	if !strings.Contains(msg, ".unknown") {
		t.Errorf("message does not contain file extension: %q", msg)
	}
}

func TestServerExitedError(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	result, goErr := handleNavigationError(m, "test.go", errServerExited)
	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if goErr != nil {
		t.Fatalf("expected nil error, got %v", goErr)
	}

	if !strings.Contains(msg, "exited unexpectedly") {
		t.Errorf("message does not contain expected text: %q", msg)
	}
}

func TestFailedServerError(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	err := errors.New("other error")

	result, goErr := handleNavigationError(m, "test.go", err)
	if result != nil {
		t.Errorf("handleNavigationError returned non-nil result for non-unavailable error: %v", result)
	}
	if goErr == nil || !errors.Is(goErr, err) {
		t.Errorf("handleNavigationError should return original error, got %v", goErr)
	}
}

func TestNoServerErrorHover(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)

	input := map[string]any{
		"file":   "test.unknown",
		"line":   1.0,
		"column": 1.0,
	}

	result, err := defs[3].Handler(context.Background(), input)
	if err != nil {
		t.Errorf("hover handler returned error: %v", err)
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "No language server is configured") {
		t.Errorf("message does not contain expected unavailability text: %q", msg)
	}
	if !strings.Contains(msg, ".unknown") {
		t.Errorf("message does not contain file extension: %q", msg)
	}
}

func TestServerExitedErrorHover(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	result, goErr := handleNavigationError(m, "test.go", errServerExited)
	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}
	if goErr != nil {
		t.Fatalf("expected nil error, got %v", goErr)
	}

	if !strings.Contains(msg, "exited unexpectedly") {
		t.Errorf("message does not contain expected text: %q", msg)
	}
}

func TestFailedServerErrorHover(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"mock": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "true",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	err := errors.New("other error")

	result, goErr := handleNavigationError(m, "test.go", err)
	if result != nil {
		t.Errorf("handleNavigationError returned non-nil result for non-unavailable error: %v", result)
	}
	if goErr == nil || !errors.Is(goErr, err) {
		t.Errorf("handleNavigationError should return original error, got %v", goErr)
	}
}

func TestSymbolResolutionError(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, t.TempDir(), nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	defsTool := defs[0]

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Update manager workspace to match tmpdir
	m.workspace = tmpdir

	input := map[string]any{
		"file":   "test.go",
		"symbol": "NonExistentSymbol",
	}

	result, err := defsTool.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler with symbol: expected nil Go error, got %v", err)
		return
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "not found") {
		t.Errorf("expected 'not found' in result, got: %q", msg)
	}
}

// symbolTestManager builds a Manager wired to a fake LSP session for symbol
// resolution tests. It registers a "go" server for .go files and injects a
// ready entry keyed by the session's actual (server, root) resolution, so
// requests reach entryFor without spawning a real process.
func symbolTestManager(t *testing.T, sess session, workspace, testFile string) *Manager {
	t.Helper()

	cfg := config.LSPConfig{
		MaxResults:     100,
		RequestTimeout: config.MustDuration("2s"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}
	m := NewManager(cfg, workspace, nil, func(string) {}, nil)
	t.Cleanup(func() { _ = m.Close() })

	root := resolveRoot(testFile, workspace, nil)
	ent := &entry{state: ServerState{Status: ServerStatusReady}, session: sess}
	m.sessions = map[sessionKey]*entry{{server: "go", root: root}: ent}

	return m
}

func TestSymbolResolutionSuccess(t *testing.T) {
	fs := newFakeServer()
	fs.definitionResult = &protocol.Location{
		URI: "file:///test.go",
		Range: protocol.Range{
			Start: protocol.Position{Line: 1, Character: 5},
			End:   protocol.Position{Line: 1, Character: 8},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := symbolTestManager(t, sess, tmpdir, testFile)

	defs := ToolDefs(m)
	defsTool := defs[0]

	result, err := defsTool.Handler(ctx, map[string]any{
		"file":   "test.go",
		"symbol": "Foo",
	})
	if err != nil {
		t.Fatalf("definitions handler with symbol: expected nil Go error, got %v", err)
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	wantEcho := "resolved test.go:2:6 (Foo)"
	if !strings.HasPrefix(msg, wantEcho+"\n") {
		t.Fatalf("expected output to start with %q, got: %q", wantEcho+"\n", msg)
	}

	locationOutput := strings.TrimPrefix(msg, wantEcho+"\n")
	if locationOutput == "" || locationOutput == "(no definitions found)" {
		t.Errorf("expected formatted location output after echo line, got: %q", locationOutput)
	}
}

func TestSymbolResolutionEmptyResult(t *testing.T) {
	fs := newFakeServer()
	// fs.definitionResult left unset: the server returns zero locations.

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := symbolTestManager(t, sess, tmpdir, testFile)

	defs := ToolDefs(m)
	defsTool := defs[0]

	result, err := defsTool.Handler(ctx, map[string]any{
		"file":   "test.go",
		"symbol": "Foo",
	})
	if err != nil {
		t.Fatalf("definitions handler with symbol: expected nil Go error, got %v", err)
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	// The echo line must not suppress the "(no definitions found)" fallback
	// just because it makes the combined output non-empty.
	wantEcho := "resolved test.go:2:6 (Foo)"
	want := wantEcho + "\n(no definitions found)"
	if msg != want {
		t.Errorf("output = %q, want %q", msg, want)
	}
}

func TestSymbolResolutionCacheSharesWithColumnPath(t *testing.T) {
	fs := newFakeServer()
	fs.definitionResult = &protocol.Location{
		URI: "file:///test.go",
		Range: protocol.Range{
			Start: protocol.Position{Line: 1, Character: 5},
			End:   protocol.Position{Line: 1, Character: 8},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\nfunc Foo() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := symbolTestManager(t, sess, tmpdir, testFile)

	// First call: explicit column.
	res, err := m.Definitions(ctx, testFile, 2, 6)
	if err != nil {
		t.Fatalf("Definitions (column path): %v", err)
	}
	if res.Incomplete {
		t.Fatalf("Definitions (column path): Incomplete = true, want false (caching only happens for non-provisional results)")
	}

	// Resolve the equivalent symbol position and confirm it yields the
	// identical (line, col) the explicit call used.
	_, resolvedLine, resolvedCol, err := resolveSymbolPosition(tmpdir, "test.go", "Foo", 0)
	if err != nil {
		t.Fatalf("resolveSymbolPosition: %v", err)
	}
	if resolvedLine != 2 || resolvedCol != 6 {
		t.Fatalf("resolveSymbolPosition: got (%d, %d), want (2, 6)", resolvedLine, resolvedCol)
	}

	// Second call: via the resolved symbol position. Should hit the cache.
	if _, err := m.Definitions(ctx, testFile, resolvedLine, resolvedCol); err != nil {
		t.Fatalf("Definitions (symbol-resolved path): %v", err)
	}

	calls := 0
	for _, method := range fs.recorded() {
		if method == "textDocument/definition" {
			calls++
		}
	}
	if calls != 1 {
		t.Errorf("textDocument/definition called %d times, want 1 (second call should hit the cache)", calls)
	}
}

func TestColumnWithoutLineError(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	defsTool := defs[0]

	input := map[string]any{
		"file":   "test.go",
		"column": 5.0,
	}

	result, err := defsTool.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler: expected nil Go error, got %v", err)
		return
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "column requires line") {
		t.Errorf("expected 'column requires line' in result, got: %q", msg)
	}
}

func TestNeitherColumnNorSymbolError(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	defsTool := defs[0]

	input := map[string]any{
		"file": "test.go",
	}

	result, err := defsTool.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler: expected nil Go error, got %v", err)
		return
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	if !strings.Contains(msg, "requires either column") && !strings.Contains(msg, "requires either") {
		t.Errorf("expected parameter error in result, got: %q", msg)
	}
}

func TestColumnPathNoEchoLine(t *testing.T) {
	cfg := config.LSPConfig{
		Servers: map[string]config.LSPServerConfig{
			"test": {
				Enabled:        true,
				FileExtensions: []string{".go"},
				Command:        "echo",
			},
		},
	}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	defsTool := defs[0]

	input := map[string]any{
		"file":   "test.unknown",
		"line":   1.0,
		"column": 1.0,
	}

	result, err := defsTool.Handler(context.Background(), input)
	if err != nil {
		t.Errorf("definitions handler: expected nil Go error, got %v", err)
		return
	}

	msg, ok := result.(string)
	if !ok {
		t.Fatalf("expected string result, got %T", result)
	}

	// With explicit column, should NOT have echo line (no "resolved" prefix)
	if strings.Contains(msg, "resolved") {
		t.Errorf("column path should not produce echo line, got: %q", msg)
	}
}

func TestColumnAndSymbolSupplied(t *testing.T) {
	cfg := config.LSPConfig{}
	m := NewManager(cfg, "/workspace", nil, func(string) {}, nil)
	defer func() { _ = m.Close() }()

	defs := ToolDefs(m)
	defsTool := defs[0]

	input := map[string]any{
		"file":   "test.go",
		"line":   1.0,
		"column": 1.0,
		"symbol": "ShouldBeIgnored",
	}

	result, err := defsTool.Handler(context.Background(), input)
	if err == nil {
		// With no server configured, should get "No language server is configured" message
		// This proves the column path was taken (no symbol resolution happened)
		msg, ok := result.(string)
		if ok && strings.Contains(msg, "No language server") {
			// Good: we took the column path, not symbol path
			return
		}
	}

	t.Errorf("column+symbol: column should win and skip symbol resolution")
}
