// Adapted from github.com/deepnoodle-ai/dive (MIT License)
package builtin

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// bashSessionMaxOutput is the maximum combined output size before truncation.
	bashSessionMaxOutput = 100 * 1024 // 100 KB

	// bashSessionCaptureLimit is the raw head and the raw tail kept per stream.
	bashSessionCaptureLimit = 1 << 20 // 1 MiB

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
	CommandWrapper          func(*exec.Cmd) (*exec.Cmd, error)
	ReleaseCommandResources func(*exec.Cmd)

	mu      sync.Mutex
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdoutR *bufio.Reader
	stderrR *bufio.Reader

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
		wrapped, err := s.CommandWrapper(cmd)
		if err != nil {
			return fmt.Errorf("bash session: wrap command: %w", err)
		}
		cmd = wrapped
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
	so, se, exitCode, timedOut, err := s.execute(ctx, command)
	if err != nil {
		return "", "", -1, err
	}
	if timedOut {
		return "", "", -1, fmt.Errorf("bash session: %w", ctx.Err())
	}
	stdout, stderr, _ = adaptCaptures(so, se)
	return stdout, stderr, exitCode, nil
}

// adaptCaptures renders captures as the legacy capped strings: the first
// bashSessionMaxOutput bytes per stream plus a truncation notice. Final output
// shaping from head and tail is a later step.
func adaptCaptures(stdout, stderr streamCapture) (so, se string, truncated bool) {
	so, st := adaptCapture(stdout)
	se, et := adaptCapture(stderr)
	return so, se, st || et
}

func adaptCapture(c streamCapture) (string, bool) {
	if c.TotalBytes <= bashSessionMaxOutput {
		return string(c.Head) + string(c.Tail), false
	}
	head := c.Head[:min(len(c.Head), bashSessionMaxOutput)]
	return trimIncompleteUTF8SuffixString(string(head)) + "\n[output truncated]", true
}

// newBashMarker returns an unpredictable per-call marker so command output
// cannot collide with the protocol markers.
func newBashMarker(kind string) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate marker: %w", err)
	}
	return "__STEINER_" + kind + "_" + hex.EncodeToString(b[:]) + "__", nil
}

// bashCaptureResult is what one stream's capture goroutine reports.
type bashCaptureResult struct {
	capture  streamCapture
	exitLine string
	err      error
}

