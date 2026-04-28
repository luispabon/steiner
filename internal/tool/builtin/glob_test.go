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
	excluder := tool.NewPathExcluder(nil, nil)
	env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
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

func TestGlobTool_Exclusions(t *testing.T) {
	tmpDir := t.TempDir()

	// Create files that should be visible.
	visible := []string{"main.go", "helper.go", "README.md"}
	for _, f := range visible {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	// Create files inside directories that should be excluded by built-in rules.
	excludedDirs := []string{".git", "node_modules", "vendor", ".steiner", ".cache", "dist", "build", "out", "target"}
	excludedFiles := []string{"config", "index.js", "lib.go", "secret.yaml", "cache.bin", "bundle.js", "output.o", "result", "binary"}
	for i, dir := range excludedDirs {
		dirPath := filepath.Join(tmpDir, dir)
		if err := os.Mkdir(dirPath, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dirPath, excludedFiles[i]), []byte(excludedFiles[i]), 0o644); err != nil {
			t.Fatalf("write %s/%s: %v", dir, excludedFiles[i], err)
		}
	}

	// Create a deeply nested file inside an excluded dir to verify no traversal.
	nestedExcluded := filepath.Join(tmpDir, "node_modules", "deep", ".cache", "nested.txt")
	if err := os.MkdirAll(filepath.Dir(nestedExcluded), 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(nestedExcluded, []byte("nested"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	excluder := tool.NewPathExcluder(nil, nil)
	env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
	toolDef := NewGlobTool(env)
	ctx := context.Background()

	t.Run("excluded directories not returned in results", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}

		for _, dir := range excludedDirs {
			if strings.Contains(result.Output, dir+"/") || result.Output == dir {
				t.Errorf("output contains excluded directory %q: %q", dir, result.Output)
			}
		}
	})

	t.Run("visible files are still found", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*.go",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Errorf("output missing visible file main.go: %q", result.Output)
		}
		if !strings.Contains(result.Output, "helper.go") {
			t.Errorf("output missing visible file helper.go: %q", result.Output)
		}
	})

	t.Run("deeply nested excluded files not returned", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if strings.Contains(result.Output, "nested.txt") {
			t.Errorf("output contains deeply nested excluded file nested.txt: %q", result.Output)
		}
	})
}

func TestGlobWalk_DoublestarPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	dirs := []string{"sub1", "sub2", "sub1/nested"}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(tmpDir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	files := []string{
		"root.go",
		"sub1/a.go",
		"sub1/b.txt",
		"sub2/c.go",
		"sub1/nested/d.go",
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, f), []byte(f), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	excluder := tool.NewPathExcluder(nil, nil)

	t.Run("**/*.go matches Go files in all subdirectories", func(t *testing.T) {
		matches, err := globWalk(tmpDir, "**/*.go", excluder, &policy)
		if err != nil {
			t.Fatalf("globWalk error: %v", err)
		}
		// root.go is at root, not under **/ — only subdir .go files match
		if len(matches) != 3 {
			t.Errorf("got %d matches, want 3: %v", len(matches), matches)
		}
	})

	t.Run("*.{go,txt} matches go and txt files at root only", func(t *testing.T) {
		matches, err := globWalk(tmpDir, "*.{go,txt}", excluder, &policy)
		if err != nil {
			t.Fatalf("globWalk error: %v", err)
		}
		// Only root.go matches — subdir files contain / in rel path
		if len(matches) != 1 {
			t.Errorf("got %d matches, want 1: %v", len(matches), matches)
		}
		if len(matches) > 0 && matches[0] != "root.go" {
			t.Errorf("match = %q, want root.go", matches[0])
		}
	})

	t.Run("early termination cap at maxGlobLimit", func(t *testing.T) {
		// Use a very broad pattern that matches everything.
		matches, err := globWalk(tmpDir, "**/*", excluder, &policy)
		if err != nil {
			t.Fatalf("globWalk error: %v", err)
		}
		if len(matches) > maxGlobLimit {
			t.Errorf("got %d matches, want <= %d", len(matches), maxGlobLimit)
		}
		if len(matches) == 0 {
			t.Error("expected at least some matches")
		}
	})
}

func TestGlobTool_CustomExcludePaths(t *testing.T) {
	tmpDir := t.TempDir()

	// Create visible files.
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Create a directory that should be excluded via exact path prefix.
	secretDir := filepath.Join(tmpDir, "secret")
	if err := os.Mkdir(secretDir, 0o755); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "key.txt"), []byte("key"), 0o644); err != nil {
		t.Fatalf("write secret/key.txt: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	excluder := tool.NewPathExcluder([]string{secretDir}, nil)
	env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
	toolDef := NewGlobTool(env)
	ctx := context.Background()

	t.Run("custom exact path exclusion", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if strings.Contains(result.Output, "secret/") || strings.Contains(result.Output, "key.txt") {
			t.Errorf("output contains excluded path: %q", result.Output)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Errorf("output missing visible file main.go: %q", result.Output)
		}
	})
}

func TestGlobTool_CustomExcludePatterns(t *testing.T) {
	tmpDir := t.TempDir()

	// Create visible files.
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("main"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	// Create files matching custom exclusion pattern.
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.Mkdir(backupDir, 0o755); err != nil {
		t.Fatalf("mkdir backups: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "data.bak"), []byte("bak"), 0o644); err != nil {
		t.Fatalf("write backups/data.bak: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	excluder := tool.NewPathExcluder(nil, []string{"*.bak"})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
	toolDef := NewGlobTool(env)
	ctx := context.Background()

	t.Run("custom glob pattern exclusion", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "*",
			"path":    ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(Result)
		if !ok {
			t.Fatalf("result type = %T, want Result", resultI)
		}
		if strings.Contains(result.Output, ".bak") {
			t.Errorf("output contains excluded pattern .bak: %q", result.Output)
		}
		if !strings.Contains(result.Output, "main.go") {
			t.Errorf("output missing visible file main.go: %q", result.Output)
		}
	})
}
