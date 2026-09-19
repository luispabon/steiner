// Adapted from github.com/deepnoodle-ai/dive (MIT License)
package builtin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// bashSessionMaxOutput is the maximum combined output size before truncation.
	bashSessionMaxOutput = 100 * 1024 // 100 KB

	// bashSessionCaptureJoinTimeout bounds how long a cancelled Execute waits
	// for the capture goroutines of the cancelled generation. Killing the shell
	// closes its pipe write ends, so those reads normally return at once;
	// descendants that inherited the pipe descriptors can keep them open past
	// the kill, so the wait is bounded rather than open-ended.
	bashSessionCaptureJoinTimeout = 2 * time.Second
)

// BashSession is a persistent bash process with marker-based output capture.
// Attribution: forked and adapted from github.com/deepnoodle-ai/dive
type BashSession struct {
	// CommandWrapper is called before starting the bash process. If non-nil,
	// the returned *exec.Cmd replaces the original. Set this to wrap the process
	// in a sandbox (e.g. bubblewrap). nil means no-op.
	CommandWrapper          func(*exec.Cmd) *exec.Cmd
	ReleaseCommandResources func(*exec.Cmd)

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdoutR *bufio.Reader
	stderrR *bufio.Reader

	counter atomic.Uint64
	started bool
}

// NewBashSession creates a new BashSession. Call Start before Execute.
func NewBashSession() *BashSession {
	return &BashSession{}
}

// Start starts the bash process. Calls CommandWrapper if set.
func (s *BashSession) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("bash session: already started")
	}

	cmd := exec.Command("/bin/bash", "--norc", "--noprofile") //nolint:noctx // persistent session process, not tied to a single request context

	// Apply the wrapper (for sandbox integration) before starting.
	if s.CommandWrapper != nil {
		cmd = s.CommandWrapper(cmd)
	}
	setBashProcessGroup(cmd)
	release := onceRelease(s.ReleaseCommandResources, cmd)
	defer release()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("bash session: stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return fmt.Errorf("bash session: stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return fmt.Errorf("bash session: stderr pipe: %w", err)
	}

	startErr := cmd.Start()
	release()
	if startErr != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return fmt.Errorf("bash session: start: %w", startErr)
	}

	s.cmd = cmd
	s.stdin = stdinPipe
	s.stdoutR = bufio.NewReader(stdoutPipe)
	s.stderrR = bufio.NewReader(stderrPipe)
	s.started = true
	return nil
}

func onceRelease(releaseFn func(*exec.Cmd), cmd *exec.Cmd) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			if releaseFn != nil {
				releaseFn(cmd)
			}
		})
	}
}

// Execute runs a shell command string and returns stdout, stderr, exitCode, and any session error.
// The context deadline is respected; if exceeded, the session is restarted.
func (s *BashSession) Execute(ctx context.Context, command string) (stdout, stderr string, exitCode int, err error) {
	stdout, stderr, exitCode, _, err = s.execute(ctx, command)
	return stdout, stderr, exitCode, err
}

