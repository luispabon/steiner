//go:build unix

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// claudeSubLaunch is the platform launcher used by spawnClaudeSubProcess. Tests
// replace it to drive the coordinator with a fake child.
var claudeSubLaunch claudeSubLauncher = launchClaudeSubChild

// launchClaudeSubChild starts the claude CLI in its own process group. The
// group lets the coordinator terminate the whole tree with one signal, and the
// group id is only ever signalled while the leader is unreaped.
//
// A non-reaping exit observer (pidfd or kqueue) is required: without it the
// coordinator could not learn the child exited without reaping it first, and
// reaping before tree termination would allow the PID/PGID to be reused. If the
// observer cannot be created, launch fails closed instead of running without it.
func launchClaudeSubChild(spec claudeSubLaunchSpec) (claudeSubChild, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	cmd.Stdin = spec.Stdin
	cmd.Stdout = spec.Stdout
	cmd.Stderr = spec.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude CLI: %w", err)
	}

	pgid := cmd.Process.Pid // Setpgid makes the child its own group leader
	observer, err := newClaudeSubExitObserver(cmd.Process.Pid)
	if err != nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		_ = cmd.Wait()
		return nil, err
	}
	return &claudeSubUnixChild{cmd: cmd, pgid: pgid, observer: observer}, nil
}

// claudeSubUnixChild is the Unix claudeSubChild. Its process group id is only
// valid while the leader is unreaped, which the coordinator guarantees by
// terminating the tree before its single wait.
type claudeSubUnixChild struct {
	cmd      *exec.Cmd
	pgid     int
	observer claudeSubExitObserver
}

func (c *claudeSubUnixChild) exited() <-chan struct{} { return c.observer.exited() }

// terminateTree SIGKILLs the child's process group. The coordinator calls it
// once, before wait and while the leader is unreaped, so the group id still
// refers to this tree. ESRCH (no processes left in the group) is success; any
// other signal error is a terminal cleanup error.
//
// Limitation: a descendant that deliberately starts a new session or process
// group (setsid/setpgid) escapes the group signal, exactly as it would escape
// any process-group cleanup. There is no portable Unix way to reap such an
// escaped daemon; the transport still owns and kills the group it created.
func (c *claudeSubUnixChild) terminateTree() error {
	if err := syscall.Kill(-c.pgid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate claude CLI process group: %w", err)
	}
	return nil
}

func (c *claudeSubUnixChild) wait() error { return c.cmd.Wait() }

func (c *claudeSubUnixChild) close() { c.observer.close() }

// claudeSubLocatorSysProcAttr groups a locator command like the connection
// process, so a cancelled locator can take its children down with it.
func claudeSubLocatorSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killClaudeSubLocator terminates a locator command's process group. It runs
// while the locator is unreaped (exec's cancel path), so the group id is still
// valid. ESRCH is success.
func killClaudeSubLocator(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// claudeSubPrepareInheritance is a no-op outside Windows: Unix has no handle
// inheritance flag to manage, and file descriptors are not inherited unless
// explicitly duped into the child.
func claudeSubPrepareInheritance(...*os.File) error { return nil }

// claudeSubExecRunner runs one short-lived claude CLI subcommand for the
// locator. It groups the command like the connection process and bounds it with
// a finite WaitDelay and bounded output, so a descendant that holds the output
// pipe cannot hang discovery. It replaces the bare CommandContext.Output
// pattern.
func claudeSubExecRunner(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	// The locator gets the same credential-free environment as the CLI child, so
	// claude --version and auth status cannot pick up an inherited API key.
	cmd.Env = claudeSubChildEnv(os.Environ(), 0)
	cmd.SysProcAttr = claudeSubLocatorSysProcAttr()
	cmd.WaitDelay = claudeSubLocatorWaitDelay
	cmd.Cancel = func() error { return killClaudeSubLocator(cmd) }
	out := newClaudeSubBoundedBuffer(claudeSubLocatorOutputMaxBytes)
	errOut := newClaudeSubBoundedBuffer(claudeSubLocatorOutputMaxBytes)
	cmd.Stdout = out
	cmd.Stderr = errOut

	if err := cmd.Run(); err != nil {
		return nil, err
	}
	if out.overflowed() {
		return nil, fmt.Errorf("claude CLI %s produced more than %d bytes of output", path, claudeSubLocatorOutputMaxBytes)
	}
	return out.bytes(), nil
}
