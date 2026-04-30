package builtin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestGrepTool(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"hello.txt": "hello world\nthis is a test\nhello again\n",
		"other.txt": "goodbye world\nnothing here\n",
		"foo.go":    "package foo\nfunc Hello() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewGrepTool(env)
	ctx := context.Background()

	t.Run("files_with_matches works", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "files_with_matches",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("Matches = %d, want > 0", result.Matches)
		}
		if result.Output == "" {
			t.Error("Output is empty, expected file names")
		}
	})

	t.Run("content mode with context works", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "content",
			"context":     1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("Matches = %d, want > 0", result.Matches)
		}
		if result.Output == "" {
			t.Error("Output is empty, expected content")
		}
	})

	t.Run("count mode works", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "count",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("Matches = %d, want > 0", result.Matches)
		}
	})

	t.Run("no matches returns empty result", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "zzz_nonexistent_zzz",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
	})

	t.Run("output_mode defaults to content", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "hello",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("Matches = %d, want > 0", result.Matches)
		}
	})

	t.Run("invalid output_mode returns error", func(t *testing.T) {
		_, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "invalid",
		})
		if err == nil {
			t.Fatal("expected error for invalid output_mode")
		}
	})

	t.Run("rejects path outside workspace", func(t *testing.T) {
		_, err := toolDef.Handler(ctx, map[string]any{
			"pattern": "hello",
			"path":    "/etc",
		})
		if err == nil {
			t.Fatal("expected error for path outside workspace")
		}
	})

	t.Run("grep with nil excluder works", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("Matches = %d, want > 0", result.Matches)
		}
	})

	t.Run("grep output contains match lines", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if !strings.Contains(result.Output, "hello") {
			t.Errorf("Output = %q, want to contain %q", result.Output, "hello")
		}
	})
}

func TestGrepTool_Exclusions(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"src/main.go":              "package main\nfunc main() {}\n",
		"src/helper.go":            "package main\nfunc helper() {}\n",
		"secret/keys.txt":          "password = secret123\n",
		"secret/tokens.txt":        "token = abcdef\n",
		".git/config":              "hello = world\n",
		"node_modules/pkg/main.js": "console.log('hello')\n",
	}
	for name, content := range files {
		p := filepath.Join(tmpDir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	ctx := context.Background()

	t.Run("excluded file paths are not searched", func(t *testing.T) {
		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder([]string{"secret"}, nil)
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "secret",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if strings.Contains(result.Output, "secret/") {
			t.Errorf("output contains excluded path, got: %s", result.Output)
		}
	})

	t.Run("excluded directories are not traversed", func(t *testing.T) {
		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, []string{".git"})
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "world",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if strings.Contains(result.Output, ".git") {
			t.Errorf("output contains excluded path, got: %s", result.Output)
		}
	})

	t.Run("non-excluded files are still searchable", func(t *testing.T) {
		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder([]string{"secret"}, nil)
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "func",
			"output_mode": "files_with_matches",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches <= 0 {
			t.Errorf("expected matches in non-excluded files, got %d", result.Matches)
		}
		if !strings.Contains(result.Output, "src/main.go") || !strings.Contains(result.Output, "src/helper.go") {
			t.Errorf("output missing expected files, got: %s", result.Output)
		}
	})

	t.Run("builtin exclusions are active", func(t *testing.T) {
		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, nil)
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "hello",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if strings.Contains(result.Output, ".git") {
			t.Errorf("output contains builtin-excluded .git path, got: %s", result.Output)
		}
		if strings.Contains(result.Output, "node_modules") {
			t.Errorf("output contains builtin-excluded node_modules path, got: %s", result.Output)
		}
	})
}

func TestGrepSearch_MultilineMatchesAcrossLines(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "multiline.txt")
	content := "alpha\nbeta\ngamma\n"
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		t.Fatalf("write multiline file: %v", err)
	}

	matches, err := grepSearch(context.Background(), tmpDir, "alpha\\nbeta", false, true, "", "", nil, 10)
	if err != nil {
		t.Fatalf("grepSearch returned error: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if matches[0].file != "multiline.txt" || matches[0].lineNumber != 1 || matches[0].line != "alpha" {
		t.Fatalf("first match = %#v, want multiline.txt line 1 alpha", matches[0])
	}
	if matches[1].file != "multiline.txt" || matches[1].lineNumber != 2 || matches[1].line != "beta" {
		t.Fatalf("second match = %#v, want multiline.txt line 2 beta", matches[1])
	}
}

func TestGrepSearch_ReturnsTraversalAndReadErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission-based traversal/read failure test is unix-oriented")
	}

	t.Run("returns read errors", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "blocked.txt")
		if err := os.WriteFile(filePath, []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write blocked file: %v", err)
		}
		if err := os.Chmod(filePath, 0o000); err != nil {
			t.Fatalf("chmod blocked file: %v", err)
		}
		defer func() {
			_ = os.Chmod(filePath, 0o644)
		}()

		if _, err := os.ReadFile(filePath); err == nil {
			t.Skip("filesystem does not deny file reads after chmod 000")
		}

		_, err := grepSearch(context.Background(), tmpDir, "needle", false, false, "", "", nil, 10)
		if err == nil {
			t.Fatal("expected read error, got nil")
		}
		if !strings.Contains(err.Error(), "read blocked.txt") {
			t.Fatalf("error = %v, want read blocked.txt context", err)
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("error = %v, want permission error", err)
		}
	})

	t.Run("returns traversal errors", func(t *testing.T) {
		tmpDir := t.TempDir()
		blockedDir := filepath.Join(tmpDir, "blocked")
		if err := os.Mkdir(blockedDir, 0o755); err != nil {
			t.Fatalf("mkdir blocked dir: %v", err)
		}
		secretPath := filepath.Join(blockedDir, "secret.txt")
		if err := os.WriteFile(secretPath, []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write blocked child file: %v", err)
		}
		if err := os.Chmod(blockedDir, 0o000); err != nil {
			t.Fatalf("chmod blocked dir: %v", err)
		}
		defer func() {
			_ = os.Chmod(blockedDir, 0o755)
		}()

		if _, err := os.ReadDir(blockedDir); err == nil {
			t.Skip("filesystem does not deny directory traversal after chmod 000")
		}

		_, err := grepSearch(context.Background(), tmpDir, "needle", false, false, "", "", nil, 10)
		if err == nil {
			t.Fatal("expected traversal error, got nil")
		}
		if !strings.Contains(err.Error(), "walk:") {
			t.Fatalf("error = %v, want walk wrapper", err)
		}
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("error = %v, want permission error", err)
		}
	})
}
