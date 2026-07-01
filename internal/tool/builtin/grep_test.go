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

	t.Run("includes file_hashes for matching files", func(t *testing.T) {
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
		if result.FileHashes == nil {
			t.Fatal("FileHashes is nil, want non-nil map")
		}
		// "hello" matches hello.txt and foo.go (foo.go contains "Hello")
		// but case-sensitive by default, so only hello.txt matches
		hash, ok := result.FileHashes["hello.txt"]
		if !ok {
			t.Fatal("FileHashes missing hello.txt entry")
		}
		expected := fileContentHash([]byte(files["hello.txt"]))
		if hash != expected {
			t.Errorf("FileHashes[hello.txt] = %q, want %q", hash, expected)
		}
	})

	t.Run("file_hashes omitted when no matches", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "zzz_nonexistent_zzz",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.FileHashes != nil {
			t.Errorf("FileHashes = %v, want nil when no matches", result.FileHashes)
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

	t.Run("explicit excluded root is not searched", func(t *testing.T) {
		steinerLog := filepath.Join(tmpDir, ".steiner", "steiner.log")
		if err := os.MkdirAll(filepath.Dir(steinerLog), 0o755); err != nil {
			t.Fatalf("mkdir .steiner: %v", err)
		}
		if err := os.WriteFile(steinerLog, []byte("EventTypeDelegationExtension\n"), 0o644); err != nil {
			t.Fatalf("write .steiner log: %v", err)
		}

		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, nil)
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        ".steiner",
			"pattern":     "EventTypeDelegationExtension",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches != 0 {
			t.Fatalf("Matches = %d, want 0 for excluded root", result.Matches)
		}
		if strings.Contains(result.Output, "EventTypeDelegationExtension") {
			t.Fatalf("output searched excluded root: %s", result.Output)
		}
	})

	t.Run("worktree root under .steiner is searchable", func(t *testing.T) {
		worktreeRoot := filepath.Join(tmpDir, ".steiner", "worktrees", "abc123")
		srcDir := filepath.Join(worktreeRoot, "src")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatalf("mkdir worktree src: %v", err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatalf("write worktree file: %v", err)
		}

		policy := tool.NewPathPolicy(worktreeRoot, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, nil)
		env := Env{WorkDir: worktreeRoot, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        ".",
			"pattern":     "func main",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches == 0 {
			t.Fatal("Matches = 0, want > 0 — worktree source files should be searchable")
		}
	})

	t.Run("nested .steiner dir inside worktree is excluded", func(t *testing.T) {
		worktreeRoot := filepath.Join(tmpDir, ".steiner", "worktrees", "def456")
		nestedSteiner := filepath.Join(worktreeRoot, ".steiner")
		if err := os.MkdirAll(nestedSteiner, 0o755); err != nil {
			t.Fatalf("mkdir nested .steiner: %v", err)
		}
		if err := os.WriteFile(filepath.Join(nestedSteiner, "log.txt"), []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write nested .steiner file: %v", err)
		}
		srcDir := filepath.Join(worktreeRoot, "src")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			t.Fatalf("mkdir worktree src: %v", err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write worktree file: %v", err)
		}

		policy := tool.NewPathPolicy(worktreeRoot, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, nil)
		env := Env{WorkDir: worktreeRoot, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        ".",
			"pattern":     "needle",
			"output_mode": "files_with_matches",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if strings.Contains(result.Output, ".steiner") {
			t.Errorf("output contains nested .steiner content, got: %s", result.Output)
		}
		if !strings.Contains(result.Output, "src/main.go") {
			t.Errorf("output missing expected src/main.go, got: %s", result.Output)
		}
	})

	t.Run("path .steiner is still excluded", func(t *testing.T) {
		steinerLog := filepath.Join(tmpDir, ".steiner", "steiner.log")
		if err := os.MkdirAll(filepath.Dir(steinerLog), 0o755); err != nil {
			t.Fatalf("mkdir .steiner: %v", err)
		}
		if err := os.WriteFile(steinerLog, []byte("EventTypeDelegationExtension\n"), 0o644); err != nil {
			t.Fatalf("write .steiner log: %v", err)
		}

		policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
		excluder := tool.NewPathExcluder(nil, nil)
		env := Env{WorkDir: tmpDir, PathPolicy: &policy, Excluder: &excluder}
		toolDef := NewGrepTool(env)

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        ".steiner",
			"pattern":     "EventTypeDelegationExtension",
			"output_mode": "content",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Matches != 0 {
			t.Fatalf("Matches = %d, want 0 for excluded root", result.Matches)
		}
		if strings.Contains(result.Output, "EventTypeDelegationExtension") {
			t.Fatalf("output searched excluded root: %s", result.Output)
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

	matches, err := grepSearch(context.Background(), tmpDir, "multiline.txt", "alpha\\nbeta", false, true, "", "", nil, nil)
	if err != nil {
		t.Fatalf("grepSearch returned error: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("len(matches) = %d, want 1", len(matches))
	}
	if matches[0].file != "multiline.txt" {
		t.Fatalf("file = %q, want multiline.txt", matches[0].file)
	}
	if len(matches[0].matches) != 2 {
		t.Fatalf("len(matches[0].matches) = %d, want 2", len(matches[0].matches))
	}
	if matches[0].matches[0].file != "multiline.txt" || matches[0].matches[0].lineNumber != 1 || matches[0].matches[0].line != "alpha" {
		t.Fatalf("first match = %#v, want multiline.txt line 1 alpha", matches[0].matches[0])
	}
	if matches[0].matches[1].file != "multiline.txt" || matches[0].matches[1].lineNumber != 2 || matches[0].matches[1].line != "beta" {
		t.Fatalf("second match = %#v, want multiline.txt line 2 beta", matches[0].matches[1])
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

		_, err := grepSearch(context.Background(), tmpDir, tmpDir, "needle", false, false, "", "", nil, nil)
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

		_, err := grepSearch(context.Background(), tmpDir, tmpDir, "needle", false, false, "", "", nil, nil)
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

func TestGrepTool_PaginationAndMetadata(t *testing.T) {
	tmpDir := t.TempDir()

	files := map[string]string{
		"alpha.txt":   "line 1\nmatch alpha\nline 3\nmatch beta\nline 5\nmatch gamma\nline 7\n",
		"bravo.txt":   "match bravo\nline 2\n",
		"charlie.txt": "line 1\nmatch charlie\n",
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

	t.Run("content pagination honors offset and context", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":             "alpha.txt",
			"pattern":          "match",
			"output_mode":      "content",
			"context":          1,
			"head_limit":       1,
			"offset":           1,
			"line_numbers":     true,
			"case_insensitive": false,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if result.Returned != 1 {
			t.Fatalf("Returned = %d, want 1", result.Returned)
		}
		if result.Matches != 1 {
			t.Fatalf("Matches = %d, want 1", result.Matches)
		}
		if !result.HasMore {
			t.Fatal("HasMore = false, want true")
		}
		if !result.Truncated {
			t.Fatal("Truncated = false, want true")
		}
		if result.NextOffset != 2 {
			t.Fatalf("NextOffset = %d, want 2", result.NextOffset)
		}
		if !strings.Contains(result.Output, "3: line 3") || !strings.Contains(result.Output, "4: match beta") || !strings.Contains(result.Output, "5: line 5") {
			t.Fatalf("Output = %q, want merged context around the second match", result.Output)
		}
		if strings.Count(result.Output, "4: match beta") != 1 {
			t.Fatalf("Output = %q, want one rendered match line", result.Output)
		}
	})

	t.Run("content windows merge without repetition", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        "alpha.txt",
			"pattern":     "match",
			"output_mode": "content",
			"context":     1,
			"head_limit":  2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := resultI.(GrepResult)
		if strings.Count(result.Output, "4: match beta") != 1 {
			t.Fatalf("Output = %q, want merged windows with one beta line", result.Output)
		}
		if strings.Count(result.Output, "5: line 5") != 1 {
			t.Fatalf("Output = %q, want merged windows without duplicate context", result.Output)
		}
	})

	t.Run("files_with_matches paginates distinct files", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "match",
			"output_mode": "files_with_matches",
			"head_limit":  1,
			"offset":      1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := resultI.(GrepResult)
		if result.Returned != 1 {
			t.Fatalf("Returned = %d, want 1", result.Returned)
		}
		if result.Matches != 1 {
			t.Fatalf("Matches = %d, want 1", result.Matches)
		}
		if !result.HasMore {
			t.Fatal("HasMore = false, want true")
		}
		if !result.Truncated {
			t.Fatal("Truncated = false, want true")
		}
		if result.NextOffset != 2 {
			t.Fatalf("NextOffset = %d, want 2", result.NextOffset)
		}
		if strings.Contains(result.Output, "alpha.txt") {
			t.Fatalf("Output = %q, want offset to skip alpha.txt", result.Output)
		}
		if !strings.Contains(result.Output, "bravo.txt") {
			t.Fatalf("Output = %q, want bravo.txt", result.Output)
		}
	})

	t.Run("count mode paginates per file", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "match",
			"output_mode": "count",
			"head_limit":  1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := resultI.(GrepResult)
		if result.Returned != 1 {
			t.Fatalf("Returned = %d, want 1", result.Returned)
		}
		if result.Matches != 3 {
			t.Fatalf("Matches = %d, want 3 for alpha.txt", result.Matches)
		}
		if !result.HasMore {
			t.Fatal("HasMore = false, want true")
		}
		if !result.Truncated {
			t.Fatal("Truncated = false, want true")
		}
		if result.NextOffset != 1 {
			t.Fatalf("NextOffset = %d, want 1", result.NextOffset)
		}
		if !strings.Contains(result.Output, "alpha.txt:3") {
			t.Fatalf("Output = %q, want alpha count row", result.Output)
		}
	})

	t.Run("single file path search preserves the file name", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"path":        "bravo.txt",
			"pattern":     "match",
			"output_mode": "files_with_matches",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result := resultI.(GrepResult)
		if strings.TrimSpace(result.Output) != "bravo.txt" {
			t.Fatalf("Output = %q, want bravo.txt", result.Output)
		}
	})
}

func TestGrepTool_Truncation(t *testing.T) {
	tmpDir := t.TempDir()

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewGrepTool(env)
	ctx := context.Background()

	t.Run("huge matching line is truncated with marker in content mode", func(t *testing.T) {
		hugeMatch := "MATCH_" + strings.Repeat("x", 500) + "\n" +
			"normal line 1\n" +
			"normal line 2\n"
		if err := os.WriteFile(filepath.Join(tmpDir, "huge_match.txt"), []byte(hugeMatch), 0o644); err != nil {
			t.Fatalf("write huge match file: %v", err)
		}

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "MATCH_",
			"output_mode": "content",
			"context":     2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if !strings.Contains(result.Output, "…<truncated>") {
			t.Errorf("Output = %q, want to contain …<truncated> marker", result.Output)
		}
		if !strings.Contains(result.Output, "normal line 1") {
			t.Errorf("Output = %q, want to contain normal line 1", result.Output)
		}
		if !strings.Contains(result.Output, "normal line 2") {
			t.Errorf("Output = %q, want to contain normal line 2", result.Output)
		}
		found := false
		for _, r := range result.TruncationReasons {
			if r == "line_length_capped" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("TruncationReasons = %v, want to include line_length_capped", result.TruncationReasons)
		}
	})

	t.Run("pagination truncation fields still independent", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(tmpDir, "match1.txt"), []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write match1: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "match2.txt"), []byte("needle\n"), 0o644); err != nil {
			t.Fatalf("write match2: %v", err)
		}

		resultI, err := toolDef.Handler(ctx, map[string]any{
			"pattern":     "needle",
			"output_mode": "files_with_matches",
			"head_limit":  1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(GrepResult)
		if !ok {
			t.Fatalf("result type = %T, want GrepResult", resultI)
		}
		if !result.Truncated {
			t.Error("Truncated = false, want true (pagination)")
		}
		if !result.HasMore {
			t.Error("HasMore = false, want true")
		}
		foundPaged := false
		foundLine := false
		for _, r := range result.TruncationReasons {
			if r == "paged" {
				foundPaged = true
			}
			if r == "line_length_capped" {
				foundLine = true
			}
		}
		if !foundPaged {
			t.Errorf("TruncationReasons = %v, want to include paged", result.TruncationReasons)
		}
		if foundLine {
			t.Errorf("TruncationReasons = %v, should NOT include line_length_capped for pagination-only result", result.TruncationReasons)
		}
	})
}
