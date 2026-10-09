//go:build windows

package provider

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// PROC_THREAD_ATTRIBUTE_JOB_LIST and PROC_THREAD_ATTRIBUTE_HANDLE_LIST (winnt.h).
// The job list makes the child a member of the job atomically at creation, so
// TerminateJobObject always reaches it and every descendant it spawns. The
// handle list restricts inheritance to exactly the three child standard handles,
// so the transport-owned parent ends can never leak into the child.
const (
	procThreadAttributeJobList    = 0x0002000D
	procThreadAttributeHandleList = 0x00020002
)

// claudeSubWinAttrList is the process attribute list the launcher populates with
// the job handle and the inheritable handle list. It is an interface so tests
// can inspect the captured attribute values, not just the call order.
type claudeSubWinAttrList interface {
	setJob(job windows.Handle) error
	setHandles(handles []windows.Handle) error
	list() *windows.ProcThreadAttributeList
	delete()
}

// claudeSubWinOps are the Windows API calls the launcher makes. Tests replace
// them to prove the Job Object ownership ordering without a Windows host.
type claudeSubWinOps struct {
	createJobObject  func() (windows.Handle, error)
	setJobLimit      func(job windows.Handle) error
	newAttrList      func() (claudeSubWinAttrList, error)
	setHandleInherit func(h windows.Handle, inherit bool) error
	createProcess    func(cmdLine, env *uint16, dir string, si *windows.StartupInfoEx, inherit bool) (windows.ProcessInformation, error)
	terminateJob     func(job windows.Handle) error
	waitProcess      func(proc windows.Handle) (uint32, error)
	processExitCode  func(proc windows.Handle) (uint32, error)
	closeHandle      func(h windows.Handle)
}

var claudeSubWindowsOps = claudeSubWinOps{
	createJobObject: func() (windows.Handle, error) { return windows.CreateJobObject(nil, nil) },
	setJobLimit:     setClaudeSubJobKillOnClose,
	newAttrList:     newClaudeSubWinAttrList,
	setHandleInherit: func(h windows.Handle, inherit bool) error {
		var flags uint32
		if inherit {
			flags = windows.HANDLE_FLAG_INHERIT
		}
		return windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, flags)
	},
	createProcess: createClaudeSubProcess,
	terminateJob:  func(job windows.Handle) error { return windows.TerminateJobObject(job, 1) },
	waitProcess:   func(proc windows.Handle) (uint32, error) { return windows.WaitForSingleObject(proc, windows.INFINITE) },
	processExitCode: func(proc windows.Handle) (uint32, error) {
		var code uint32
		err := windows.GetExitCodeProcess(proc, &code)
		return code, err
	},
	closeHandle: func(h windows.Handle) { _ = windows.CloseHandle(h) },
}

// claudeSubLaunch is the platform launcher used by spawnClaudeSubProcess. Tests
// replace it to drive the coordinator with a fake child.
var claudeSubLaunch claudeSubLauncher = launchClaudeSubChild

func launchClaudeSubChild(spec claudeSubLaunchSpec) (claudeSubChild, error) {
	return launchClaudeSubChildWith(spec, claudeSubWindowsOps)
}

