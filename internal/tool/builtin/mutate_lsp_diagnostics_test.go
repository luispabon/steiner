package builtin

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func newMutateDiagnosticsTestTool(root string, fn tool.MutateDiagnosticsFunc) tool.ToolDef {
	policy := tool.NewPathPolicy(root, config.PathsConfig{})
	return NewMutateTool(Env{
		WorkDir:           root,
		PathPolicy:        &policy,
		FileObserved:      func(string) bool { return true },
		MutateDiagnostics: fn,
	})
}

func TestMutateLSPDiagnosticsAppendedOnSuccess(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateDiagnosticsTestTool(root, func(_ context.Context, _ []string) string {
		return "Diagnostics after mutation:\nfoo.go:1:1 error: bad"
	})

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if !strings.Contains(got.Output, "Diagnostics after mutation:\nfoo.go:1:1 error: bad") {
		t.Fatalf("Output = %q, want diagnostics section", got.Output)
	}
}

func TestMutateLSPDiagnosticsSkippedOnFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	called := false
	toolDef := newMutateDiagnosticsTestTool(root, func(_ context.Context, _ []string) string {
		called = true
		return "should not appear"
	})

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "note.txt", "old_string": "does-not-match", "new_string": "x"},
		},
	})
	if got.OperationsFailed == 0 {
		t.Fatalf("OperationsFailed = %d, want > 0", got.OperationsFailed)
	}
	if called {
		t.Fatal("MutateDiagnostics was called on a failed mutate batch")
	}
}

func TestMutateLSPDiagnosticsNilSkipsEntirely(t *testing.T) {
	root := t.TempDir()
	toolDef := newMutateDiagnosticsTestTool(root, nil)

	got := runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
		},
	})
	if got.OperationsFailed != 0 {
		t.Fatalf("OperationsFailed = %d, want 0", got.OperationsFailed)
	}
	if got.Output != "" {
		t.Fatalf("Output = %q, want empty string when MutateDiagnostics is nil", got.Output)
	}
}

func TestMutateLSPDiagnosticsExcludesDeletedIncludesCreated(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "gone.txt"), []byte("bye\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var captured []string
	toolDef := newMutateDiagnosticsTestTool(root, func(_ context.Context, files []string) string {
		captured = files
		return ""
	})

	runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
			map[string]any{"type": "delete_file", "path": "gone.txt"},
		},
	})
	if !slices.Contains(captured, "created.txt") {
		t.Fatalf("captured files = %v, want to include created.txt", captured)
	}
	if slices.Contains(captured, "gone.txt") {
		t.Fatalf("captured files = %v, want to exclude gone.txt", captured)
	}
}

func TestMutateLSPDiagnosticsMoveIncludesToExcludesFrom(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old.txt"), []byte("move me\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	var captured []string
	toolDef := newMutateDiagnosticsTestTool(root, func(_ context.Context, files []string) string {
		captured = files
		return ""
	})

	runMutate(t, toolDef, map[string]any{
		"operations": []any{
			map[string]any{"type": "move", "from": "old.txt", "to": "new.txt"},
		},
	})
	if !slices.Contains(captured, "new.txt") {
		t.Fatalf("captured files = %v, want to include new.txt", captured)
	}
	if slices.Contains(captured, "old.txt") {
		t.Fatalf("captured files = %v, want to exclude old.txt", captured)
	}
}