// execute is Execute plus a flag reporting whether either stream was truncated.
func (s *BashSession) execute(ctx context.Context, command string) (stdout, stderr string, exitCode int, truncated bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return "", "", -1, false, fmt.Errorf("bash session: not started")
	}

	n := s.counter.Add(1)
	stdoutMarker := fmt.Sprintf("__STEINER_STDOUT_%d__", n)
	stderrMarker := fmt.Sprintf("__STEINER_STDERR_%d__", n)
	exitMarker := fmt.Sprintf("__STEINER_EXIT_%d__", n)

	// Inject markers: run the user command, then echo markers + exit code on both streams.
	// The closing brace and marker suffix must be on their own line: appending them via
	// ";" on the same line as the user's command would corrupt heredocs, here-strings, and
	// trailing comments, whose terminators must appear alone on their line.
	script := fmt.Sprintf(
		"{\n%s\n} ; __exit__=$? ; echo %s ; echo %s >&2 ; echo %s:$__exit__ ; unset __exit__\n",
		command,
		stdoutMarker,
		stderrMarker,
		exitMarker,
	)

	if _, err := io.WriteString(s.stdin, script); err != nil {
		return "", "", -1, false, fmt.Errorf("bash session: write command: %w", err)
	}

	type captureResult struct {
		text  string
		trunc bool
		err   error
	}

	stdoutCh := make(chan captureResult, 1)
	stderrCh := make(chan captureResult, 1)

	// Capture the readers of this generation: a cancellation restarts the
	// session and replaces s.stdoutR/s.stderrR, so the goroutines must keep
	// reading (and reporting on) the pipes they were started for.
	stdoutR := s.stdoutR
	stderrR := s.stderrR

	var captures sync.WaitGroup
	captures.Add(2)

	go func() {
		defer captures.Done()
		text, trunc, err := readUntilMarker(stdoutR, stdoutMarker, bashSessionMaxOutput)
		stdoutCh <- captureResult{text, trunc, err}
	}()

	go func() {
		defer captures.Done()
		text, trunc, err := readUntilMarker(stderrR, stderrMarker, bashSessionMaxOutput)
		stderrCh <- captureResult{text, trunc, err}
	}()

	var stdoutRes, stderrRes captureResult

	// Wait for both streams, respecting ctx cancellation.
	pending := 2
	for pending > 0 {
		select {
		case <-ctx.Done():
			return "", "", -1, false, s.cancelInFlight(ctx, &captures)
		case r := <-stdoutCh:
			stdoutRes = r
			pending--
		case r := <-stderrCh:
			stderrRes = r
			pending--
		}
	}

	if stdoutRes.err != nil || stderrRes.err != nil {
		return "", "", -1, false, bashStreamReadError(stdoutRes.text, stdoutRes.err, stderrRes.text, stderrRes.err)
	}

	// Read the exit-code line from stdout (emitted after the stdout marker).
	exitLine, err := s.stdoutR.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", "", -1, false, fmt.Errorf("bash session: read exit line: %w", err)
	}
	exitLine = strings.TrimSpace(exitLine)

	code, parseErr := parseBashExitLine(exitLine, exitMarker)
	if parseErr != nil {
		return "", "", -1, false, fmt.Errorf("bash session: %w", parseErr)
	}

	stdoutText, stderrText := stdoutRes.text, stderrRes.text
	if stdoutRes.trunc {
		stdoutText += "\n[output truncated]"
	}
	if stderrRes.trunc {
		stderrText += "\n[output truncated]"
	}

	return stdoutText, stderrText, code, stdoutRes.trunc || stderrRes.trunc, nil
}

// cancelInFlight restarts the session after ctx cancelled an in-flight command
// and joins that generation's capture goroutines, returning the cancellation
// error for the caller to surface.
func (s *BashSession) cancelInFlight(ctx context.Context, captures *sync.WaitGroup) error {
	s.restartAfterCancel(ctx)
	if !joinCaptureGoroutines(captures, bashSessionCaptureJoinTimeout) {
		slog.Warn("bash session: capture goroutines still running after cancellation",
			"timeout", bashSessionCaptureJoinTimeout)
	}
	return fmt.Errorf("bash session: %w", ctx.Err())
}

// joinCaptureGoroutines waits for the capture goroutines registered with wg to
// finish, up to timeout, and reports whether they did.
func joinCaptureGoroutines(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func bashStreamReadError(stdoutText string, stdoutErr error, stderrText string, stderrErr error) error {
	var parts []string
	if stdoutErr != nil {
		parts = append(parts, fmt.Sprintf("read stdout: %v", stdoutErr))
	}
	if stderrErr != nil {
		parts = append(parts, fmt.Sprintf("read stderr: %v", stderrErr))
	}
	if stderrText = strings.TrimSpace(stderrText); stderrText != "" {
		parts = append(parts, "stderr: "+stderrText)
	}
	if stdoutText = strings.TrimSpace(stdoutText); stdoutText != "" {
		parts = append(parts, "stdout: "+stdoutText)
	}
	return fmt.Errorf("bash session: %s", strings.Join(parts, "; "))
}

// Restart kills and restarts the bash process, clearing all session state.
func (s *BashSession) Restart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restartLocked(ctx)
}

