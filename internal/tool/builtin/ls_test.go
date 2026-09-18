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
		if lineCount := countResultLines(result.Output); lineCount != 3 {
			t.Errorf("line count = %d, want 3", lineCount)
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
		if lineCount := countResultLines(result.Output); lineCount != 5 {
			t.Errorf("line count = %d, want 5", lineCount)
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
		if lineCount := countResultLines(result.Output); lineCount != 1 {
			t.Errorf("line count = %d, want 1", lineCount)
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
		if lineCount := countResultLines(result.Output); lineCount != 0 {
			t.Errorf("line count = %d, want 0", lineCount)
		}
	})

	t.Run("recursive respects excluder", func(t *testing.T) {
		excludedTmpDir := t.TempDir()
		secret := filepath.Join(excludedTmpDir, "secret")
		if err := os.Mkdir(secret, 0o755); err != nil {
			t.Fatalf("mkdir secret: %v", err)
		}
		if err := os.WriteFile(filepath.Join(secret, "key.txt"), []byte("secret"), 0o644); err != nil {
			t.Fatalf("write secret/key.txt: %v", err)
		}
		if err := os.WriteFile(filepath.Join(excludedTmpDir, "public.txt"), []byte("public"), 0o644); err != nil {
			t.Fatalf("write public.txt: %v", err)
		}

		excludedPolicy := tool.NewPathPolicy(excludedTmpDir, config.PathsConfig{})
		excludedExcluder := tool.NewPathExcluder([]string{"secret"}, nil)
		excludedEnv := Env{WorkDir: excludedTmpDir, PathPolicy: &excludedPolicy, Excluder: &excludedExcluder}
		excludedToolDef := NewLSTool(excludedEnv)

		resultI, err := excludedToolDef.Handler(ctx, map[string]any{
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
		if strings.Contains(result.Output, "secret") {
			t.Fatalf("Output contains excluded path: %q", result.Output)
		}
		if !strings.Contains(result.Output, "public.txt") {
			t.Fatalf("Output missing visible file public.txt: %q", result.Output)
		}
	})

}
