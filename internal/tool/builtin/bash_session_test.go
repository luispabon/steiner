package builtin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/sandbox"
)

// execSession runs command through the live execute path and returns the raw
// captured stdout and stderr. A timeout is returned as an error, as a session
// failure would be.
func execSession(ctx context.Context, s *BashSession, command string) (stdout, stderr string, exitCode int, err error) {
	so, se, exitCode, timedOut, err := s.execute(ctx, command)
	if err != nil {
		return "", "", -1, err
	}
	if timedOut {
		return "", "", -1, fmt.Errorf("bash session: %w", ctx.Err())
	}
	return string(so.Head) + string(so.Tail), string(se.Head) + string(se.Tail), exitCode, nil
}

func TestBashSession(t *testing.T) {
	t.Run("persistent env vars across calls", func(t *testing.T) {
		s := NewBashSession()
		if err := s.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		defer func() { _ = s.Close() }()

		ctx := context.Background()

		// Set a variable in the first call.
		_, _, code, err := execSession(ctx, s, "export STEINER_TEST_VAR=hello123")
		if err != nil {
			t.Fatalf("Execute set var: %v", err)
		}
		if code != 0 {
			t.Fatalf("set var exit code = %d, want 0", code)
		}

		// Read it back in a second call.
		stdout, _, code, err := execSession(ctx, s, "echo $STEINER_TEST_VAR")
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
		stdout, stderr, code, err := execSession(ctx, s, "echo toout ; echo toerr >&2")
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
		_, _, _, err := execSession(ctx, s, "export STEINER_RESTART_VAR=should_vanish")
		if err != nil {
			t.Fatalf("Execute set: %v", err)
		}

		if err := s.Restart(ctx); err != nil {
			t.Fatalf("Restart: %v", err)
		}

		// After restart the variable should be gone.
		stdout, _, code, err := execSession(ctx, s, "echo \"[$STEINER_RESTART_VAR]\"")
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
			_, _, code, err := execSession(ctx, s, cmd)
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
		_, _, code, err := execSession(ctx, s, "false")
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

		stdout, stderr, code, err := execSession(ctx, s, cmd)
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
		stdout, _, code, err := execSession(ctx, s, "echo hi # trailing comment")
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
		s.CommandWrapper = func(cmd *exec.Cmd) (*exec.Cmd, error) {
			wrapperCalled = true
			wrappedCmd = cmd
			return cmd, nil
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
		stdout, _, code, err := execSession(ctx, s, "echo wrapped")
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
	s.CommandWrapper = func(cmd *exec.Cmd) (*exec.Cmd, error) {
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
		return cmd, nil
	}

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()
	stdout, _, code, err := execSession(ctx, s, `echo "[$STEINER_TEST_FAKE_TOKEN]"`)
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
	if err := sandbox.PrereqCheck(); err != nil {
		t.Skipf("bwrap unusable in this environment: %v", err)
	}

	t.Setenv("STEINER_TEST_FAKE_TOKEN", "x")

	root := t.TempDir()
	sb := sandbox.New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, root, root, t.TempDir(), t.TempDir())
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
	stdout, stderr, code, err := execSession(ctx, s, `echo "[$STEINER_TEST_FAKE_TOKEN]"`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr=%q)", code, stderr)
	}
	if !strings.Contains(stdout, "[]") {
		t.Errorf("stdout = %q, want empty var (sandbox.WrapCommand did not filter the persistent shell's Env)", stdout)
	}
}

// TestJoinCaptureGoroutines pins both branches of the bounded join: finished
// capture goroutines are joined immediately, unfinished ones stop the wait at
// the timeout instead of blocking the caller.
func TestJoinCaptureGoroutines(t *testing.T) {
	t.Run("finished captures join", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		wg.Done()

		if ok := joinCaptureGoroutines(&wg, time.Second); !ok {
			t.Error("joinCaptureGoroutines = false, want true for finished captures")
		}
	})

	t.Run("unfinished captures return at timeout", func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Add(1)
		defer wg.Done() // released when the subtest ends

		const timeout = 20 * time.Millisecond
		start := time.Now()
		ok := joinCaptureGoroutines(&wg, timeout)
		elapsed := time.Since(start)

		if ok {
			t.Error("joinCaptureGoroutines = true, want false while captures are unfinished")
		}
		if elapsed < timeout {
			t.Errorf("joinCaptureGoroutines returned after %v, want at least the %v timeout", elapsed, timeout)
		}
	})
}

