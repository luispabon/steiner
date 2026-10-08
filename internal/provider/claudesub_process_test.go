package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	claudeSubHelperModeEnv = "STEINER_CLAUDESUB_TEST_HELPER"
	claudeSubHelperFileEnv = "STEINER_CLAUDESUB_TEST_FILE"
)

// TestClaudeSubHelperProcess is not a test: it is the entry point of the child
// process the lifecycle tests spawn, and does nothing in a normal run. The
// process-group helper modes live in claudesub_process_linux_test.go.
func TestClaudeSubHelperProcess(t *testing.T) {
	mode := os.Getenv(claudeSubHelperModeEnv)
	switch mode {
	case "":
		return
	case "exit":
		_, _ = io.WriteString(os.Stdout, `{"type":"system","subtype":"init"}`+"\n")
		os.Exit(0)
	case "stream-ignore-eof":
		_, _ = io.WriteString(os.Stdout, `{"type":"system","subtype":"init","model":"claude-haiku-5-5"}`+"\n")
		_, _ = io.WriteString(os.Stdout, `{"type":"control_response","response":{"subtype":"success","request_id":"steiner-1"}}`+"\n")
		// Drain stdin but ignore its EOF, so only Close can stop the process.
		_, _ = io.Copy(io.Discard, os.Stdin)
		writeClaudeSubHelperFile("eof")
		blockClaudeSubHelper()
	case "hold-open":
		// Never read stdin and keep stdout/stderr open, so the transport must
		// release its own goroutines on shutdown.
		blockClaudeSubHelper()
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

func blockClaudeSubHelper() {
	for {
		time.Sleep(time.Hour)
	}
}

func writeClaudeSubHelperFile(content string) {
	if path := os.Getenv(claudeSubHelperFileEnv); path != "" {
		_ = os.WriteFile(path, []byte(content), 0o600)
	}
}

// claudeSubFakeChild is a claudeSubChild for coordinator tests. It records the
// order in which terminateTree, wait and close are called and can block
// termination so tests can observe concurrent Close callers.
type claudeSubFakeChild struct {
	mu             sync.Mutex
	terminateErr   error
	waitErr        error
	terminateBlock chan struct{}
	exitedCh       chan struct{}
	exitedOnce     sync.Once
	callLog        []string
}

func newClaudeSubFakeChild() *claudeSubFakeChild {
	return &claudeSubFakeChild{exitedCh: make(chan struct{})}
}

func (c *claudeSubFakeChild) exited() <-chan struct{} { return c.exitedCh }

func (c *claudeSubFakeChild) terminateTree() error {
	c.record("terminate")
	if c.terminateBlock != nil {
		<-c.terminateBlock
	}
	return c.terminateErr
}

func (c *claudeSubFakeChild) wait() error {
	c.record("wait")
	return c.waitErr
}

func (c *claudeSubFakeChild) close() { c.record("close") }

func (c *claudeSubFakeChild) signalExit() { c.exitedOnce.Do(func() { close(c.exitedCh) }) }

func (c *claudeSubFakeChild) record(name string) {
	c.mu.Lock()
	c.callLog = append(c.callLog, name)
	c.mu.Unlock()
}

func (c *claudeSubFakeChild) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.callLog...)
}

// claudeSubUseFakeChild replaces the platform launcher with one returning child.
func claudeSubUseFakeChild(t *testing.T, child claudeSubChild) {
	t.Helper()
	old := claudeSubLaunch
	claudeSubLaunch = func(claudeSubLaunchSpec) (claudeSubChild, error) { return child, nil }
	t.Cleanup(func() { claudeSubLaunch = old })
}

// claudeSubShortGrace shortens the coordinator grace for a test.
func claudeSubShortGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := claudeSubCloseGrace
	claudeSubCloseGrace = d
	t.Cleanup(func() { claudeSubCloseGrace = old })
}

func claudeSubDrainEvents(conn claudeSubConn) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		for range conn.Events() {
		}
		close(done)
	}()
	return done
}

