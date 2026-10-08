package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// claudeSubScannerMaxBytes bounds one stdout JSON line. It is a variable so the
// reader's ceiling is directly testable.
var claudeSubScannerMaxBytes = 64 << 20 // 64 MiB

const (
	claudeSubEventBuffer     = 256
	claudeSubStderrTailBytes = 8 << 10 // last 8 KiB of stderr
)

// claudeSubCloseGrace bounds two things in the coordinator: how long it waits
// for the CLI to exit on its own after stdin EOF before terminating the tree,
// and how long it lets the readers drain after tree termination before
// force-closing the owned read handles. Tests shorten it.
var claudeSubCloseGrace = 5 * time.Second

// claudeSubObserverPoll bounds how long a stopped platform exit observer takes
// to notice its stop signal; it never affects correctness.
const claudeSubObserverPoll = 200 * time.Millisecond

// claudeSubStderrTail is a bounded writer that keeps only the last max bytes.
type claudeSubStderrTail struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newClaudeSubStderrTail(max int) *claudeSubStderrTail {
	return &claudeSubStderrTail{max: max}
}

func (t *claudeSubStderrTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(p) >= t.max {
		t.buf = append(t.buf[:0], p[len(p)-t.max:]...)
		return len(p), nil
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-t.max:]...)
	}
	return len(p), nil
}