// execute runs command and returns bounded captures of both streams. When ctx
// ends first, the shell is killed and restarted, the partial captures are
// returned with timedOut set and a nil error.
func (s *BashSession) execute(ctx context.Context, command string) (stdout, stderr streamCapture, exitCode int, timedOut bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return stdout, stderr, -1, false, errors.New("bash session: not started")
	}

	var stdoutMarker, stderrMarker, exitMarker string
	for _, m := range []struct {
		dst  *string
		kind string
	}{{&stdoutMarker, "STDOUT"}, {&stderrMarker, "STDERR"}, {&exitMarker, "EXIT"}} {
		if *m.dst, err = newBashMarker(m.kind); err != nil {
			return stdout, stderr, -1, false, fmt.Errorf("bash session: %w", err)
		}
	}

	// Inject markers: run the user command, then echo markers + exit code on both streams.
	// The closing brace and marker suffix must be on their own line: appending them via
	// ";" on the same line as the user's command would corrupt heredocs, here-strings, and
	// trailing comments, whose terminators must appear alone on their line.
	script := fmt.Sprintf(
		"{\n%s\n} </dev/null ; __exit__=$? ; echo %s ; echo %s >&2 ; echo %s:$__exit__ ; unset __exit__\n",
		command,
		stdoutMarker,
		stderrMarker,
		exitMarker,
	)

	if _, err := io.WriteString(s.stdin, script); err != nil {
		return stdout, stderr, -1, false, fmt.Errorf("bash session: write command: %w", err)
	}

	stdoutCh := make(chan bashCaptureResult, 1)
	stderrCh := make(chan bashCaptureResult, 1)

	// Capture the readers of this generation: a cancellation restarts the
	// session and replaces s.stdoutR/s.stderrR, so the goroutines must keep
	// reading (and reporting on) the pipes they were started for.
	stdoutR := s.stdoutR
	stderrR := s.stderrR

	var captures sync.WaitGroup
	captures.Add(2)

	// The capture leaves the newline that ends each marker line unread; consume
	// it, and on stdout the exit line that follows, so the next call starts clean.
	go func() {
		defer captures.Done()
		res := bashCaptureResult{}
		res.capture, res.err = captureUntilMarker(stdoutR, stdoutMarker, bashSessionCaptureLimit, bashSessionCaptureLimit)
		if res.err == nil {
			if _, res.err = stdoutR.ReadString('\n'); res.err == nil {
				res.exitLine, res.err = stdoutR.ReadString('\n')
			}
			if errors.Is(res.err, io.EOF) && res.exitLine != "" {
				res.err = nil
			}
		}
		stdoutCh <- res
	}()

	go func() {
		defer captures.Done()
		res := bashCaptureResult{}
		res.capture, res.err = captureUntilMarker(stderrR, stderrMarker, bashSessionCaptureLimit, bashSessionCaptureLimit)
		if res.err == nil {
			_, res.err = stderrR.ReadString('\n')
		}
		stderrCh <- res
	}()

	var stdoutRes, stderrRes bashCaptureResult
	var stdoutDone, stderrDone bool

	// Wait for both streams, respecting ctx cancellation.
	for !stdoutDone || !stderrDone {
		select {
		case <-ctx.Done():
			s.cancelInFlight(&captures)
			// Collect partials from goroutines that finished; ones still
			// stuck past the join bound leave an empty capture.
			if !stdoutDone {
				select {
				case stdoutRes = <-stdoutCh:
				default:
				}
			}
			if !stderrDone {
				select {
				case stderrRes = <-stderrCh:
				default:
				}
			}
			return stdoutRes.capture, stderrRes.capture, -1, true, nil
		case stdoutRes = <-stdoutCh:
			stdoutDone = true
		case stderrRes = <-stderrCh:
			stderrDone = true
		}
	}

	if stdoutRes.err != nil || stderrRes.err != nil {
		return stdoutRes.capture, stderrRes.capture, -1, false, bashStreamReadError(
			string(stdoutRes.capture.Head)+string(stdoutRes.capture.Tail), stdoutRes.err,
			string(stderrRes.capture.Head)+string(stderrRes.capture.Tail), stderrRes.err)
	}

	code, parseErr := parseBashExitLine(strings.TrimSpace(stdoutRes.exitLine), exitMarker)
	if parseErr != nil {
		return stdoutRes.capture, stderrRes.capture, -1, false, fmt.Errorf("bash session: %w", parseErr)
	}
	return stdoutRes.capture, stderrRes.capture, code, false, nil
}

// cancelInFlight kills the shell after ctx cancelled an in-flight command and
// joins that generation's capture goroutines (bounded) so their partial
// captures can be collected, then restarts the session. Killing the shell's
// process group closes the pipes, and the restart closes the read ends, so
// background descendants holding the descriptors cannot hang the caller.
func (s *BashSession) cancelInFlight(captures *sync.WaitGroup) {
	if s.cmd != nil {
		_ = s.stdin.Close()
		killBashProcess(s.cmd)
	}
	joined := joinCaptureGoroutines(captures, bashSessionCaptureJoinTimeout)
	s.restartAfterCancel(context.Background())
	if !joined && !joinCaptureGoroutines(captures, bashSessionCaptureJoinTimeout) {
		slog.Warn("bash session: capture goroutines still running after cancellation",
			"timeout", bashSessionCaptureJoinTimeout)
	}
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
		wrapped, err := s.CommandWrapper(cmd)
		if err != nil {
			return fmt.Errorf("bash session: restart wrap command: %w", err)
		}
		cmd = wrapped
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