// launchClaudeSubChildWith creates a job object that kills its members on close,
// attaches it to the process at creation through PROC_THREAD_ATTRIBUTE_JOB_LIST,
// and starts the child with CreateProcess. Membership therefore exists before
// the child can spawn anything, and shutdown is one TerminateJobObject call.
// There is no PID-based taskkill and no exec.Cmd fallback.
func launchClaudeSubChildWith(spec claudeSubLaunchSpec, ops claudeSubWinOps) (claudeSubChild, error) {
	if ext := strings.ToLower(filepath.Ext(spec.Path)); ext == ".cmd" || ext == ".bat" {
		return nil, fmt.Errorf("claude CLI at %s is a %s shim, which cannot be launched safely: install the native claude executable", spec.Path, ext)
	}

	job, err := ops.createJobObject()
	if err != nil {
		return nil, fmt.Errorf("create claude CLI job object: %w", err)
	}
	if err := ops.setJobLimit(job); err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("configure claude CLI job object: %w", err)
	}

	attrs, err := ops.newAttrList()
	if err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("create claude CLI process attribute list: %w", err)
	}
	defer attrs.delete()
	if err := attrs.setJob(job); err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("attach claude CLI job object attribute: %w", err)
	}

	// Only these three child ends may be inherited. Mark them inheritable, put
	// exactly them in the handle list, and clear the flag again once the child
	// has inherited them.
	stdHandles := []windows.Handle{
		windows.Handle(spec.Stdin.Fd()),
		windows.Handle(spec.Stdout.Fd()),
		windows.Handle(spec.Stderr.Fd()),
	}
	// Track the handles actually marked, so every return path clears exactly
	// those. A failure part-way through the loop must not leave an earlier
	// child standard handle inheritable for a concurrent process launch to
	// inherit, and the flag is cleared again once the child has inherited them.
	var inheritable []windows.Handle
	defer func() {
		for _, h := range inheritable {
			_ = ops.setHandleInherit(h, false)
		}
	}()
	for _, h := range stdHandles {
		if err := ops.setHandleInherit(h, true); err != nil {
			ops.closeHandle(job)
			return nil, fmt.Errorf("make claude CLI standard handle inheritable: %w", err)
		}
		inheritable = append(inheritable, h)
	}
	if err := attrs.setHandles(stdHandles); err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("attach claude CLI handle list attribute: %w", err)
	}

	cmdLine, err := claudeSubWindowsCommandLine(spec.Path, spec.Args)
	if err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("build claude CLI command line: %w", err)
	}
	env, err := claudeSubWindowsEnvBlock(spec.Env)
	if err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("build claude CLI environment: %w", err)
	}

	var si windows.StartupInfoEx
	si.Cb = uint32(unsafe.Sizeof(si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput = stdHandles[0]
	si.StdOutput = stdHandles[1]
	si.StdErr = stdHandles[2]
	si.ProcThreadAttributeList = attrs.list()

	pi, err := ops.createProcess(cmdLine, env, spec.Dir, &si, true)
	if err != nil {
		ops.closeHandle(job)
		return nil, fmt.Errorf("start claude CLI: %w", err)
	}

	child := &claudeSubWindowsChild{
		ops:      ops,
		job:      job,
		proc:     pi.Process,
		thread:   pi.Thread,
		exitedCh: make(chan struct{}),
	}
	go child.observeExit()
	return child, nil
}

// claudeSubWindowsChild is the Windows claudeSubChild. Its job object owns the
// whole tree for the life of the process, so termination never depends on a PID.
type claudeSubWindowsChild struct {
	ops    claudeSubWinOps
	job    windows.Handle
	proc   windows.Handle
	thread windows.Handle

	exitedCh  chan struct{}
	exitOnce  sync.Once
	closeOnce sync.Once
	waitOnce  sync.Once
	waitErr   error
}

func (c *claudeSubWindowsChild) exited() <-chan struct{} { return c.exitedCh }

// observeExit signals a non-reaping exit observation: waiting on the process
// handle never reaps the process, which stays valid until the handle is closed.
func (c *claudeSubWindowsChild) observeExit() {
	if _, err := c.ops.waitProcess(c.proc); err != nil {
		return
	}
	c.exitOnce.Do(func() { close(c.exitedCh) })
}

// terminateTree terminates every process in the job object. It is safe before
// wait and reaches descendants that are still job members.
func (c *claudeSubWindowsChild) terminateTree() error {
	if err := c.ops.terminateJob(c.job); err != nil {
		return fmt.Errorf("terminate claude CLI job object: %w", err)
	}
	return nil
}

// wait reaps the child: wait for exit, read the exit code, then release the
// process and thread handles.
func (c *claudeSubWindowsChild) wait() error {
	c.waitOnce.Do(func() {
		if _, err := c.ops.waitProcess(c.proc); err != nil {
			c.waitErr = fmt.Errorf("wait for claude CLI: %w", err)
			return
		}
		code, err := c.ops.processExitCode(c.proc)
		if err != nil {
			c.waitErr = fmt.Errorf("read claude CLI exit code: %w", err)
			return
		}
		if code != 0 {
			c.waitErr = fmt.Errorf("claude CLI process exited: exit status 0x%x", code)
		}
	})
	return c.waitErr
}

// close releases the handles. Closing the job handle also kills any stragglers,
// because the job was created with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
func (c *claudeSubWindowsChild) close() {
	c.closeOnce.Do(func() {
		c.ops.closeHandle(c.thread)
		c.ops.closeHandle(c.proc)
		c.ops.closeHandle(c.job)
	})
}

func setClaudeSubJobKillOnClose(job windows.Handle) error {
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	return err
}

type claudeSubRealAttrList struct {
	c *windows.ProcThreadAttributeListContainer
}

func newClaudeSubWinAttrList() (claudeSubWinAttrList, error) {
	// Two attributes: the job list and the handle list.
	c, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return nil, err
	}
	return &claudeSubRealAttrList{c: c}, nil
}

func (a *claudeSubRealAttrList) setJob(job windows.Handle) error {
	return a.c.Update(procThreadAttributeJobList, unsafe.Pointer(&job), unsafe.Sizeof(job))
}

func (a *claudeSubRealAttrList) setHandles(handles []windows.Handle) error {
	if len(handles) == 0 {
		return fmt.Errorf("claude CLI handle list is empty")
	}
	return a.c.Update(
		procThreadAttributeHandleList,
		unsafe.Pointer(&handles[0]),
		unsafe.Sizeof(handles[0])*uintptr(len(handles)),
	)
}

func (a *claudeSubRealAttrList) list() *windows.ProcThreadAttributeList { return a.c.List() }

func (a *claudeSubRealAttrList) delete() { a.c.Delete() }

