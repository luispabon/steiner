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
