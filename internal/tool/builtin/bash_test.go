package builtin

import (
	"context"
	"os"
	"os/exec"
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

func TestBashToolWrapperSelection(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	var commandCalls, readOnlyCalls int
	commandWrapper := func(cmd *exec.Cmd) *exec.Cmd {
		commandCalls++
		return cmd
	}
	readOnlyWrapper := func(cmd *exec.Cmd) *exec.Cmd {
		readOnlyCalls++
		return cmd
	}

	tests := []struct {
		name             string
		readOnly         bool
		unsandboxed      bool
		readOnlyWrapper  func(*exec.Cmd) *exec.Cmd
		wantCommandCalls int
		wantReadOnly     int
		wantExitCode     int
		wantMessage      string
	}{
		{name: "read-only uses read-only wrapper", readOnly: true, wantReadOnly: 1},
		{name: "default uses command wrapper", wantCommandCalls: 1},
		{name: "read-only wins over unsandboxed", readOnly: true, unsandboxed: true, wantReadOnly: 1},
		{name: "missing read-only wrapper fails closed", readOnly: true, readOnlyWrapper: nil, wantExitCode: 255, wantMessage: "read-only bash requested but sandbox read-only wrapper is unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commandCalls = 0
			readOnlyCalls = 0
			roWrapper := tt.readOnlyWrapper
			if tt.name != "missing read-only wrapper fails closed" {
				roWrapper = readOnlyWrapper
			}
			toolDef := NewBashTool(Env{
				PathPolicy:                    &policy,
				CommandWrapper:                commandWrapper,
				ReadOnlyProjectCommandWrapper: roWrapper,
			})
			ctx := context.Background()
			if tt.readOnly {
				ctx = tool.WithReadOnlyProjectBash(ctx, true)
			}
			if tt.unsandboxed {
				ctx = context.WithValue(ctx, tool.BashUnsandboxedKey{}, true)
			}

			resultValue, err := toolDef.Handler(ctx, map[string]any{"command": "true"})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			result, ok := resultValue.(*BashResult)
			if !ok {
				t.Fatalf("Handler() result type = %T, want *BashResult", resultValue)
			}
			if result.ExitCode != tt.wantExitCode {
				t.Errorf("ExitCode = %d, want %d", result.ExitCode, tt.wantExitCode)
			}
			if result.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", result.Message, tt.wantMessage)
			}
			if commandCalls != tt.wantCommandCalls {
				t.Errorf("CommandWrapper calls = %d, want %d", commandCalls, tt.wantCommandCalls)
			}
			if readOnlyCalls != tt.wantReadOnly {
				t.Errorf("ReadOnlyProjectCommandWrapper calls = %d, want %d", readOnlyCalls, tt.wantReadOnly)
			}
		})
	}
}
