package builtin

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestLSTool(t *testing.T) {
	tmpDir := t.TempDir()

	entries := []string{"b.txt", "a.txt", "sub/c.txt", "sub/d.txt"}
	for _, e := range entries {
		path := filepath.Join(tmpDir, e)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(e), err)
		}
		if err := os.WriteFile(path, []byte(e), 0o644); err != nil {
			t.Fatalf("write %s: %v", e, err)
		}
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewLSTool(env)
	ctx := context.Background()

	t.Run("lists top-level files and directories", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path": ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if result.Returned != 3 {
			t.Errorf("Returned = %d, want 3", result.Returned)
		}
		if result.Output != "sub/\na.txt\nb.txt" {
			t.Errorf("Output = %q, want %q", result.Output, "sub/\na.txt\nb.txt")
		}
	})

	t.Run("recursive lists subdirectories", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":      ".",
			"recursive": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if result.Returned != 5 {
			t.Errorf("Returned = %d, want 5", result.Returned)
		}
		want := "a.txt\nb.txt\nsub/\nsub/c.txt\nsub/d.txt"
		if result.Output != want {
			t.Errorf("Output = %q, want %q", result.Output, want)
		}
	})

	t.Run("pagination with limit and offset", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   ".",
			"limit":  1,
			"offset": 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if result.Returned != 1 {
			t.Errorf("Returned = %d, want 1", result.Returned)
		}
		if result.Output != "a.txt" {
			t.Errorf("Output = %q, want %q", result.Output, "a.txt")
		}
		if result.NextOffset != 2 {
			t.Errorf("NextOffset = %d, want 2", result.NextOffset)
		}
	})

	t.Run("offset beyond results returns empty", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":   ".",
			"limit":  10,
			"offset": 100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*Result)
		if !ok {
			t.Fatalf("result type = %T, want *Result", resultI)
		}
		if result.Returned != 0 {
			t.Errorf("Returned = %d, want 0", result.Returned)
		}
	})

}
