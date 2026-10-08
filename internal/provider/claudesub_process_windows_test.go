//go:build windows

package provider

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// claudeSubFakeAttrList records the attribute-list steps for the ordering test.
type claudeSubFakeAttrList struct{ record func(string) }

func (a claudeSubFakeAttrList) setJob(windows.Handle) error {
	a.record("setJobAttribute")
	return nil
}

func (a claudeSubFakeAttrList) list() *windows.ProcThreadAttributeList { return nil }

func (a claudeSubFakeAttrList) delete() { a.record("deleteAttrList") }

// TestClaudeSubWindowsJobObjectOrder proves the Job Object ownership ordering:
// the job is created and configured with KILL_ON_JOB_CLOSE, attached through a
// process attribute list before CreateProcess, and shutdown is a single
// TerminateJobObject call with no PID-based taskkill.
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

	release := make(chan struct{})
	ops := claudeSubWinOps{
		createJobObject: func() (windows.Handle, error) { record("createJobObject"); return windows.Handle(1), nil },
		setJobLimit:     func(windows.Handle) error { record("setJobLimit"); return nil },
		newAttrList: func() (claudeSubWinAttrList, error) {
			record("newAttrList")
			return claudeSubFakeAttrList{record: record}, nil
		},
		createProcess: func(*uint16, *uint16, string, *windows.StartupInfoEx, bool) (windows.ProcessInformation, error) {
			record("createProcess")
			return windows.ProcessInformation{Process: windows.Handle(2), Thread: windows.Handle(3)}, nil
		},
		terminateJob:    func(windows.Handle) error { record("terminateJob"); return nil },
		waitProcess:     func(windows.Handle) (uint32, error) { <-release; return windows.WAIT_OBJECT_0, nil },
		processExitCode: func(windows.Handle) (uint32, error) { record("processExitCode"); return 0, nil },
		closeHandle:     func(windows.Handle) { record("closeHandle") },
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

	// Creation ordering: the job exists and is configured, and its attribute is
	// attached, before the process is created.
	steps := []string{"createJobObject", "setJobLimit", "newAttrList", "setJobAttribute", "createProcess"}
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
