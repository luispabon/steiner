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

func TestGlobTool(t *testing.T) {
	tmpDir := t.TempDir()
	files := []string{"c.txt", "a.txt", "b.txt", "d.go", "e.go"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewGlobTool(env)
	ctx := context.Background()

	t.Run("returns results including txt files", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if result.Returned <= 0 {
			t.Errorf("Returned = %d, want > 0", result.Returned)
		}
		if !strings.Contains(result.Output, "a.txt") {
			t.Errorf("Output missing a.txt: %q", result.Output)
		}
		if !strings.Contains(result.Output, "b.txt") {
			t.Errorf("Output missing b.txt: %q", result.Output)
		}
		if !strings.Contains(result.Output, "c.txt") {
			t.Errorf("Output missing c.txt: %q", result.Output)
		}
	})

	t.Run("results are sorted alphabetically", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		lines := strings.Split(result.Output, "\n")
		var files []string
		for _, l := range lines {
			if strings.HasSuffix(l, ".txt") {
				files = append(files, l)
			}
		}
		if len(files) < 3 {
			t.Fatalf("found %d txt files, want at least 3", len(files))
		}
		for i := 1; i < len(files); i++ {
			if files[i] < files[i-1] {
				t.Errorf("files not sorted: %q before %q", files[i-1], files[i])
			}
		}
	})

	t.Run("limit and offset control pagination", func(t *testing.T) {
		allResult, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		all, _ := allResult.(Result)
		totalReturned := all.Returned

		pageResult, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
			"limit":   2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		page, _ := pageResult.(Result)
		if page.Returned > 2 {
			t.Errorf("limit=2 returned %d items", page.Returned)
		}
		if totalReturned > 2 && page.NextOffset == 0 {
			t.Errorf("expected NextOffset when more results exist")
		}
	})

	t.Run("offset works correctly", func(t *testing.T) {
		firstResult, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
			"limit":   2,
			"offset":  0,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		first, _ := firstResult.(Result)

		secondResult, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
			"limit":   2,
			"offset":  1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		second, _ := secondResult.(Result)

		if first.Output == second.Output {
			t.Error("different offsets produced same output")
		}
	})

	t.Run("offset beyond results returns empty page", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.txt",
			"path":    ".",
			"limit":   10,
			"offset":  100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if result.Returned != 0 {
			t.Errorf("Returned = %d, want 0", result.Returned)
		}
	})
}