// restartAfterCancel restarts the session with the lock already held after
// ctx was cancelled mid-command, so a dead session doesn't linger unusable.
// Best-effort: logs a warning rather than surfacing the restart failure,
// since the caller already has a cancellation error to return.
func (s *BashSession) restartAfterCancel(ctx context.Context) {
	if err := s.restartLocked(ctx); err != nil {
		slog.Warn("restart bash session", "error", err)
	}
}

// restartLocked performs the restart with the lock already held.
func (s *BashSession) restartLocked(_ context.Context) error {
	if s.cmd != nil {
		_ = s.stdin.Close()
		killBashProcess(s.cmd)
		_ = s.cmd.Wait()
	}
	s.cmd = nil
	s.stdin = nil
	s.stdoutR = nil
	s.stderrR = nil
	s.started = false

	cmd := exec.Command("/bin/bash", "--norc", "--noprofile") //nolint:noctx // persistent session process, not tied to a single request context
	if s.CommandWrapper != nil {
		cmd = s.CommandWrapper(cmd)
	}
	setBashProcessGroup(cmd)
	release := onceRelease(s.ReleaseCommandResources, cmd)
	defer release()

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("bash session: restart stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdinPipe.Close()
		return fmt.Errorf("bash session: restart stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		return fmt.Errorf("bash session: restart stderr pipe: %w", err)
	}
	startErr := cmd.Start()
	release()
	if startErr != nil {
		_ = stdinPipe.Close()
		_ = stdoutPipe.Close()
		_ = stderrPipe.Close()
		return fmt.Errorf("bash session: restart start: %w", startErr)
	}

	s.cmd = cmd
	s.stdin = stdinPipe
	s.stdoutR = bufio.NewReader(stdoutPipe)
	s.stderrR = bufio.NewReader(stderrPipe)
	s.started = true
	return nil
}

// Close shuts down the bash process.
func (s *BashSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started || s.cmd == nil {
		return nil
	}

	_ = s.stdin.Close()
	killBashProcess(s.cmd)
	err := s.cmd.Wait()
	s.started = false
	s.cmd = nil
	s.stdin = nil
	s.stdoutR = nil
	s.stderrR = nil

	// Ignore "signal: killed" — that is expected from our Kill call.
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil
		}
		return fmt.Errorf("bash session: close: %w", err)
	}
	return nil
}

// readUntilMarker reads lines from r until it encounters a line that is exactly
// the given marker, then returns the first maxBytes of everything read before
// it and whether anything was dropped. Memory stays bounded: lines are read in
// bufio-sized fragments and bytes past maxBytes are discarded while scanning
// continues, so the marker is still found however much output precedes it.
func readUntilMarker(r *bufio.Reader, marker string, maxBytes int) (string, bool, error) {
	var buf []byte
	truncated := false
	finish := func(err error) (string, bool, error) {
		text := string(buf)
		if truncated {
			text = trimIncompleteUTF8SuffixString(text)
		}
		return text, truncated, err
	}
	appendCapped := func(b []byte) {
		room := maxBytes - len(buf)
		if room < 0 {
			room = 0
		}
		if len(b) > room {
			b = b[:room]
			truncated = true
		}
		buf = append(buf, b...)
	}

	midLine := false
	for {
		frag, err := r.ReadSlice('\n')
		if err != nil && err != bufio.ErrBufferFull && err != io.EOF {
			return finish(err)
		}
		if !midLine && err == nil && string(frag[:len(frag)-1]) == marker {
			return finish(nil)
		}
		appendCapped(frag)
		switch err {
		case bufio.ErrBufferFull:
			midLine = true
		case io.EOF:
			return finish(io.ErrUnexpectedEOF)
		default:
			midLine = false
		}
	}
}

// parseBashExitLine parses the exit code from a bash session exit marker line.
// The line must match the format "exitMarker:code" where code is an integer.
// If the line doesn't match or the code cannot be parsed, an error is returned.
func parseBashExitLine(line, exitMarker string) (int, error) {
	prefix := exitMarker + ":"
	if !strings.HasPrefix(line, prefix) {
		return -1, fmt.Errorf("missing exit marker %q", exitMarker)
	}

	code, err := strconv.Atoi(strings.TrimSpace(line[len(prefix):]))
	if err != nil {
		return -1, fmt.Errorf("parse exit code: %w", err)
	}

	return code, nil
}