func claudeSubSpawnFake(t *testing.T, child claudeSubChild) *claudeSubProcess {
	t.Helper()
	claudeSubUseFakeChild(t, child)
	conn, err := spawnClaudeSubProcess(context.Background(), "unused", nil, nil, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	p, ok := conn.(*claudeSubProcess)
	if !ok {
		t.Fatalf("spawn returned %T, want *claudeSubProcess", conn)
	}
	return p
}

// TestClaudeSubProcessCoordinatorTerminatesBeforeWait proves the coordinator
// terminates the tree exactly once and only then reaps the child.
func TestClaudeSubProcessCoordinatorTerminatesBeforeWait(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	p := claudeSubSpawnFake(t, child)
	drained := claudeSubDrainEvents(p)

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v, want nil", err)
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("Events() did not close")
	}

	calls := child.calls()
	terminateAt, waitAt, closeAt := -1, -1, -1
	for i, c := range calls {
		switch c {
		case "terminate":
			if terminateAt == -1 {
				terminateAt = i
			}
		case "wait":
			waitAt = i
		case "close":
			closeAt = i
		}
	}
	if terminateAt == -1 || waitAt == -1 {
		t.Fatalf("child calls = %v, want terminate and wait", calls)
	}
	if terminateAt > waitAt {
		t.Errorf("child calls = %v, want terminate before wait", calls)
	}
	if closeAt < waitAt {
		t.Errorf("child calls = %v, want close after wait", calls)
	}
	if n := strings.Count(strings.Join(calls, ","), "terminate"); n != 1 {
		t.Errorf("terminate called %d times, want exactly once: %v", n, calls)
	}
	if n := strings.Count(strings.Join(calls, ","), "wait"); n != 1 {
		t.Errorf("wait called %d times, want exactly once: %v", n, calls)
	}
}

// TestClaudeSubProcessNaturalExitReleasesControl proves a child that exits on
// its own still gets its tree terminated and reaped without a caller Close.
func TestClaudeSubProcessNaturalExitReleasesControl(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	p := claudeSubSpawnFake(t, child)
	drained := claudeSubDrainEvents(p)

	child.signalExit()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		t.Fatal("Events() did not close after the child exited")
	}

	calls := child.calls()
	if len(calls) < 2 || calls[0] != "terminate" || calls[1] != "wait" {
		t.Errorf("child calls = %v, want terminate then wait after natural exit", calls)
	}
}

// TestClaudeSubProcessTreeTerminationFailureIsCleanupError proves a failed tree
// termination is surfaced as a terminal cleanup error.
func TestClaudeSubProcessTreeTerminationFailureIsCleanupError(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	child.terminateErr = errors.New("kill boom")
	p := claudeSubSpawnFake(t, child)
	claudeSubDrainEvents(p)

	err := p.Close(context.Background())
	if err == nil || !strings.Contains(err.Error(), "kill boom") {
		t.Fatalf("Close() error = %v, want the tree termination failure", err)
	}
}

// TestClaudeSubForceCloseSkipsBothGraces proves forced teardown skips both grace
// intervals. A graceful Close of the same child waits out the natural-exit grace
// and the reader-drain grace, so discovery could spend two grace periods past
// its reserve; ForceClose must not.
func TestClaudeSubForceCloseSkipsBothGraces(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, time.Second)

	env := append(os.Environ(), claudeSubHelperModeEnv+"=hold-open")
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}

	start := time.Now()
	if err := conn.ForceClose(context.Background()); err != nil {
		t.Errorf("ForceClose() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Errorf("ForceClose took %s; it must skip both grace intervals", elapsed)
	}
}

