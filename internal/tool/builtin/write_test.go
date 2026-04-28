package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestWriteTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewWriteTool(env)
	ctx := context.Background()

	t.Run("creates new file", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":    "new_file.txt",
			"content": "hello world",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		if result.Path != "new_file.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "new_file.txt")
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "new_file.txt"))
		if err != nil {
			t.Fatalf("read created file: %v", err)
		}
		if string(data) != "hello world" {
			t.Errorf("file content = %q, want %q", string(data), "hello world")
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tmpDir, "existing.txt"), []byte("old content"), 0o644); err != nil {
			t.Fatalf("write existing file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":    "existing.txt",
			"content": "new content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		if result.Path != "existing.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "existing.txt")
		}
		data, err := os.ReadFile(filepath.Join(tmpDir, "existing.txt"))
		if err != nil {
			t.Fatalf("read overwritten file: %v", err)
		}
		if string(data) != "new content" {
			t.Errorf("file content = %q, want %q", string(data), "new content")
		}
	})
}
