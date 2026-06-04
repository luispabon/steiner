// Adapted from github.com/deepnoodle-ai/dive (MIT License)
package builtin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// bashSessionMaxOutput is the maximum combined output size before truncation.
	bashSessionMaxOutput = 100 * 1024 // 100 KB
)

// BashSession is a persistent bash process with marker-based output capture.
// Attribution: forked and adapted from github.com/deepnoodle-ai/dive
type BashSession struct {
	// CommandWrapper is called before starting the bash process. If non-nil,
	// the returned *exec.Cmd replaces the original. Set this to wrap the process
	// in a sandbox (e.g. bubblewrap). nil means no-op.
	CommandWrapper func(*exec.Cmd) *exec.Cmd

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

	cmd := exec.Command("/bin/bash", "--norc", "--noprofile")

	// Apply the wrapper (for sandbox integration) before starting.
	if s.CommandWrapper != nil {
		cmd = s.CommandWrapper(cmd)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("bash session: stdin pipe: %w", err)
	}

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return fmt.Errorf("bash session: stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		return fmt.Errorf("bash session: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		stderrPipe.Close()
		return fmt.Errorf("bash session: start: %w", err)
	}

	s.cmd = cmd
	s.stdin = stdinPipe
	s.stdoutR = bufio.NewReader(stdoutPipe)
	s.stderrR = bufio.NewReader(stderrPipe)
	s.started = true
	return nil
}

// Execute runs a shell command string and returns stdout, stderr, exitCode, and any session error.
// The context deadline is respected; if exceeded, the session is restarted.
func (s *BashSession) Execute(ctx context.Context, command string) (stdout, stderr string, exitCode int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return "", "", -1, fmt.Errorf("bash session: not started")
	}

	n := s.counter.Add(1)
	stdoutMarker := fmt.Sprintf("__STEINER_STDOUT_%d__", n)
	stderrMarker := fmt.Sprintf("__STEINER_STDERR_%d__", n)
	exitMarker := fmt.Sprintf("__STEINER_EXIT_%d__", n)

	// Inject markers: run the user command, then echo markers + exit code on both streams.
	script := fmt.Sprintf(
		"{ %s ; } ; __exit__=$? ; echo %s ; echo %s >&2 ; echo %s:$__exit__ ; unset __exit__\n",
		command,
		stdoutMarker,
		stderrMarker,
		exitMarker,
	)

	if _, err := io.WriteString(s.stdin, script); err != nil {
		return "", "", -1, fmt.Errorf("bash session: write command: %w", err)
	}

	type captureResult struct {
		text string
		err  error
	}

	stdoutCh := make(chan captureResult, 1)
	stderrCh := make(chan captureResult, 1)

	go func() {
		text, err := readUntilMarker(s.stdoutR, stdoutMarker)
		stdoutCh <- captureResult{text, err}
	}()

	go func() {
		text, err := readUntilMarker(s.stderrR, stderrMarker)
		stderrCh <- captureResult{text, err}
	}()

	var stdoutRes, stderrRes captureResult

	// Wait for both streams, respecting ctx cancellation.
	pending := 2
	for pending > 0 {
		select {
		case <-ctx.Done():
			// Best-effort: restart so the session stays usable.
			_ = s.restartLocked(ctx)
			return "", "", -1, fmt.Errorf("bash session: %w", ctx.Err())
		case r := <-stdoutCh:
			stdoutRes = r
			pending--
		case r := <-stderrCh:
			stderrRes = r
			pending--
		}
	}

	if stdoutRes.err != nil {
		return "", "", -1, fmt.Errorf("bash session: read stdout: %w", stdoutRes.err)
	}
	if stderrRes.err != nil {
		return "", "", -1, fmt.Errorf("bash session: read stderr: %w", stderrRes.err)
	}

	// Read the exit-code line from stdout (emitted after the stdout marker).
	exitLine, err := s.stdoutR.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", "", -1, fmt.Errorf("bash session: read exit line: %w", err)
	}
	exitLine = strings.TrimSpace(exitLine)

	code := -1
	prefix := exitMarker + ":"
	if strings.HasPrefix(exitLine, prefix) {
		var parsed int
		if _, scanErr := fmt.Sscanf(exitLine[len(prefix):], "%d", &parsed); scanErr == nil {
			code = parsed
		}
	}

	stdoutText, stdoutTrunc := maybeTruncate(stdoutRes.text, bashSessionMaxOutput)
	stderrText, stderrTrunc := maybeTruncate(stderrRes.text, bashSessionMaxOutput)

	if stdoutTrunc {
		stdoutText += "\n[output truncated]"
	}
	if stderrTrunc {
		stderrText += "\n[output truncated]"
	}

	return stdoutText, stderrText, code, nil
}

// Restart kills and restarts the bash process, clearing all session state.
func (s *BashSession) Restart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.restartLocked(ctx)
}

// restartLocked performs the restart with the lock already held.
func (s *BashSession) restartLocked(_ context.Context) error {
	if s.cmd != nil {
		_ = s.stdin.Close()
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
	}
	s.cmd = nil
	s.stdin = nil
	s.stdoutR = nil
	s.stderrR = nil
	s.started = false

	cmd := exec.Command("/bin/bash", "--norc", "--noprofile")
	if s.CommandWrapper != nil {
		cmd = s.CommandWrapper(cmd)
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("bash session: restart stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return fmt.Errorf("bash session: restart stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		return fmt.Errorf("bash session: restart stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		stderrPipe.Close()
		return fmt.Errorf("bash session: restart start: %w", err)
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
	_ = s.cmd.Process.Kill()
	err := s.cmd.Wait()
	s.started = false
	s.cmd = nil
	s.stdin = nil
	s.stdoutR = nil
	s.stderrR = nil

	// Ignore "signal: killed" — that is expected from our Kill call.
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			_ = exitErr // expected
			return nil
		}
		return fmt.Errorf("bash session: close: %w", err)
	}
	return nil
}

// readUntilMarker reads lines from r until it encounters a line that is exactly
// the given marker, then returns everything read before the marker.
func readUntilMarker(r *bufio.Reader, marker string) (string, error) {
	var sb strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil && err != io.EOF {
			return sb.String(), err
		}
		trimmed := strings.TrimRight(line, "\n")
		if trimmed == marker {
			return sb.String(), nil
		}
		sb.WriteString(line)
		if err == io.EOF {
			// Unexpected EOF before marker — process likely died.
			return sb.String(), io.ErrUnexpectedEOF
		}
	}
}

// maybeTruncate truncates s to maxBytes and returns whether truncation occurred.
func maybeTruncate(s string, maxBytes int) (string, bool) {
	if len(s) <= maxBytes {
		return s, false
	}
	return s[:maxBytes], true
}
