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

func (w *recordingWrapper) WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) (*exec.Cmd, error) {
	w.calls++
	w.lastReadOnlyProject = readOnlyProject
	return cmd, nil
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
		wantDir, err := filepath.EvalSymlinks(tmpDir)
		if err != nil {
			t.Fatalf("EvalSymlinks: %v", err)
		}
		pwdOut := strings.TrimSpace(result.Output)
		if pwdOut != wantDir {
			t.Fatalf("pwd output = %q, want %q", pwdOut, wantDir)
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
			t.Fatalf("unexpected error: %v", err)
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

// TestBashToolConfiguredTimeoutCapInSchema proves NewBashTool threads the Env
// cap into the parameter schema: the 300-second cap is the schema maximum and
// the default stays at the 30-second request default.
func TestBashToolConfiguredTimeoutCapInSchema(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	toolDef := NewBashTool(Env{PathPolicy: &policy, BashTimeoutCap: 300 * time.Second})

	props, _ := toolDef.ParameterSchema["properties"].(map[string]any)
	ts, _ := props["timeout_seconds"].(map[string]any)
	if ts == nil {
		t.Fatal("bash schema missing timeout_seconds")
	}
	if got := ts["maximum"]; got != 300 {
		t.Errorf("timeout_seconds maximum = %v, want 300", got)
	}
	if got := ts["default"]; got != defaultBashTimeoutSeconds {
		t.Errorf("timeout_seconds default = %v, want %d", got, defaultBashTimeoutSeconds)
	}
}

// TestBashToolZeroEnvUsesFiniteCap proves a non-positive or sub-second Env cap
// falls back to the finite 120-second default in the schema rather than a zero
// maximum, and that the fallback actually governs handler execution: a fresh
// tool for each fallback cap runs a command bounded by the requested
// timeout_seconds (2) rather than an invalid zero cap.
func TestBashToolZeroEnvUsesFiniteCap(t *testing.T) {
	tests := []struct {
		name string
		cap  time.Duration
	}{
		{name: "zero cap", cap: 0},
		{name: "negative cap", cap: -1 * time.Second},
		{name: "sub-second cap", cap: 500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
			toolDef := NewBashTool(Env{PathPolicy: &policy, BashTimeoutCap: tt.cap})

			props, _ := toolDef.ParameterSchema["properties"].(map[string]any)
			ts, _ := props["timeout_seconds"].(map[string]any)
			if ts == nil {
				t.Fatal("bash schema missing timeout_seconds")
			}
			if got := ts["maximum"]; got != defaultBashTimeoutCapSeconds {
				t.Errorf("timeout_seconds maximum = %v, want %d", got, defaultBashTimeoutCapSeconds)
			}
			if got := ts["default"]; got != defaultBashTimeoutSeconds {
				t.Errorf("timeout_seconds default = %v, want %d", got, defaultBashTimeoutSeconds)
			}

			ctx := withUnsandboxedWrapper(context.Background())
			resultValue, err := toolDef.Handler(ctx, map[string]any{
				"command":         "sleep 0.75; printf fallback-ok",
				"timeout_seconds": 2,
			})
			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			result, ok := resultValue.(*BashResult)
			if !ok {
				t.Fatalf("result type = %T, want *BashResult", resultValue)
			}
			if result.ExitCode != 0 {
				t.Errorf("ExitCode = %d, want 0", result.ExitCode)
			}
			if !strings.Contains(result.Output, "fallback-ok") {
				t.Errorf("Output = %q, want to contain %q", result.Output, "fallback-ok")
			}
		})
	}
}

// TestBashToolTimeoutResultAndRecovery proves a short lifecycle timeout still
// yields the existing timeout result (not a Go error) and that the tool stays
// usable for a later call.
func TestBashToolTimeoutResultAndRecovery(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	toolDef := NewBashTool(Env{PathPolicy: &policy, BashTimeoutCap: time.Second})

	shortCtx, cancel := context.WithTimeout(withUnsandboxedWrapper(context.Background()), 20*time.Millisecond)
	defer cancel()

	resultValue, err := toolDef.Handler(shortCtx, map[string]any{
		"command":         "sleep 5",
		"timeout_seconds": 30,
	})
	if err != nil {
		t.Fatalf("timeout handler error = %v", err)
	}
	timeoutResult, ok := resultValue.(*BashResult)
	if !ok {
		t.Fatalf("result type = %T, want *BashResult", resultValue)
	}
	if timeoutResult.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero for timed out command")
	}
	if timeoutResult.Output == "" {
		t.Error("Output is empty, want timeout error message")
	}

	recoveryValue, err := toolDef.Handler(withUnsandboxedWrapper(context.Background()), map[string]any{"command": "echo recovered"})
	if err != nil {
		t.Fatalf("recovery handler error = %v", err)
	}
	recovery, ok := recoveryValue.(*BashResult)
	if !ok {
		t.Fatalf("recovery result type = %T, want *BashResult", recoveryValue)
	}
	if recovery.ExitCode != 0 {
		t.Errorf("recovery ExitCode = %d, want 0", recovery.ExitCode)
	}
	if !strings.Contains(recovery.Output, "recovered") {
		t.Errorf("recovery Output = %q, want to contain %q", recovery.Output, "recovered")
	}
}

// TestBashToolEnforcesConfiguredTimeoutCap proves the handler's own configured
// cap, rather than parent-context cancellation, expires an over-cap request:
// with an uncancelled parent context, a request above the 1-second cap, and a
// command that would otherwise outlive the cap, the handler still returns the
// established timeout result once the cap elapses, and the session recovers
// afterwards.
func TestBashToolEnforcesConfiguredTimeoutCap(t *testing.T) {
	policy := tool.NewPathPolicy(t.TempDir(), config.PathsConfig{})
	toolDef := NewBashTool(Env{PathPolicy: &policy, BashTimeoutCap: time.Second})

	// No parent deadline: only the configured cap can expire this call.
	ctx := withUnsandboxedWrapper(context.Background())

	start := time.Now()
	resultValue, err := toolDef.Handler(ctx, map[string]any{
		"command":         "sleep 10",
		"timeout_seconds": 30,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("handler error = %v", err)
	}
	result, ok := resultValue.(*BashResult)
	if !ok {
		t.Fatalf("result type = %T, want *BashResult", resultValue)
	}
	if result.ExitCode == 0 {
		t.Errorf("ExitCode = 0, want non-zero for cap-expired command")
	}
	if !strings.Contains(result.Output, "context deadline exceeded") {
		t.Errorf("Output = %q, want to contain %q", result.Output, "context deadline exceeded")
	}
	// The 1-second cap, not the 30-second request, governs: without cap
	// enforcement the command would run its full 10 seconds.
	if elapsed >= 5*time.Second {
		t.Errorf("elapsed = %v, want well under the 10s command runtime", elapsed)
	}

	recoveryValue, err := toolDef.Handler(withUnsandboxedWrapper(context.Background()), map[string]any{"command": "echo recovered"})
	if err != nil {
		t.Fatalf("recovery handler error = %v", err)
	}
	recovery, ok := recoveryValue.(*BashResult)
	if !ok {
		t.Fatalf("recovery result type = %T, want *BashResult", recoveryValue)
	}
	if recovery.ExitCode != 0 {
		t.Errorf("recovery ExitCode = %d, want 0", recovery.ExitCode)
	}
	if !strings.Contains(recovery.Output, "recovered") {
		t.Errorf("recovery Output = %q, want to contain %q", recovery.Output, "recovered")
	}
}