// TestBashSession_CancellationJoinsCaptures verifies that a cancelled execute
// returns, does not block indefinitely on the capture goroutines of the
// cancelled generation, and leaves the session usable.
func TestBashSession_CancellationJoinsCaptures(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, _, _, err := execSession(ctx, s, "sleep 30")
		done <- err
	}()

	// The command is still running, so cancellation always takes the restart
	// path while both capture goroutines are blocked on their pipes.
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute error = %v, want context.Canceled", err)
		}
		// Killing the shell closes its pipes, so the capture goroutines are joined
		// well before the bounded timeout: a wait that hit the timeout would mean
		// the join depends on it.
		if elapsed := time.Since(start); elapsed >= bashSessionCaptureJoinTimeout {
			t.Errorf("Execute returned after %v, want the capture goroutines joined well inside the %v bound", elapsed, bashSessionCaptureJoinTimeout)
		}
	case <-time.After(bashSessionCaptureJoinTimeout + 5*time.Second):
		t.Fatal("Execute did not return within the bounded capture join window after cancellation")
	}

	// The restarted session must read a fresh command's output without stale
	// capture goroutines from the cancelled generation interfering.
	stdout, _, code, err := execSession(context.Background(), s, "echo after-cancel")
	if err != nil {
		t.Fatalf("Execute after cancellation: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout, "after-cancel") {
		t.Errorf("stdout = %q, want to contain %q", stdout, "after-cancel")
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

func TestParseBashExitLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		marker    string
		wantCode  int
		wantError bool
	}{
		{
			name:      "valid exit code",
			line:      "__STEINER_EXIT_1__:0",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  0,
			wantError: false,
		},
		{
			name:      "nonzero exit code",
			line:      "__STEINER_EXIT_1__:42",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  42,
			wantError: false,
		},
		{
			name:      "negative exit code",
			line:      "__STEINER_EXIT_1__:-1",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: false,
		},
		{
			name:      "whitespace around exit code",
			line:      "__STEINER_EXIT_1__: 12 ",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  12,
			wantError: false,
		},
		{
			name:      "missing exit marker",
			line:      "garbage text",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: true,
		},
		{
			name:      "unparseable exit code",
			line:      "__STEINER_EXIT_1__:notanumber",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: true,
		},
		{
			name:      "suffix junk",
			line:      "__STEINER_EXIT_1__:12junk",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: true,
		},
		{
			name:      "integer overflow",
			line:      "__STEINER_EXIT_1__:999999999999999999999999999999999999999",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: true,
		},
		{
			name:      "missing colon",
			line:      "__STEINER_EXIT_1__0",
			marker:    "__STEINER_EXIT_1__",
			wantCode:  -1,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, err := parseBashExitLine(tt.line, tt.marker)
			if tt.wantError && err == nil {
				t.Errorf("parseBashExitLine returned no error, want error")
			}
			if !tt.wantError && err != nil {
				t.Errorf("parseBashExitLine returned error %v, want nil", err)
			}
			if !tt.wantError && code != tt.wantCode {
				t.Errorf("parseBashExitLine returned code %d, want %d", code, tt.wantCode)
			}
		})
	}
}

func TestBashSessionExecuteTimeoutKeepsPartials(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The background sleep inherits the pipes and outlives the shell.
	so, se, code, timedOut, err := s.execute(ctx, "sleep 60 & echo partial-out; echo partial-err >&2; sleep 60")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !timedOut || code != -1 {
		t.Errorf("timedOut = %v, code = %d; want true, -1", timedOut, code)
	}
	if elapsed := time.Since(start); elapsed > 2*bashSessionCaptureJoinTimeout {
		t.Errorf("execute took %v, want bounded", elapsed)
	}
	if got := string(so.Head); got != "partial-out\n" {
		t.Errorf("stdout partial = %q, want %q", got, "partial-out\n")
	}
	if got := string(se.Head); got != "partial-err\n" {
		t.Errorf("stderr partial = %q, want %q", got, "partial-err\n")
	}

	out, _, code, err := execSession(context.Background(), s, "echo recovered")
	if err != nil || code != 0 || out != "recovered\n" {
		t.Errorf("after timeout = %q, %d, %v; want recovered, 0, nil", out, code, err)
	}
}

