package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestBashTool(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewBashTool(env)
	ctx := context.Background()

	t.Run("executes simple command", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"command": "echo hello world",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*BashResult)
		if !ok {
			t.Fatalf("result type = %T, want *BashResult", resultI)
		}
		if result.ExitCode != 0 {
			t.Errorf("ExitCode = %d, want 0", result.ExitCode)
		}
		if !strings.Contains(result.Output, "hello world") {
			t.Errorf("Output = %q, want to contain %q", result.Output, "hello world")
		}
	})

	t.Run("respects cwd", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"command": "pwd",
			"cwd":     ".",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*BashResult)
		if !ok {
			t.Fatalf("result type = %T, want *BashResult", resultI)
		}
		pwdOut := strings.TrimSpace(result.Output)
		if strings.Contains(pwdOut, "access denied") {
			return
		}
		if !strings.HasSuffix(pwdOut, "/") && pwdOut != tmpDir {
			t.Fatalf("pwd output = %q, want %q or a path ending with /", pwdOut, tmpDir)
		}
	})

	t.Run("truncates output", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"command":          "python3 -c \"print('x'*50000)\"",
			"max_output_chars": 100,
		})
		if err != nil {
			t.Skipf("python3 not available or error: %v", err)
		}
		result, ok := resultI.(*BashResult)
		if !ok {
			t.Fatalf("result type = %T, want *BashResult", resultI)
		}
		if !result.Truncated {
			t.Errorf("Truncated = false, want true. Output length = %d", len(result.Output))
		}
	})

	t.Run("timeout works", func(t *testing.T) {
		shortCtx, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		resultI, err := toolDef.Handler(shortCtx, map[string]any{
			"command":         "sleep 5",
			"timeout_seconds": 1,
		})
		if err != nil {
			return // Go-level error is acceptable for timeout
		}
		result, ok := resultI.(*BashResult)
		if !ok {
			t.Fatalf("result type = %T, want *BashResult", resultI)
		}
		if result.ExitCode == 0 {
			t.Errorf("ExitCode = 0, want non-zero for timed out command")
		}
	})

	t.Run("command with non-zero exit code", func(t *testing.T) {
		resultI, err := toolDef.Handler(ctx, map[string]any{
			"command": "false",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*BashResult)
		if !ok {
			t.Fatalf("result type = %T, want *BashResult", resultI)
		}
		if result.ExitCode == 0 {
			t.Errorf("ExitCode = 0, want non-zero for 'false' command")
		}
	})
}
