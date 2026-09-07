package lsp

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"go.lsp.dev/jsonrpc2"
)

// WrapFn wraps a server command before launch, e.g. inside the sandbox.
type WrapFn func(*exec.Cmd) *exec.Cmd

// TransportSpec specifies how to spawn and initialize a language server.
type TransportSpec struct {
	Command string
	Args    []string
	Env     []string
	// RootPath is the absolute workspace directory of the server.
	RootPath string
	// InitializationOptions is passed verbatim as the initialize request's
	// initializationOptions.
	InitializationOptions map[string]any
	Stderr                io.Writer
	// Wrap transforms the command before launch, e.g. inside the sandbox.
	Wrap WrapFn
}

// childProcess abstracts process lifecycle so session construction is testable
// without a real language server binary.
type childProcess interface {
	// Exited is closed once the process has exited.
	Exited() <-chan struct{}
	// Kill force-terminates the process. It is safe to call after Exited is closed.
	Kill()
}

// newTransport spawns a language server process and completes the handshake.
func newTransport(ctx context.Context, spec TransportSpec) (session, error) {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)
	cmd.Env = spec.Env
	if spec.Wrap != nil {
		cmd = spec.Wrap(cmd)
	}
	if spec.Stderr != nil {
		cmd.Stderr = spec.Stderr
	} else {
		cmd.Stderr = io.Discard
	}
	setProcessGroup(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start server: %w", err)
	}
	proc := newExecProcess(cmd)

	stream := jsonrpc2.NewStream(&readWriteCloser{r: stdout, w: stdin})

	s, err := newSession(ctx, stream, spec.RootPath, spec.InitializationOptions, proc)
	if err != nil {
		proc.Kill()
		return nil, err
	}
	return s, nil
}

// execProcess is the childProcess backed by a spawned command.
type execProcess struct {
	cmd    *exec.Cmd
	exited chan struct{}
	once   sync.Once
}

// newExecProcess starts reaping cmd; cmd must already have been started.
func newExecProcess(cmd *exec.Cmd) *execProcess {
	p := &execProcess{cmd: cmd, exited: make(chan struct{})}
	go func() {
		// Only the fact of the exit matters here: a server that dies reports its
		// reason on stderr, which the caller wires up through TransportSpec.
		_ = cmd.Wait()
		p.markExited()
	}()
	return p
}

func (p *execProcess) Exited() <-chan struct{} {
	return p.exited
}

func (p *execProcess) Kill() {
	// Best-effort: the process may already be gone, and there is no fallback
	// beyond signalling it once the graceful sequence has failed.
	_ = killProcess(p.cmd)
}

func (p *execProcess) markExited() {
	p.once.Do(func() { close(p.exited) })
}

// readWriteCloser adapts the process stdio pipes to io.ReadWriteCloser.
type readWriteCloser struct {
	r io.ReadCloser
	w io.WriteCloser
}

func (rwc *readWriteCloser) Read(b []byte) (int, error) {
	return rwc.r.Read(b)
}

func (rwc *readWriteCloser) Write(b []byte) (int, error) {
	return rwc.w.Write(b)
}

// Close closes both pipes, reporting the first failure.
func (rwc *readWriteCloser) Close() error {
	werr := rwc.w.Close()
	rerr := rwc.r.Close()
	if werr != nil {
		return fmt.Errorf("close stdin: %w", werr)
	}
	if rerr != nil {
		return fmt.Errorf("close stdout: %w", rerr)
	}
	return nil
}