// TestClaudeSubForceCloseEscalatesRunningCoordinator proves forcing escalates an
// already-running graceful coordinator: a Close that has entered the
// natural-exit grace is cut short by a later ForceClose instead of waiting out
// the full grace.
func TestClaudeSubForceCloseEscalatesRunningCoordinator(t *testing.T) {
	claudeSubShortGrace(t, 30*time.Second)
	child := newClaudeSubFakeChild()
	p := claudeSubSpawnFake(t, child)
	claudeSubDrainEvents(p)

	graceful := make(chan error, 1)
	go func() { graceful <- p.Close(context.Background()) }()
	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not start")
	}
	time.Sleep(20 * time.Millisecond) // let the coordinator enter the grace wait

	start := time.Now()
	if err := p.ForceClose(context.Background()); err != nil {
		t.Fatalf("ForceClose() error = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("ForceClose took %s; escalation did not cut the grace short", elapsed)
	}
	select {
	case err := <-graceful:
		if err != nil {
			t.Errorf("graceful Close() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("graceful Close did not observe the terminal result")
	}
}

// TestClaudeSubProcessCloseCallersShareOneCoordinator proves an expired caller
// context does not control later callers: the coordinator keeps running and a
// later Close observes the terminal result.
func TestClaudeSubProcessCloseCallersShareOneCoordinator(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	child.terminateBlock = make(chan struct{})
	p := claudeSubSpawnFake(t, child)
	claudeSubDrainEvents(p)

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	first := make(chan error, 1)
	go func() { first <- p.Close(expired) }()

	select {
	case err := <-first:
		if err == nil {
			t.Error("expired-context Close returned nil, want a deadline error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expired-context Close did not return")
	}

	// The coordinator was still blocked in tree termination; a later caller with
	// its own context must observe the terminal result once it completes.
	later := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		later <- p.Close(ctx)
	}()
	close(child.terminateBlock)
	select {
	case err := <-later:
		if err != nil {
			t.Errorf("later Close() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("later Close did not observe the terminal result")
	}
}

// TestClaudeSubProcessConcurrentCloseCallers runs several Close callers with
// differing contexts at once and proves they all return and share one
// coordinator.
func TestClaudeSubProcessConcurrentCloseCallers(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	p := claudeSubSpawnFake(t, child)
	claudeSubDrainEvents(p)

	const callers = 6
	results := make(chan error, callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			if i%2 == 0 {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				results <- p.Close(ctx)
				return
			}
			expired, cancel := context.WithCancel(context.Background())
			cancel()
			results <- p.Close(expired)
		}(i)
	}
	for i := 0; i < callers; i++ {
		select {
		case <-results:
		case <-time.After(5 * time.Second):
			t.Fatal("a Close caller did not return")
		}
	}
	if n := strings.Count(strings.Join(child.calls(), ","), "terminate"); n != 1 {
		t.Errorf("terminate called %d times, want one coordinator: %v", n, child.calls())
	}
}

// TestClaudeSubProcessScannerFailureReachesCoordinator drives readStdout with an
// oversize line and proves the scan failure starts the coordinator (which
// terminates the tree) instead of killing anything directly.
func TestClaudeSubProcessScannerFailureReachesCoordinator(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	oldCeiling := claudeSubScannerMaxBytes
	claudeSubScannerMaxBytes = 16
	t.Cleanup(func() { claudeSubScannerMaxBytes = oldCeiling })

	child := newClaudeSubFakeChild()
	p, feed := newClaudeSubTestProcess(t, child)

	if _, err := feed.Write([]byte(`{"type":"assistant","message":"` + strings.Repeat("x", 64) + `"}` + "\n")); err != nil {
		t.Fatalf("feed write error = %v", err)
	}
	p.readStdout()

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cause := p.Err(); cause == nil || !strings.Contains(cause.Error(), "read claude CLI stdout") {
		t.Errorf("Err() = %v, want the scan failure", cause)
	}
	if n := strings.Count(strings.Join(child.calls(), ","), "terminate"); n != 1 {
		t.Errorf("terminate calls = %v, want one from the coordinator", child.calls())
	}
}

// TestClaudeSubProcessWriteFailureReachesCoordinator proves a stdin write
// failure starts the coordinator rather than killing anything directly.
func TestClaudeSubProcessWriteFailureReachesCoordinator(t *testing.T) {
	claudeSubShortGrace(t, 50*time.Millisecond)
	child := newClaudeSubFakeChild()
	p, _ := newClaudeSubTestProcess(t, child)

	// Close the write end so the writer's pipe write fails immediately.
	_ = p.stdin.Close()
	if err := p.Send([]byte(`{"type":"user"}`)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Wait for the write failure to start the coordinator before Close, so the
	// writer records its failure rather than seeing the coordinator's own stdin
	// close as the cause.
	select {
	case <-p.started:
	case <-time.After(2 * time.Second):
		t.Fatal("write failure did not start the coordinator")
	}

	if err := p.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cause := p.Err(); cause == nil || !strings.Contains(cause.Error(), "write to claude CLI stdin") {
		t.Errorf("Err() = %v, want the write failure", cause)
	}
	if n := strings.Count(strings.Join(child.calls(), ","), "terminate"); n != 1 {
		t.Errorf("terminate calls = %v, want one from the coordinator", child.calls())
	}
}

// newClaudeSubTestProcess builds a claudeSubProcess with an owned stdout pipe so
// a test can drive one reader directly. It returns the process and the stdout
// write end the test feeds.
func newClaudeSubTestProcess(t *testing.T, child claudeSubChild) (*claudeSubProcess, *os.File) {
	t.Helper()
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	t.Cleanup(func() { closeFiles(stdinR, stdinW, stdoutR, stdoutW) })

	p := &claudeSubProcess{
		child:       child,
		stdin:       stdinW,
		stdout:      stdoutR,
		events:      make(chan claudeSubEvent, 4),
		stderrTail:  newClaudeSubStderrTail(claudeSubStderrTailBytes),
		grace:       claudeSubCloseGrace,
		started:     make(chan struct{}),
		stopping:    make(chan struct{}),
		terminal:    make(chan struct{}),
		forced:      make(chan struct{}),
		readersDone: make(chan struct{}),
		writerDone:  make(chan struct{}),
	}
	// The manual process models readers that are already done: no reader
	// goroutine runs, so the coordinator's reader join must not block. The
	// writer runs so a Close can join it.
	close(p.readersDone)
	p.sendCond = sync.NewCond(&p.sendMu)
	go p.writeLoop()
	return p, stdoutW
}

func TestClaudeSubStderrTail(t *testing.T) {
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{name: "truncates to last bytes", writes: []string{"hello world"}, want: "lo world"},
		{name: "keeps latest across writes", writes: []string{"abc", "defghij"}, want: "cdefghij"},
		{name: "short input kept whole", writes: []string{"tiny"}, want: "tiny"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tail := newClaudeSubStderrTail(8)
			for _, w := range tc.writes {
				if _, err := tail.Write([]byte(w)); err != nil {
					t.Fatalf("Write(%q) error = %v", w, err)
				}
			}
			if got := tail.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestClaudeSubReadStdoutBounds exercises the JSONL reader's line ceiling
// directly, so the 64 MiB production limit does not need a 64 MiB fixture.
func TestClaudeSubReadStdoutBounds(t *testing.T) {
	line := `{"a":1}` // 7 bytes, valid JSON with no type field
	tests := []struct {
		name    string
		ceiling int
		want    int
		wantErr bool
	}{
		{name: "within ceiling", ceiling: 32, want: 1},
		{name: "over ceiling", ceiling: 4, want: 0, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			claudeSubShortGrace(t, 20*time.Millisecond)
			old := claudeSubScannerMaxBytes
			claudeSubScannerMaxBytes = tc.ceiling
			defer func() { claudeSubScannerMaxBytes = old }()

			p, feed := newClaudeSubTestProcess(t, newClaudeSubFakeChild())
			if _, err := feed.Write([]byte(line + "\n")); err != nil {
				t.Fatalf("feed write error = %v", err)
			}
			_ = feed.Close()
			p.readStdout()

			if got := len(p.events); got != tc.want {
				t.Errorf("events = %d, want %d", got, tc.want)
			}
			p.errMu.Lock()
			scanErr := p.scanErr
			p.errMu.Unlock()
			if (scanErr != nil) != tc.wantErr {
				t.Errorf("scanErr = %v, wantErr %v", scanErr, tc.wantErr)
			}
			if tc.wantErr {
				// Join the coordinator the scan failure started so no goroutine
				// outlives the test.
				if err := p.Close(context.Background()); err != nil {
					t.Fatalf("Close() error = %v", err)
				}
			}
		})
	}
}

func TestClaudeSubProcessLifecycle(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 500*time.Millisecond)

	eofFile := filepath.Join(t.TempDir(), "stdin-eof")
	env := append(os.Environ(), claudeSubHelperModeEnv+"=stream-ignore-eof", claudeSubHelperFileEnv+"="+eofFile)

	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}

	events := claudeSubReadEvents(t, conn, 2, 5*time.Second)
	if events[0].Type != "system" || events[0].Subtype != "init" {
		t.Errorf("event[0] = %+v, want system/init", events[0])
	}
	if events[1].Type != "control_response" || events[1].Subtype != "" {
		t.Errorf("event[1] = %+v, want control_response", events[1])
	}

	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("second Close() error = %v, want nil (idempotent)", err)
	}
	if cause := conn.Err(); cause == nil {
		t.Error("Err() = nil, want the kill cause after stopping a process that ignored stdin EOF")
	}
	if _, err := os.Stat(eofFile); err != nil {
		t.Errorf("helper did not observe stdin EOF before being killed: %v", err)
	}
	select {
	case _, ok := <-conn.Events():
		if ok {
			t.Error("Events() yielded an event after close")
		}
	case <-time.After(time.Second):
		t.Error("Events() was not closed by Close")
	}
}

func TestClaudeSubProcessConcurrentSendClose(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 200*time.Millisecond)

	env := append(os.Environ(), claudeSubHelperModeEnv+"=stream-ignore-eof")
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = conn.Send([]byte(`{"type":"user","message":{"role":"user","content":[]}}`))
			}
		}()
	}
	time.Sleep(5 * time.Millisecond)
	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	wg.Wait()
	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
	if cause := conn.Err(); cause == nil {
		t.Error("Err() = nil, want a kill cause")
	}
}

