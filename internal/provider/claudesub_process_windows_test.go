//go:build windows

package provider

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// claudeSubFakeAttrList captures the attribute values the launcher sets, so a
// test can inspect the job handle and the exact inheritable handle list rather
// than only the call order.
type claudeSubFakeAttrList struct {
	record   func(string)
	job      windows.Handle
	handles  []windows.Handle
	sentinel *windows.ProcThreadAttributeList
}

func (a *claudeSubFakeAttrList) setJob(job windows.Handle) error {
	a.record("setJobAttribute")
	a.job = job
	return nil
}

func (a *claudeSubFakeAttrList) setHandles(handles []windows.Handle) error {
	a.record("setHandleList")
	a.handles = append([]windows.Handle(nil), handles...)
	return nil
}

func (a *claudeSubFakeAttrList) list() *windows.ProcThreadAttributeList { return a.sentinel }

func (a *claudeSubFakeAttrList) delete() { a.record("deleteAttrList") }

// TestClaudeSubWindowsJobObjectOrder proves the Job Object ownership ordering
// and the exact handle-inheritance setup: the job is created and configured
// with KILL_ON_JOB_CLOSE, both attributes are attached to one list before
// CreateProcess, the handle list holds exactly the three child standard
// handles, the attribute list is wired into the StartupInfoEx, and shutdown is
// a single TerminateJobObject call with no PID-based taskkill.
func TestClaudeSubWindowsJobObjectOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	record := func(s string) {
		mu.Lock()
		calls = append(calls, s)
		mu.Unlock()
	}
	order := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		for i, c := range calls {
			if c == name {
				return i
			}
		}
		return -1
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	t.Cleanup(func() { closeFiles(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW) })

	wantHandles := []windows.Handle{
		windows.Handle(stdinR.Fd()),
		windows.Handle(stdoutW.Fd()),
		windows.Handle(stderrW.Fd()),
	}
	sentinel := new(windows.ProcThreadAttributeList)
	attrs := &claudeSubFakeAttrList{record: record, sentinel: sentinel}

	release := make(chan struct{})
	var capturedSI windows.StartupInfoEx
	var capturedInherit bool
	ops := claudeSubWinOps{
		createJobObject: func() (windows.Handle, error) { record("createJobObject"); return windows.Handle(1), nil },
		setJobLimit:     func(windows.Handle) error { record("setJobLimit"); return nil },
		newAttrList: func() (claudeSubWinAttrList, error) {
			record("newAttrList")
			return attrs, nil
		},
		setHandleInherit: func(windows.Handle, bool) error { record("setHandleInherit"); return nil },
		createProcess: func(_ *uint16, _ *uint16, _ string, si *windows.StartupInfoEx, inherit bool) (windows.ProcessInformation, error) {
			record("createProcess")
			capturedSI = *si
			capturedInherit = inherit
			return windows.ProcessInformation{Process: windows.Handle(2), Thread: windows.Handle(3)}, nil
		},
		terminateJob:    func(windows.Handle) error { record("terminateJob"); return nil },
		waitProcess:     func(windows.Handle) (uint32, error) { <-release; return windows.WAIT_OBJECT_0, nil },
		processExitCode: func(windows.Handle) (uint32, error) { record("processExitCode"); return 0, nil },
		closeHandle:     func(windows.Handle) { record("closeHandle") },
	}

	spec := claudeSubLaunchSpec{
		Path:   `C:\tools\claude.exe`,
		Args:   []string{"-p"},
		Env:    []string{"B=2", "A=1"},
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stderrW,
	}
	child, err := launchClaudeSubChildWith(spec, ops)
	if err != nil {
		t.Fatalf("launchClaudeSubChildWith() error = %v", err)
	}

	// Creation ordering: the job exists and is configured, both attributes are
	// attached, and only then is the process created.
	steps := []string{"createJobObject", "setJobLimit", "newAttrList", "setJobAttribute", "setHandleList", "createProcess"}
	last := -1
	for _, step := range steps {
		at := order(step)
		if at == -1 {
			t.Fatalf("calls = %v, missing %s", calls, step)
		}
		if at < last {
			t.Fatalf("calls = %v, %s is out of order", calls, step)
		}
		last = at
	}

	// Attribute values: the job handle and exactly the three child handles.
	if attrs.job != windows.Handle(1) {
		t.Errorf("job attribute = %v, want the created job handle 1", attrs.job)
	}
	if len(attrs.handles) != len(wantHandles) {
		t.Fatalf("handle list = %v, want exactly %v", attrs.handles, wantHandles)
	}
	for i, h := range wantHandles {
		if attrs.handles[i] != h {
			t.Errorf("handle list[%d] = %v, want %v", i, attrs.handles[i], h)
		}
	}

	// StartupInfoEx wiring: the attribute list, the three standard handles and
	// the extended struct size.
	if capturedSI.ProcThreadAttributeList != sentinel {
		t.Errorf("StartupInfoEx.ProcThreadAttributeList = %v, want the populated list", capturedSI.ProcThreadAttributeList)
	}
	if capturedSI.StdInput != wantHandles[0] || capturedSI.StdOutput != wantHandles[1] || capturedSI.StdErr != wantHandles[2] {
		t.Errorf("StartupInfoEx std handles = (%v,%v,%v), want %v", capturedSI.StdInput, capturedSI.StdOutput, capturedSI.StdErr, wantHandles)
	}
	if want := uint32(unsafe.Sizeof(windows.StartupInfoEx{})); capturedSI.Cb != want {
		t.Errorf("StartupInfoEx.Cb = %d, want %d", capturedSI.Cb, want)
	}
	if !capturedInherit {
		t.Error("CreateProcess called with inherit = false, want true for the handle list")
	}

	// Shutdown ordering: TerminateJobObject before the single wait/exit-code read.
	if err := child.terminateTree(); err != nil {
		t.Fatalf("terminateTree() error = %v", err)
	}
	close(release)
	if err := child.wait(); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
	child.close()

	if order("terminateJob") == -1 {
		t.Fatalf("calls = %v, want a TerminateJobObject call", calls)
	}
	if order("terminateJob") > order("processExitCode") {
		t.Fatalf("calls = %v, want terminateJob before the reaping wait", calls)
	}
	for _, c := range calls {
		if strings.Contains(strings.ToLower(c), "taskkill") || strings.Contains(strings.ToLower(c), "terminateprocess") {
			t.Fatalf("calls = %v, want no PID-based taskkill", calls)
		}
	}
}