func TestBashSessionExecuteMarkerCollision(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	cmd := "echo __STEINER_STDOUT_1__; echo __STEINER_EXIT_1__:9; echo __STEINER_STDERR_1__ >&2; (exit 4)"
	so, se, code, timedOut, err := s.execute(context.Background(), cmd)
	if err != nil || timedOut {
		t.Fatalf("execute: %v (timedOut=%v)", err, timedOut)
	}
	if code != 4 {
		t.Errorf("exit code = %d, want 4", code)
	}
	if want := "__STEINER_STDOUT_1__\n__STEINER_EXIT_1__:9\n"; string(so.Head) != want {
		t.Errorf("stdout = %q, want %q", so.Head, want)
	}
	if want := "__STEINER_STDERR_1__\n"; string(se.Head) != want {
		t.Errorf("stderr = %q, want %q", se.Head, want)
	}
}

func TestBashSessionExecuteLargeOutputKeepsTail(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	const n = 3_000_000
	so, _, code, timedOut, err := s.execute(ctx, "head -c 3000000 /dev/zero | tr '\\0' a; printf END; (exit 6)")
	if err != nil || timedOut {
		t.Fatalf("execute: %v (timedOut=%v)", err, timedOut)
	}
	if code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	if so.TotalBytes != n+3 || !so.HeadTruncated {
		t.Errorf("TotalBytes = %d, HeadTruncated = %v; want %d, true", so.TotalBytes, so.HeadTruncated, n+3)
	}
	if len(so.Head) != bashSessionCaptureLimit || len(so.Tail) != bashSessionCaptureLimit {
		t.Errorf("head/tail = %d/%d, want %d each", len(so.Head), len(so.Tail), bashSessionCaptureLimit)
	}
	if !strings.HasSuffix(string(so.Tail), "aaaEND") {
		t.Errorf("tail does not end with the real last bytes: %q", so.Tail[max(0, len(so.Tail)-10):])
	}
}

func TestBashSessionShellExitReturnsError(t *testing.T) {
	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	so, _, _, timedOut, err := s.execute(context.Background(), "echo before; exit 3")
	if err == nil || timedOut {
		t.Fatalf("execute err = %v, timedOut = %v; want error from shell exit", err, timedOut)
	}
	if string(so.Head) != "before\n" {
		t.Errorf("partial stdout = %q, want %q", so.Head, "before\n")
	}
}

// TestBashSessionExecuteEscapedDescendant covers a descendant that left the
// shell's process group (own session) and keeps the pipes open after the shell
// is killed. Cancellation must still return within the bounded joins and keep
// what was emitted before the cancel.
func TestBashSessionExecuteEscapedDescendant(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not available")
	}
	pidFile := filepath.Join(t.TempDir(), "pid")

	s := NewBashSession()
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = s.Close() }()

	// Always reap the escaped descendant, even if an assertion fails first.
	t.Cleanup(func() {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return
		}
		var pid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(raw)), "%d", &pid); err != nil || pid <= 0 {
			return
		}
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill() // may already be gone
		}
	})

	cmd := fmt.Sprintf("echo out-before; echo err-before >&2; setsid sh -c 'echo $$ > %[1]s.tmp && mv %[1]s.tmp %[1]s && exec sleep 60' & wait", pidFile)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type result struct {
		so, se   streamCapture
		timedOut bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		so, se, _, timedOut, err := s.execute(ctx, cmd)
		done <- result{so, se, timedOut, err}
	}()

	// Readiness: the descendant has escaped once it published its pid.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("escaped descendant never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	start := time.Now()
	cancel()
	select {
	case r := <-done:
		if r.err != nil || !r.timedOut {
			t.Fatalf("execute err = %v, timedOut = %v; want nil, true", r.err, r.timedOut)
		}
		if elapsed := time.Since(start); elapsed > 2*bashSessionCaptureJoinTimeout+time.Second {
			t.Errorf("cancellation took %v, want bounded joins", elapsed)
		}
		if got := string(r.so.Head); got != "out-before\n" {
			t.Errorf("stdout partial = %q, want %q", got, "out-before\n")
		}
		if got := string(r.se.Head); got != "err-before\n" {
			t.Errorf("stderr partial = %q, want %q", got, "err-before\n")
		}
	case <-time.After(3*bashSessionCaptureJoinTimeout + 5*time.Second):
		t.Fatal("execute did not return after cancellation")
	}

	out, _, code, err := execSession(context.Background(), s, "echo recovered")
	if err != nil || code != 0 || out != "recovered\n" {
		t.Errorf("after cancel = %q, %d, %v; want recovered, 0, nil", out, code, err)
	}
}
