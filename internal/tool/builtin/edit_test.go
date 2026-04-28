package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestEditTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewEditTool(env)
	ctx := context.Background()

	t.Run("replaces one exact match", func(t *testing.T) {
		content := "hello world\nfoo bar\nbaz qux\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":       "test.txt",
			"old_string": "foo bar",
			"new_string": "replaced",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "test.txt"))
		if err != nil {
			t.Fatalf("read edited file: %v", err)
		}
		if string(data) != "hello world\nreplaced\nbaz qux\n" {
			t.Errorf("file content = %q, want %q", string(data), "hello world\nreplaced\nbaz qux\n")
		}
	})

	t.Run("fails on missing old_string", func(t *testing.T) {
		content := "hello world\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "missing.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":       "missing.txt",
			"old_string": "nonexistent text",
			"new_string": "replaced",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v (may indicate Go-level error from dive)", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		if result.Output == "" {
			t.Error("expected error message in Output for missing old_string")
		}
	})

	t.Run("fails on ambiguous old_string when replace_all is false", func(t *testing.T) {
		content := "hello\nhello\nworld\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "ambiguous.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":       "ambiguous.txt",
			"old_string": "hello",
			"new_string": "hi",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v (may indicate Go-level error from dive)", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		if result.Output == "" {
			t.Error("expected error message in Output for ambiguous old_string")
		}
	})

	t.Run("succeeds on ambiguous old_string when replace_all is true", func(t *testing.T) {
		content := "hello\nhello\nworld\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "replace_all.txt"), []byte(content), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        "replace_all.txt",
			"old_string":  "hello",
			"new_string":  "hi",
			"replace_all": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "replace_all.txt"))
		if err != nil {
			t.Fatalf("read edited file: %v", err)
		}
		if string(data) != "hi\nhi\nworld\n" {
			t.Errorf("file content = %q, want %q", string(data), "hi\nhi\nworld\n")
		}
	})
}