// TestClaudeSubWindowsRejectsCmdShim proves a .cmd/.bat shim is refused at
// startup rather than launched unsafely.
func TestClaudeSubWindowsRejectsCmdShim(t *testing.T) {
	_, err := launchClaudeSubChildWith(claudeSubLaunchSpec{Path: `C:\tools\claude.cmd`}, claudeSubWindowsOps)
	if err == nil || !strings.Contains(err.Error(), "shim") {
		t.Fatalf("error = %v, want a shim startup error", err)
	}
}

// TestClaudeSubWindowsLocatorUsesJobObject proves the Windows locator owns its
// tree through the same Job Object launcher: cancellation terminates the tree
// (terminateTree before the single wait) and there is no direct Process.Kill or
// taskkill path.
func TestClaudeSubWindowsLocatorUsesJobObject(t *testing.T) {
	child := newClaudeSubFakeChild()
	old := claudeSubLocatorLaunch
	claudeSubLocatorLaunch = func(claudeSubLaunchSpec) (claudeSubChild, error) { return child, nil }
	t.Cleanup(func() { claudeSubLocatorLaunch = old })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := claudeSubExecRunner(ctx, `C:\tools\claude.exe`, "--version")
		done <- err
	}()

	// Let the runner reach the exit wait, then cancel it.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("claudeSubExecRunner() error = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("locator runner did not return after cancellation")
	}

	calls := child.calls()
	terminateAt, waitAt := -1, -1
	for i, c := range calls {
		switch c {
		case "terminate":
			if terminateAt == -1 {
				terminateAt = i
			}
		case "wait":
			waitAt = i
		case "kill":
			t.Fatalf("calls = %v, want no direct process kill on Windows", calls)
		}
	}
	if terminateAt == -1 || waitAt == -1 {
		t.Fatalf("calls = %v, want terminate and wait", calls)
	}
	if terminateAt > waitAt {
		t.Errorf("calls = %v, want terminate before wait", calls)
	}
}

