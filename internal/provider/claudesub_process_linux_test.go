//go:build linux

package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// claudeSubHelperEnv builds a child environment with exactly one helper mode
// entry. os.Getenv returns the first match, so an inherited mode must be
// stripped before the override is appended or a grandchild would re-enter the
// parent's mode.
func claudeSubHelperEnv(mode, file string) []string {
	base := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if key == claudeSubHelperModeEnv || key == claudeSubHelperFileEnv {
			continue
		}
		base = append(base, kv)
	}
	base = append(base, claudeSubHelperModeEnv+"="+mode)
	if file != "" {
		base = append(base, claudeSubHelperFileEnv+"="+file)
	}
	return base
}

// TestClaudeSubLinuxHelperProcess is the entry point for the process-group
// helper modes. It does nothing in a normal run.
func TestClaudeSubLinuxHelperProcess(t *testing.T) {
	mode := os.Getenv(claudeSubHelperModeEnv)
	switch mode {
	case "":
		return
	case "grandchild-hold":
		// Same process group: the group kill must reach it.
		pid := spawnClaudeSubHelperChild(t, "hold-open", false)
		writeClaudeSubHelperFile(strconv.Itoa(pid))
		blockClaudeSubHelper()
	case "escape-hold", "locator-hold":
		// A new process group: it escapes the group kill and holds the pipe.
		pid := spawnClaudeSubHelperChild(t, "hold-open", true)
		writeClaudeSubHelperFile(strconv.Itoa(pid))
		os.Exit(0)
	case "locator-env":
		// Record which credential variables the locator child inherited.
		var seen []string
		for _, name := range claudeSubLocatorCredentialVars {
			if _, ok := os.LookupEnv(name); ok {
				seen = append(seen, name)
			}
		}
		if err := os.WriteFile(os.Getenv(claudeSubHelperFileEnv), []byte(strings.Join(seen, "\n")), 0o600); err != nil {
			t.Fatalf("write locator env report: %v", err)
		}
	default:
		t.Fatalf("unknown helper mode %q", mode)
	}
}

// spawnClaudeSubHelperChild starts another copy of this test binary in the
// hold-open mode. escape puts it in its own process group.
func spawnClaudeSubHelperChild(t *testing.T, mode string, escape bool) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestClaudeSubHelperProcess")
	cmd.Env = claudeSubHelperEnv(mode, "")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if escape {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn helper child: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	return pid
}

// TestClaudeSubProcessGroupGrandchildDiesOnClose proves the coordinator's one
// group signal reaches a same-group grandchild that holds the pipe.
func TestClaudeSubProcessGroupGrandchildDiesOnClose(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 300*time.Millisecond)

	pidFile := filepath.Join(t.TempDir(), "grandchild-pid")
	env := claudeSubHelperEnv("grandchild-hold", pidFile)
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubLinuxHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	claudeSubDrainEvents(conn)

	pid := claudeSubWaitPID(t, pidFile)
	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	claudeSubAssertDead(t, pid)
}

// TestClaudeSubProcessEscapedDescendantReleasesReaders proves Close releases the
// owned read handles even when a descendant that escaped the process group
// holds the stdout pipe, and still reaps the leader.
func TestClaudeSubProcessEscapedDescendantReleasesReaders(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	claudeSubShortGrace(t, 200*time.Millisecond)

	pidFile := filepath.Join(t.TempDir(), "escaped-pid")
	env := claudeSubHelperEnv("escape-hold", pidFile)
	conn, err := spawnClaudeSubProcess(context.Background(), os.Args[0], []string{"-test.run=TestClaudeSubLinuxHelperProcess"}, env, "")
	if err != nil {
		t.Fatalf("spawnClaudeSubProcess() error = %v", err)
	}
	proc, ok := conn.(*claudeSubProcess)
	if !ok {
		t.Fatalf("spawn returned %T, want *claudeSubProcess", conn)
	}
	claudeSubDrainEvents(conn)

	pid := claudeSubWaitPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	if err := conn.Close(context.Background()); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	select {
	case <-proc.readersDone:
	default:
		t.Error("readers still active after Close with an escaped descendant holding the pipe")
	}
}

// claudeSubLocatorCredentialVars are the credential variables steiner must not
// hand to the locator's claude subcommands.
var claudeSubLocatorCredentialVars = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN"}

// TestClaudeSubLocatorDropsCredentialEnv proves the locator child never inherits
// the credential variables, so claude --version and claude auth status cannot
// see or report an API key from steiner's environment.
func TestClaudeSubLocatorDropsCredentialEnv(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	for _, name := range claudeSubLocatorCredentialVars {
		t.Setenv(name, "secret-"+name)
	}
	reportFile := filepath.Join(t.TempDir(), "locator-env")
	t.Setenv(claudeSubHelperModeEnv, "locator-env")
	t.Setenv(claudeSubHelperFileEnv, reportFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := claudeSubExecRunner(ctx, os.Args[0], "-test.run=TestClaudeSubLinuxHelperProcess"); err != nil {
		t.Fatalf("claudeSubExecRunner() error = %v", err)
	}
	report, err := os.ReadFile(reportFile)
	if err != nil {
		t.Fatalf("read locator env report: %v (the locator helper did not run)", err)
	}
	if got := string(report); got != "" {
		t.Errorf("locator child inherited credential variables %q, want none", strings.Split(got, "\n"))
	}
}

// TestClaudeSubLocatorDescendantHeldOutputReleased proves the locator runner is
// bounded by its WaitDelay even when an escaped descendant holds its output
// pipe, rather than hanging on the inherited descriptor.
func TestClaudeSubLocatorDescendantHeldOutputReleased(t *testing.T) {
	if os.Getenv(claudeSubHelperModeEnv) != "" {
		t.Skip("helper process")
	}
	old := claudeSubLocatorWaitDelay
	claudeSubLocatorWaitDelay = 300 * time.Millisecond
	t.Cleanup(func() { claudeSubLocatorWaitDelay = old })

	pidFile := filepath.Join(t.TempDir(), "locator-pid")
	t.Setenv(claudeSubHelperModeEnv, "locator-hold")
	t.Setenv(claudeSubHelperFileEnv, pidFile)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := claudeSubExecRunner(ctx, os.Args[0], "-test.run=TestClaudeSubLinuxHelperProcess")
	elapsed := time.Since(start)

	if err == nil {
		t.Error("claudeSubExecRunner() error = nil, want the WaitDelay error")
	}
	if elapsed > 3*time.Second {
		t.Errorf("claudeSubExecRunner() took %s, want the WaitDelay bound", elapsed)
	}
	if pid, perr := claudeSubReadPID(pidFile); perr == nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}

func claudeSubWaitPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		pid, err := claudeSubReadPID(path)
		if err == nil {
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("read helper pid file: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func claudeSubReadPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(string(data))
}

func claudeSubAssertDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive (kill err = %v), want the group kill to reach it", pid, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