func (t *claudeSubStderrTail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// claudeSubExitObserver reports that a process has exited without reaping it,
// so the coordinator can still terminate the tree and then reap exactly once.
// Platform files implement it (pidfd on Linux/Android, kqueue EVFILT_PROC on
// the BSDs, WaitForSingleObject on Windows).
type claudeSubExitObserver interface {
	// exited is closed once the observed process has exited.
	exited() <-chan struct{}
	// close releases the observer. It is safe to call more than once.
	close()
}

// claudeSubChild is a launched child process owned by the lifecycle
// coordinator. Platform launch files implement it. Only the coordinator calls
// terminateTree and wait, exactly once each and in that order.
type claudeSubChild interface {
	// exited reports a non-reaping observation of the child's exit.
	exited() <-chan struct{}
	// terminateTree kills the child's whole process tree. It is safe while the
	// child is unreaped and must never be called after wait. ESRCH-equivalent
	// "nothing left" outcomes are success; any other error is a terminal
	// cleanup error.
	terminateTree() error
	// wait reaps the child and reports its exit.
	wait() error
	// close releases platform resources after wait.
	close()
}

// claudeSubLaunchSpec describes one child launch. The pipe fields are the
// child-side ends the parent hands to the platform launcher; the parent closes
// its own copies right after launch returns.
type claudeSubLaunchSpec struct {
	Path   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

// claudeSubLauncher starts a child process from spec. Platform files assign the
// real launcher; tests replace it to drive the coordinator with a fake child.
type claudeSubLauncher func(spec claudeSubLaunchSpec) (claudeSubChild, error)

// claudeSubProcess is the production claudeSubConn. It owns both ends of the
// child's stdin/stdout/stderr pipes, so a forced shutdown can close the read
// handles directly instead of waiting for pipe EOF, and it never depends on
// pipe EOF to reap the child. Four goroutines run per process: a stdout reader,
// a stderr drainer, a writer and the exit observer. One coordinator (started
// once) is the only code that terminates the tree and reaps the child.
type claudeSubProcess struct {
	child claudeSubChild

	stdin  *os.File // stdin write end owned by us
	stdout *os.File // stdout read end owned by us
	stderr *os.File // stderr read end owned by us

	events     chan claudeSubEvent
	stderrTail *claudeSubStderrTail

	// grace is captured at spawn so the coordinator never reads the package
	// variable concurrently with a test that shortens it.
	grace time.Duration

	sendMu     sync.Mutex
	sendCond   *sync.Cond
	sendQueue  [][]byte
	sendClosed bool

	startOnce sync.Once
	started   chan struct{} // closed when the coordinator starts
	stopping  chan struct{} // closed when the coordinator stops the queues
	terminal  chan struct{} // closed when the coordinator finishes

	handlesOnce sync.Once

	readersLeft atomic.Int32
	readersDone chan struct{}
	writerDone  chan struct{}

	errMu      sync.Mutex
	err        error
	waitErr    error
	forcedErr  error
	scanErr    error
	writeErr   error
	cleanupErr error // terminal cleanup error, published before terminal closes
}

// spawnClaudeSubProcess starts path with args and env in dir. It deliberately
// does not use exec.CommandContext: the process outlives the request context
// and is stopped only by Close. It owns the pipes: it creates them, passes the
// child-side ends to the platform launcher and then closes its copies.
func spawnClaudeSubProcess(_ context.Context, path string, args, env []string, dir string) (claudeSubConn, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create claude CLI stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW)
		return nil, fmt.Errorf("create claude CLI stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW, stdoutR, stdoutW)
		return nil, fmt.Errorf("create claude CLI stderr pipe: %w", err)
	}

	child, err := claudeSubLaunch(claudeSubLaunchSpec{
		Path:   path,
		Args:   args,
		Env:    env,
		Dir:    dir,
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stderrW,
	})
	// The parent never needs the child-side ends, whether or not launch worked.
	closeFiles(stdinR, stdoutW, stderrW)
	if err != nil {
		closeFiles(stdinW, stdoutR, stderrR)
		return nil, err
	}

	p := &claudeSubProcess{
		child:       child,
		stdin:       stdinW,
		stdout:      stdoutR,
		stderr:      stderrR,
		events:      make(chan claudeSubEvent, claudeSubEventBuffer),
		stderrTail:  newClaudeSubStderrTail(claudeSubStderrTailBytes),
		grace:       claudeSubCloseGrace,
		started:     make(chan struct{}),
		stopping:    make(chan struct{}),
		terminal:    make(chan struct{}),
		readersDone: make(chan struct{}),
		writerDone:  make(chan struct{}),
	}
	p.readersLeft.Store(2)
	p.sendCond = sync.NewCond(&p.sendMu)

	go p.readStdout()
	go p.drainStderr()
	go p.writeLoop()
	go p.observeExit()
	return p, nil
}

func closeFiles(files ...*os.File) {
	for _, f := range files {
		if f != nil {
			_ = f.Close()
		}
	}
}

// Send enqueues line for the CLI's stdin. The queue is unbounded and serviced by
// writeLoop, so a full stdin pipe blocks only the writer goroutine, never the
// caller.
func (p *claudeSubProcess) Send(line []byte) error {
	buf := make([]byte, 0, len(line)+1)
	buf = append(buf, line...)
	buf = append(buf, '\n')

	p.sendMu.Lock()
	if p.sendClosed {
		p.sendMu.Unlock()
		return errors.New("claude CLI connection is closed")
	}
	p.sendQueue = append(p.sendQueue, buf)
	p.sendMu.Unlock()
	p.sendCond.Signal()
	return nil
}

func (p *claudeSubProcess) Events() <-chan claudeSubEvent { return p.events }

func (p *claudeSubProcess) Err() error {
	p.errMu.Lock()
	defer p.errMu.Unlock()
	return p.err
}

// Close starts the single lifecycle coordinator on the first call, then waits
// for the shared terminal cleanup result or ctx. It never lets one caller's
// deadline control another: an expired ctx returns a non-nil error while the
// coordinator keeps running, so a later Close still observes the terminal
// result. It returns the coordinator's terminal cleanup error (a failed tree
// termination) or nil.
func (p *claudeSubProcess) Close(ctx context.Context) error {
	p.startShutdown(nil)
	select {
	case <-p.terminal:
		p.errMu.Lock()
		defer p.errMu.Unlock()
		return p.cleanupErr
	case <-ctx.Done():
		return fmt.Errorf("claude CLI shutdown did not complete: %w", ctx.Err())
	}
}

// observeExit starts the coordinator when the child exits on its own, so a
// process that exits without Close still gets its tree terminated and is
// reaped. It returns once the coordinator has started.
func (p *claudeSubProcess) observeExit() {
	select {
	case <-p.child.exited():
		p.startShutdown(nil)
	case <-p.started:
	}
}

// startShutdown starts the lifecycle coordinator exactly once. cause is the
// root cause when shutdown was requested by a reader or writer failure; it is
// nil for a caller Close or a natural exit.
func (p *claudeSubProcess) startShutdown(cause error) {
	p.startOnce.Do(func() {
		if cause != nil {
			p.errMu.Lock()
			if p.forcedErr == nil {
				p.forcedErr = cause
			}
			p.errMu.Unlock()
		}
		close(p.started)
		go p.coordinator()
	})
}

// coordinator is the only code that terminates the process tree and reaps the
// child. It runs once: stop the queues, wait one grace interval for a natural
// exit, terminate the tree (always, exactly once, while the child is unreaped),
// drain and release the readers and writer, reap the child, then publish Err,
// Events and the terminal cleanup result in that order.
//
// The final wait cannot be interrupted by a context: if tree termination fails
// the child may never exit and this goroutine can outlive a caller's deadline.
// That OS-level limitation is why Close waits on ctx rather than forcing the
// wait to return.
func (p *claudeSubProcess) coordinator() {
	p.stopQueues()

	// Give a well-behaved CLI one grace interval to exit on stdin EOF. The
	// tree is terminated either way, so descendants are always cleaned up.
	select {
	case <-p.child.exited():
	case <-time.After(p.grace):
	}

	cleanupErr := p.child.terminateTree()
	p.errMu.Lock()
	p.cleanupErr = cleanupErr
	p.errMu.Unlock()

	// Let the readers drain to EOF, then force-close the owned read handles so
	// a descendant that escaped the tree cannot keep them blocked.
	select {
	case <-p.readersDone:
	case <-time.After(p.grace):
	}
	p.closeReadHandles()
	<-p.readersDone

	// The stdin close in stopQueues releases a blocked writer.
	<-p.writerDone

	waitErr := p.child.wait()
	p.errMu.Lock()
	p.waitErr = waitErr
	p.errMu.Unlock()

	p.finalizeErr()
	close(p.events)
	p.child.close()
	close(p.terminal)
}

// stopQueues stops the outbound queue and closes stdin so the writer is
// released and the CLI sees EOF. It runs once, from the coordinator.
func (p *claudeSubProcess) stopQueues() {
	p.sendMu.Lock()
	p.sendClosed = true
	p.sendQueue = nil
	p.sendCond.Broadcast()
	p.sendMu.Unlock()
	close(p.stopping)
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
}

// closeReadHandles closes the owned stdout/stderr read handles at most once.
// Closing them is always safe and releases readers blocked on a descendant-held
// pipe.
func (p *claudeSubProcess) closeReadHandles() {
	p.handlesOnce.Do(func() {
		closeFiles(p.stdout, p.stderr)
	})
}

// finalizeErr records the exit cause before Events closes, preferring the root
// cause we recorded (forced/scan/write) over the wait result.
func (p *claudeSubProcess) finalizeErr() {
	p.errMu.Lock()
	var err error
	switch {
	case p.forcedErr != nil:
		err = p.forcedErr
	case p.writeErr != nil:
		err = p.writeErr
	case p.scanErr != nil:
		err = p.scanErr
	case p.waitErr != nil:
		err = fmt.Errorf("claude CLI process exited: %w", p.waitErr)
	}
	if err != nil {
		if tail := strings.TrimSpace(p.stderrTail.String()); tail != "" {
			err = fmt.Errorf("%w (stderr: %s)", err, tail)
		}
	}
	p.err = err
	p.errMu.Unlock()
}

// readStdout scans JSON lines from the CLI and forwards decoded envelopes. It
// exits on EOF, on the reader stop signal, or when the owned read handle is
// closed by a forced shutdown. A scan failure requests coordinator shutdown
// rather than killing anything itself.
func (p *claudeSubProcess) readStdout() {
	defer p.readerFinished()
	scanner := bufio.NewScanner(p.stdout)
	scanner.Buffer(nil, claudeSubScannerMaxBytes)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		raw := make([]byte, len(line))
		copy(raw, line)

		var env struct {
			Type    string `json:"type"`
			Subtype string `json:"subtype"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			continue // not a decodable envelope; ignore the line
		}
		ev := claudeSubEvent{Type: env.Type, Subtype: env.Subtype, Raw: json.RawMessage(raw)}
		select {
		case p.events <- ev:
		case <-p.stopping:
			return
		}
	}
	err := scanner.Err()
	if err == nil || p.stoppingClosed() {
		return // clean EOF, or our own forced handle close
	}
	p.errMu.Lock()
	if p.scanErr == nil {
		p.scanErr = fmt.Errorf("read claude CLI stdout: %w", err)
	}
	cause := p.scanErr
	p.errMu.Unlock()
	p.startShutdown(cause)
}

func (p *claudeSubProcess) stoppingClosed() bool {
	select {
	case <-p.stopping:
		return true
	default:
		return false
	}
}

// drainStderr copies stderr into the bounded tail so the exit error can carry
// the CLI's last words.
func (p *claudeSubProcess) drainStderr() {
	defer p.readerFinished()
	_, _ = io.Copy(p.stderrTail, p.stderr)
}

func (p *claudeSubProcess) readerFinished() {
	if p.readersLeft.Add(-1) == 0 {
		close(p.readersDone)
	}
}

// writeLoop drains the outbound queue to stdin. A pipe-write failure requests
// coordinator shutdown rather than killing anything itself.
func (p *claudeSubProcess) writeLoop() {
	defer close(p.writerDone)
	for {
		p.sendMu.Lock()
		for len(p.sendQueue) == 0 && !p.sendClosed {
			p.sendCond.Wait()
		}
		if len(p.sendQueue) == 0 {
			p.sendMu.Unlock()
			return
		}
		buf := p.sendQueue[0]
		p.sendQueue = p.sendQueue[1:]
		p.sendMu.Unlock()

		if _, err := p.stdin.Write(buf); err != nil {
			if p.stoppingClosed() {
				return // our own forced stdin close, not a real failure
			}
			p.errMu.Lock()
			if p.writeErr == nil {
				p.writeErr = fmt.Errorf("write to claude CLI stdin: %w", err)
			}
			cause := p.writeErr
			p.errMu.Unlock()
			p.startShutdown(cause)
			return
		}
	}
}