// TestClaudeSubProcessNaturalExitStopsWriter proves that when the process exits
// on its own, the coordinator stops writeLoop, so the writer goroutine does not
// leak without a caller Close, and later sends are rejected.
func TestClaudeSubProcessNaturalExitStopsWriter(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 50*time.Millisecond)

	env := append(os.Environ(), claudeSubHelperModeEnv+"=exit")
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	proc, ok := conn.(*claudeSubProcess)
	if !ok {
		t.Fatalf("spawn returned %T, want *claudeSubProcess", conn)
	}

	drained := claudeSubDrainEvents(conn)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("Events() did not close after the helper exited")
	}

	select {
	case <-proc.writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not terminate after natural process exit")
	}
	if err := conn.Send([]byte(`{"type":"user"}`)); err == nil {
		t.Error("Send() after the process exited = nil, want an error")
	}
}

// TestClaudeSubProcessCloseReleasesBlockedWriterAndReaders leaves the child's
// stdout/stderr open and blocks the writer on a full stdin pipe, then asserts
// Close itself releases the readers and writer before returning. The test never
// closes a pipe itself.
func TestClaudeSubProcessCloseReleasesBlockedWriterAndReaders(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 200*time.Millisecond)

	env := append(os.Environ(), claudeSubHelperModeEnv+"=hold-open")
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	proc, ok := conn.(*claudeSubProcess)
	if !ok {
		t.Fatalf("spawn returned %T, want *claudeSubProcess", conn)
	}

	// The child never reads stdin, so a large line blocks the writer in its pipe
	// write; the child's stdout/stderr stay open.
	if err := conn.Send([]byte(`{"type":"user","message":"` + strings.Repeat("x", 1<<20) + `"}`)); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let the writer reach the blocked write

	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	select {
	case <-proc.writerDone:
	default:
		t.Error("writer still active after Close returned")
	}
	select {
	case <-proc.readersDone:
	default:
		t.Error("readers still active after Close returned")
	}
}

func claudeSubReadEvents(t *testing.T, conn claudeSubConn, n int, timeout time.Duration) []claudeSubEvent {
	t.Helper()
	deadline := time.After(timeout)
	events := make([]claudeSubEvent, 0, n)
	for len(events) < n {
		select {
		case ev, ok := <-conn.Events():
			if !ok {
				t.Fatalf("Events() closed after %d events, want %d", len(events), n)
			}
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("timed out after %d events, want %d", len(events), n)
		}
	}
	return events
}