// TestClaudeSubWindowsLocatorPassesChildEnv proves the locator launches the CLI
// with the steiner-built environment. A nil Env would give CreateProcess an
// empty block, and an inherited environment would forward credentials.
func TestClaudeSubWindowsLocatorPassesChildEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	var launched claudeSubLaunchSpec
	old := claudeSubLocatorLaunch
	claudeSubLocatorLaunch = func(spec claudeSubLaunchSpec) (claudeSubChild, error) {
		launched = spec
		return nil, errors.New("launch stopped by test")
	}
	t.Cleanup(func() { claudeSubLocatorLaunch = old })

	if _, err := claudeSubExecRunner(context.Background(), `C:\tools\claude.exe`, "--version"); err == nil {
		t.Fatal("claudeSubExecRunner() error = nil, want the launch error")
	}
	if len(launched.Env) == 0 {
		t.Fatal("locator launch Env is empty, want the steiner child environment")
	}
	sawForced := false
	for _, kv := range launched.Env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(key, "ANTHROPIC_API_KEY") {
			t.Errorf("locator launch env carries %q, want it stripped", key)
		}
		if kv == "CLAUDE_CODE_DISABLE_FAST_MODE=1" {
			sawForced = true
		}
	}
	if !sawForced {
		t.Error("locator launch env lacks CLAUDE_CODE_DISABLE_FAST_MODE=1")
	}
}

// TestClaudeSubWindowsHelperLifecycle exercises the full Windows transport with
// the shared helper process. It is compile-checked locally and runs on Windows.
func TestClaudeSubWindowsHelperLifecycle(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 300*time.Millisecond)

	env := append(os.Environ(), claudeSubHelperModeEnv+"=stream-ignore-eof")
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	claudeSubDrainEvents(conn)
	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestClaudeSubWindowsInheritCleanupOnFailure proves that when marking the
// child standard handles inheritable fails part-way through, every handle
// already marked has its inherit flag cleared before the launcher returns, so
// no unintended transport handle is left inheritable for a concurrent launch.
func TestClaudeSubWindowsInheritCleanupOnFailure(t *testing.T) {
	cases := []struct {
		name   string
		failOn int // 1-based index of the setHandleInherit(true) call that fails
	}{
		{name: "second handle fails", failOn: 2},
		{name: "third handle fails", failOn: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdinR, stdinW, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe() error = %v", err)
			}
			stdoutR, stdoutW, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe() error = %v", err)
			}
			stderrR, stderrW, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe() error = %v", err)
			}
			t.Cleanup(func() { closeFiles(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW) })

			var marked []windows.Handle
			var cleared []windows.Handle
			setCalls := 0
			jobClosed := false
			ops := claudeSubWinOps{
				createJobObject: func() (windows.Handle, error) { return windows.Handle(1), nil },
				setJobLimit:     func(windows.Handle) error { return nil },
				newAttrList: func() (claudeSubWinAttrList, error) {
					return &claudeSubFakeAttrList{record: func(string) {}, sentinel: new(windows.ProcThreadAttributeList)}, nil
				},
				setHandleInherit: func(h windows.Handle, inherit bool) error {
					if !inherit {
						cleared = append(cleared, h)
						return nil
					}
					setCalls++
					if setCalls == tc.failOn {
						return errors.New("injected SetHandleInformation failure")
					}
					marked = append(marked, h)
					return nil
				},
				createProcess: func(*uint16, *uint16, string, *windows.StartupInfoEx, bool) (windows.ProcessInformation, error) {
					t.Fatalf("createProcess called after an inheritance failure")
					return windows.ProcessInformation{}, nil
				},
				closeHandle: func(windows.Handle) { jobClosed = true },
			}

			spec := claudeSubLaunchSpec{
				Path:   `C:\tools\claude.exe`,
				Stdin:  stdinR,
				Stdout: stdoutW,
				Stderr: stderrW,
			}
			if _, err := launchClaudeSubChildWith(spec, ops); err == nil {
				t.Fatal("launchClaudeSubChildWith() error = nil, want the injected failure")
			}

			// Only the handles marked before the failure may be marked, and each
			// must have been cleared again before the launcher returned.
			if want := tc.failOn - 1; len(marked) != want {
				t.Fatalf("marked handles = %v, want %d", marked, want)
			}
			if len(cleared) != len(marked) {
				t.Fatalf("cleared handles = %v, want exactly the %d marked handles", cleared, len(marked))
			}
			for i, h := range marked {
				if cleared[i] != h {
					t.Errorf("cleared[%d] = %v, want the marked handle %v", i, cleared[i], h)
				}
			}
			if !jobClosed {
				t.Error("job object not closed after an inheritance failure")
			}
		})
	}
}
