package builtin

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/sandbox"
)

func TestBashSession(t *testing.T) {
	t.Run("persistent env vars across calls", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()

		// Set a variable in the first call.
		_, _, code, err := s.Execute(ctx, "export STEINER_TEST_VAR=hello123")
		if err != nil {
			t.Fatalf("Execute set var: %v", err)
		}
		if code != 0 {
			t.Fatalf("set var exit code = %d, want 0", code)
		}

		// Read it back in a second call.
		stdout, _, code, err := s.Execute(ctx, "echo $STEINER_TEST_VAR")
		if err != nil {
			t.Fatalf("Execute get var: %v", err)
		}
		if code != 0 {
			t.Fatalf("get var exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "hello123") {
			t.Errorf("stdout = %q, want to contain %q", stdout, "hello123")
		}
	})

	t.Run("stderr capture", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()
		stdout, stderr, code, err := s.Execute(ctx, "echo toout ; echo toerr >&2")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "toout") {
			t.Errorf("stdout = %q, want to contain %q", stdout, "toout")
		}
		if !strings.Contains(stderr, "toerr") {
			t.Errorf("stderr = %q, want to contain %q", stderr, "toerr")
		}
	})

	t.Run("session restart clears state", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()

		// Set a variable before restart.
		_, _, _, err := s.Execute(ctx, "export STEINER_RESTART_VAR=should_vanish")
		if err != nil {
			t.Fatalf("Execute set: %v", err)
		}

		if err := s.Restart(ctx); err != nil {
			t.Fatalf("Restart: %v", err)
		}

		// After restart the variable should be gone.
		stdout, _, code, err := s.Execute(ctx, "echo \"[$STEINER_RESTART_VAR]\"")
		if err != nil {
			t.Fatalf("Execute after restart: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if strings.Contains(stdout, "should_vanish") {
			t.Errorf("stdout = %q, variable survived restart", stdout)
		}
	})

	t.Run("multiple sequential commands succeed", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()
		commands := []string{
			"echo one",
			"echo two",
			"echo three",
			"true",
			"echo five",
		}
		for _, cmd := range commands {
			_, _, code, err := s.Execute(ctx, cmd)
			if err != nil {
				t.Fatalf("Execute(%q): %v", cmd, err)
			}
			if code != 0 {
				t.Errorf("Execute(%q) exit code = %d, want 0", cmd, code)
			}
		}
	})

	t.Run("non-zero exit code is captured", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()
		_, _, code, err := s.Execute(ctx, "false")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if code == 0 {
			t.Errorf("exit code = 0, want non-zero for 'false'")
		}
	})

	t.Run("heredoc command completes without hanging", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()
		outFile := filepath.Join(t.TempDir(), "heredoc.txt")
		cmd := fmt.Sprintf("cat > %s << 'EOF'\nline one\nline two\nEOF", outFile)

		stdout, stderr, code, err := s.Execute(ctx, cmd)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0 (stdout=%q stderr=%q)", code, stdout, stderr)
		}

		got, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if want := "line one\nline two\n"; string(got) != want {
			t.Errorf("file contents = %q, want %q", got, want)
		}
	})

	t.Run("trailing comment on last line does not hang", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()
		stdout, _, code, err := s.Execute(ctx, "echo hi # trailing comment")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "hi") {
			t.Errorf("stdout = %q, want to contain %q", stdout, "hi")
		}
	})

	t.Run("CommandWrapper hook is called", func(t *testing.T) {
		wrapperCalled := false
		var wrappedCmd *exec.Cmd

		s := NewBashSession()
		s.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
			wrapperCalled = true
			wrappedCmd = cmd
			return cmd
		}

		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		if !wrapperCalled {
			t.Error("CommandWrapper was not called during Start")
		}
		if wrappedCmd == nil {
			t.Error("CommandWrapper received nil cmd")
		}

		// Verify the session is functional after wrapping.
		ctx := context.Background()
		stdout, _, code, err := s.Execute(ctx, "echo wrapped")
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "wrapped") {
			t.Errorf("stdout = %q, want to contain %q", stdout, "wrapped")
		}
	})
}

func TestBashSession_CommandWrapperFiltersEnv(t *testing.T) {
	t.Setenv("STEINER_TEST_FAKE_TOKEN", "x")

	s := NewBashSession()
	s.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
		host := cmd.Env
		if host == nil {
			host = os.Environ()
		}
		var filtered []string
		for _, kv := range host {
			if strings.HasPrefix(kv, "PATH=") {
				filtered = append(filtered, kv)
			}
		}
		cmd.Env = filtered
		return cmd
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	stdout, _, code, err := s.Execute(ctx, `echo "[$STEINER_TEST_FAKE_TOKEN]"`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "[]") {
		t.Errorf("stdout = %q, want empty var (CommandWrapper's filtered Env not applied to the persistent shell)", stdout)
	}
}

func TestBashSession_RealSandboxWrapperFiltersEnv(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bwrap integration only runs on Linux")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed")
	}

	t.Setenv("STEINER_TEST_FAKE_TOKEN", "x")

	root := t.TempDir()
	sb := sandbox.New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, nil, root, root, t.TempDir(), t.TempDir())
	if err := sb.EnsureHome(); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}

	s := NewBashSession()
	s.CommandWrapper = sb.WrapCommand

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	stdout, stderr, code, err := s.Execute(ctx, `echo "[$STEINER_TEST_FAKE_TOKEN]"`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		if strings.Contains(strings.ToLower(stderr), "operation not permitted") ||
			strings.Contains(strings.ToLower(stderr), "creating new namespace failed") {
			t.Skipf("sandbox unavailable in this environment: %s", stderr)
		}
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stdout, "[]") {
		t.Errorf("stdout = %q, want empty var (sandbox.WrapCommand did not filter the persistent shell's Env)", stdout)
	}
}

func TestBashStreamReadErrorIncludesCapturedStderr(t *testing.T) {
	err := bashStreamReadError("", io.ErrUnexpectedEOF, "bwrap: mount failed\n", io.ErrUnexpectedEOF)
	if err == nil {
		t.Fatal("error is nil")
	}
	msg := err.Error()
	for _, want := range []string{
		"read stdout: unexpected EOF",
		"read stderr: unexpected EOF",
		"stderr: bwrap: mount failed",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want substring %q", msg, want)
		}
	}
}