func createClaudeSubProcess(cmdLine, env *uint16, dir string, si *windows.StartupInfoEx, inherit bool) (windows.ProcessInformation, error) {
	var pi windows.ProcessInformation
	var dirPtr *uint16
	if dir != "" {
		p, err := windows.UTF16PtrFromString(dir)
		if err != nil {
			return pi, err
		}
		dirPtr = p
	}
	flags := uint32(windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	err := windows.CreateProcess(nil, cmdLine, nil, nil, inherit, flags, env, dirPtr, &si.StartupInfo, &pi)
	if err != nil {
		return pi, err
	}
	return pi, nil
}

// claudeSubWindowsCommandLine quotes path and args into one CreateProcess
// command line.
func claudeSubWindowsCommandLine(path string, args []string) (*uint16, error) {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, syscall.EscapeArg(path))
	for _, a := range args {
		parts = append(parts, syscall.EscapeArg(a))
	}
	return windows.UTF16PtrFromString(strings.Join(parts, " "))
}

// claudeSubWindowsEnvBlock builds a sorted, double-NUL-terminated UTF-16
// environment block for CreateProcess.
func claudeSubWindowsEnvBlock(env []string) (*uint16, error) {
	entries := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.IndexByte(kv, 0) >= 0 {
			return nil, fmt.Errorf("environment entry contains a NUL byte")
		}
		entries = append(entries, kv)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.ToLower(entries[i]) < strings.ToLower(entries[j])
	})
	block := make([]uint16, 0, len(entries)*8)
	for _, kv := range entries {
		block = append(block, utf16.Encode([]rune(kv))...)
		block = append(block, 0)
	}
	if len(entries) == 0 {
		// An empty environment block is two NULs; a lone terminator is malformed.
		block = append(block, 0)
	}
	block = append(block, 0)
	return &block[0], nil
}

// claudeSubPrepareInheritance clears the inherit flag on the transport-owned
// pipe ends, so they can never be inherited by a child. It runs before the
// launcher and fails closed if a handle is invalid.
func claudeSubPrepareInheritance(files ...*os.File) error {
	for _, f := range files {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return fmt.Errorf("clear claude CLI pipe handle inheritance: %w", err)
		}
	}
	return nil
}

// claudeSubLocatorLaunch launches a locator command. Tests replace it to prove
// the locator uses Job Object ownership and never a direct process kill.
var claudeSubLocatorLaunch claudeSubLauncher = launchClaudeSubChild

// claudeSubExecRunner runs one short-lived claude CLI subcommand for the
// locator. On Windows it launches through the same Job Object ownership as the
// connection, so cancellation and the bounded drain terminate the whole tree
// with TerminateJobObject. There is no exec.Cmd Process.Kill path.
func claudeSubExecRunner(ctx context.Context, path string, args ...string) ([]byte, error) {
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW)
		return nil, err
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closeFiles(stdinR, stdinW, stdoutR, stdoutW)
		return nil, err
	}
	if err := claudeSubPrepareInheritance(stdinW, stdoutR, stderrR); err != nil {
		closeFiles(stdinR, stdinW, stdoutR, stdoutW, stderrR, stderrW)
		return nil, err
	}

	child, err := claudeSubLocatorLaunch(claudeSubLaunchSpec{
		Path:   path,
		Args:   args,
		Env:    claudeSubChildEnv(os.Environ(), 0),
		Stdin:  stdinR,
		Stdout: stdoutW,
		Stderr: stderrW,
	})
	closeFiles(stdinR, stdoutW, stderrW)
	if err != nil {
		closeFiles(stdinW, stdoutR, stderrR)
		return nil, err
	}
	_ = stdinW.Close() // the locator needs no stdin

	out := newClaudeSubBoundedBuffer(claudeSubLocatorOutputMaxBytes)
	var readers sync.WaitGroup
	readers.Add(2)
	readDone := make(chan struct{})
	go func() {
		defer readers.Done()
		_, _ = io.Copy(out, stdoutR)
	}()
	go func() {
		defer readers.Done()
		_, _ = io.Copy(io.Discard, stderrR)
	}()
	go func() {
		readers.Wait()
		close(readDone)
	}()

	// Wait for the locator to exit or for the caller to cancel it, then
	// terminate the whole job tree and reap, exactly as the connection
	// coordinator does.
	select {
	case <-child.exited():
	case <-ctx.Done():
	}
	cleanupErr := child.terminateTree()
	waitErr := child.wait()
	child.close()

	// A descendant that escaped the job and holds the pipe is bounded by the
	// locator WaitDelay, after which the owned read handles are force-closed.
	select {
	case <-readDone:
	case <-time.After(claudeSubLocatorWaitDelay):
	}
	closeFiles(stdoutR, stderrR)
	<-readDone

	if cleanupErr != nil {
		return nil, cleanupErr
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	if waitErr != nil {
		return nil, waitErr
	}
	if out.overflowed() {
		return nil, fmt.Errorf("claude CLI %s produced more than %d bytes of output", path, claudeSubLocatorOutputMaxBytes)
	}
	return out.bytes(), nil
}
