package builtin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
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

func TestLSRecursive_ReturnsRootErrors(t *testing.T) {
	t.Run("returns missing root error", func(t *testing.T) {
		_, err := lsRecursive(context.Background(), filepath.Join(t.TempDir(), "missing"), defaultLSLimit, 0, tool.PathExcluder{})
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("lsRecursive error = %v, want not exist error", err)
		}
	})

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission-based traversal failure test is unix-oriented")
	}

	t.Run("returns inaccessible root error", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o000); err != nil {
			t.Fatalf("chmod root: %v", err)
		}
		defer func() { _ = os.Chmod(root, 0o755) }()
		if _, err := os.ReadDir(root); err == nil {
			t.Skip("filesystem does not deny directory traversal after chmod 000")
		}

		_, err := lsRecursive(context.Background(), root, defaultLSLimit, 0, tool.PathExcluder{})
		if !errors.Is(err, fs.ErrPermission) {
			t.Fatalf("lsRecursive error = %v, want permission error", err)
		}
	})
}

func TestLSRecursive_SkipsInaccessibleDirectories(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission-based traversal failure test is unix-oriented")
	}

	tmpDir := t.TempDir()
	visiblePath := filepath.Join(tmpDir, "visible.txt")
	if err := os.WriteFile(visiblePath, []byte("visible"), 0o644); err != nil {
		t.Fatalf("write visible file: %v", err)
	}
	blockedDir := filepath.Join(tmpDir, "blocked")
	if err := os.Mkdir(blockedDir, 0o755); err != nil {
		t.Fatalf("mkdir blocked dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(blockedDir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write blocked child file: %v", err)
	}
	if err := os.Chmod(blockedDir, 0o000); err != nil {
		t.Fatalf("chmod blocked dir: %v", err)
	}
	defer func() { _ = os.Chmod(blockedDir, 0o755) }()
	if _, err := os.ReadDir(blockedDir); err == nil {
		t.Skip("filesystem does not deny directory traversal after chmod 000")
	}

	result, err := lsRecursive(context.Background(), tmpDir, defaultLSLimit, 0, tool.PathExcluder{})
	if err != nil {
		t.Fatalf("lsRecursive: %v", err)
	}
	if !strings.Contains(result.Output, "visible.txt") {
		t.Fatalf("Output missing visible file: %q", result.Output)
	}
	if strings.Contains(result.Output, "blocked/secret.txt") {
		t.Fatalf("Output contains inaccessible directory child: %q", result.Output)
	}
}
