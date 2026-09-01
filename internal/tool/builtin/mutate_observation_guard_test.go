package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// TestMutateReplaceObservationGuard exercises the requirement that a replace
// operation against an existing file must be backed by either an observed
// read this session or an explicit file_hash: an unguarded blind edit is
// rejected before planning, with a diagnostic naming what's missing.
func TestMutateReplaceObservationGuard(t *testing.T) {
	newEnv := func(root string, observed tool.FileObservedChecker) Env {
		policy := tool.NewPathPolicy(root, config.PathsConfig{})
		return Env{WorkDir: root, PathPolicy: &policy, FileObserved: observed}
	}

	t.Run("unobserved with no file_hash is rejected", func(t *testing.T) {
		root := t.TempDir()
		content := "one\ntwo\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		toolDef := NewMutateTool(newEnv(root, nil))

		result, err := toolDef.Handler(context.Background(), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
			},
		})
		if err != nil {
			t.Fatalf("Handler() error = %v", err)
		}
		got, ok := result.(*MutateResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutateResult", result)
		}
		if got.OperationsFailed != 1 || got.OperationsApplied != 0 {
			t.Fatalf("mutate result = %#v, want the operation rejected", got)
		}
		got2, err := os.ReadFile(filepath.Join(root, "note.txt"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if string(got2) != content {
			t.Fatalf("file content changed to %q, want unchanged %q", got2, content)
		}
	})

	t.Run("unobserved with file_hash succeeds", func(t *testing.T) {
		root := t.TempDir()
		content := "one\ntwo\n"
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		toolDef := NewMutateTool(newEnv(root, nil))

		result, err := toolDef.Handler(context.Background(), map[string]any{
			"operations": []any{
				map[string]any{
					"type":       "replace",
					"path":       "note.txt",
					"old_string": "one",
					"new_string": "ONE",
					"file_hash":  FileContentHash([]byte(content)),
				},
			},
		})
		if err != nil {
			t.Fatalf("Handler() error = %v", err)
		}
		got, ok := result.(*MutateResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutateResult", result)
		}
		if got.OperationsFailed != 0 || got.OperationsApplied != 1 {
			t.Fatalf("mutate result = %#v, want the operation applied", got)
		}
		assertFile(t, filepath.Join(root, "note.txt"), "ONE\ntwo\n")
	})

	t.Run("observed read succeeds without file_hash", func(t *testing.T) {
		root := t.TempDir()
		content := "one\ntwo\n"
		absPath := filepath.Join(root, "note.txt")
		if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		observed := func(path string) bool { return path == absPath }
		toolDef := NewMutateTool(newEnv(root, observed))

		result, err := toolDef.Handler(context.Background(), map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
			},
		})
		if err != nil {
			t.Fatalf("Handler() error = %v", err)
		}
		got, ok := result.(*MutateResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutateResult", result)
		}
		if got.OperationsFailed != 0 || got.OperationsApplied != 1 {
			t.Fatalf("mutate result = %#v, want the operation applied", got)
		}
		assertFile(t, absPath, "ONE\ntwo\n")
	})

	t.Run("create, write, move, delete_file on an unobserved path are unaffected", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("existing\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		toolDef := NewMutateTool(newEnv(root, nil))

		got := runMutate(t, toolDef, map[string]any{
			"operations": []any{
				map[string]any{"type": "create", "path": "created.txt", "content": "created\n"},
				map[string]any{"type": "write", "path": "written.txt", "content": "written\n"},
				map[string]any{"type": "delete_file", "path": "existing.txt"},
				map[string]any{"type": "move", "from": "written.txt", "to": "moved.txt"},
			},
		})
		if got.OperationsFailed != 0 || got.OperationsApplied != 4 {
			t.Fatalf("mutate result = %#v, want all four operations applied", got)
		}
	})

	t.Run("rejection message names the missing precondition", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		toolDef := NewMutateTool(newEnv(root, nil))

		got := runMutate(t, toolDef, map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "note.txt", "old_string": "one", "new_string": "ONE"},
			},
		})
		wantSubstrings := []string{
			"not read this session",
			"no file_hash supplied",
			"read the file first",
			"file_hash from a read/grep result",
		}
		for _, want := range wantSubstrings {
			if !strings.Contains(got.Output, want) {
				t.Errorf("Output = %q, want it to contain %q", got.Output, want)
			}
		}
	})
}
