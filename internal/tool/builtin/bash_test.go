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

// withUnsandboxedWrapper injects an explicitly-unsandboxed ResolvedSandbox into
// ctx, matching what the execution pipeline sets for every call.
func withUnsandboxedWrapper(ctx context.Context) context.Context {
	return context.WithValue(ctx, tool.SandboxWrapperKey{}, tool.ResolvedSandbox{Wrapper: tool.Unsandboxed{}})
}

// recordingWrapper satisfies tool.SandboxWrapper for testing, recording the
// number of calls and the last readOnlyProject value it was wrapped with.
type recordingWrapper struct {
	calls               int
	lastReadOnlyProject bool
}

func (w *recordingWrapper) Enabled() bool { return true }

func (w *recordingWrapper) WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) *exec.Cmd {
	w.calls++
	w.lastReadOnlyProject = readOnlyProject
	return cmd
}

func TestBashTool(t *testing.T) {
	tmpDir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewBashTool(env)
	ctx := withUnsandboxedWrapper(context.Background())

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

// TestBashToolUsesResolvedSandboxWrapper proves bash applies exactly the
// ResolvedSandbox decision found in context: it calls WrapCommandMode with the
// baked-in readOnlyProject value, and never needs a readOnlyProject bool of its
// own to pass alongside it.
func TestBashToolUsesResolvedSandboxWrapper(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})

	tests := []struct {
		name            string
		readOnlyProject bool
	}{
		{name: "read-only project", readOnlyProject: true},
		{name: "default (writable) project", readOnlyProject: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolDef := NewBashTool(Env{PathPolicy: &policy})
			wrapper := &recordingWrapper{}
			ctx := context.WithValue(context.Background(), tool.SandboxWrapperKey{}, tool.ResolvedSandbox{
				Wrapper:         wrapper,
				ReadOnlyProject: tt.readOnlyProject,
			})

			resultValue, err := toolDef.Handler(ctx, map[string]any{"command": "true"})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			result, ok := resultValue.(*BashResult)
			if !ok {
				t.Fatalf("Handler() result type = %T, want *BashResult", resultValue)
			}
			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0", result.ExitCode)
			}
			if wrapper.calls != 1 {
				t.Fatalf("wrapper calls = %d, want 1", wrapper.calls)
			}
			if wrapper.lastReadOnlyProject != tt.readOnlyProject {
				t.Errorf("readOnlyProject = %v, want %v", wrapper.lastReadOnlyProject, tt.readOnlyProject)
			}
		})
	}
}

// TestBashToolFailsClosedWithoutSandboxWrapperKey proves bash refuses to run
// when invoked outside the execution pipeline (no SandboxWrapperKey in
// context), rather than silently assuming unsandboxed execution.
func TestBashToolFailsClosedWithoutSandboxWrapperKey(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
	}{
		{
			name: "key absent",
			ctx:  context.Background,
		},
		{
			name: "key present with nil wrapper",
			ctx: func() context.Context {
				return context.WithValue(context.Background(), tool.SandboxWrapperKey{}, tool.ResolvedSandbox{})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
			toolDef := NewBashTool(Env{PathPolicy: &policy})

			resultValue, err := toolDef.Handler(tc.ctx(), map[string]any{"command": "true"})
			if err != nil {
				t.Fatalf("Handler() error = %v", err)
			}
			result, ok := resultValue.(*BashResult)
			if !ok {
				t.Fatalf("Handler() result type = %T, want *BashResult", resultValue)
			}
			if result.ExitCode != 255 {
				t.Errorf("ExitCode = %d, want 255", result.ExitCode)
			}
			if !strings.Contains(result.Output, "sandbox wrapper not resolved") {
				t.Error("Output is missing the sandbox wrapper error message")
			}
		})
	}
}
