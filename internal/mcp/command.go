package mcp

import (
	"context"
	"io"
	"os"
	"os/exec"
)

// buildCommand constructs the stdio command for an MCP server.
//
// The construction order is load-bearing:
//  1. cmd.Stdin and cmd.Stdout must stay nil: the SDK's StdinPipe and StdoutPipe
//     fail with "exec: Stdout already set" if either is already set.
//  2. The wrap may return a fresh exec.Cmd with no context (the sandbox wrapper
//     builds a struct literal); it is rebuilt with CommandContext so exec.Start
//     accepts the non-nil Cancel set later.
//  3. applyProcessGroup must run on the command returned by wrap: the sandbox
//     wrapper (sandbox.WrapCommandMode) builds a fresh exec.Cmd and copies only
//     Path, Args, Stdin, Stdout, Stderr, Env, and ExtraFiles, discarding
//     SysProcAttr, Cancel, and WaitDelay. Reaping applied before the wrap
//     silently does nothing.
func buildCommand(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)

	env := os.Environ()
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	cmd.Stderr = stderr

	if wrap != nil {
		cmd = wrap(cmd)
		// The sandbox wrapper returns a fresh exec.Cmd built from a struct
		// literal, so it has no context. exec.Start rejects a non-nil Cancel on
		// such a command ("exec: command with a non-nil Cancel was not created
		// with CommandContext"), so rebuild it with CommandContext and copy the
		// wrapper's fields back onto the result.
		wrapped := cmd
		args := wrapped.Args
		if len(args) > 0 {
			args = args[1:]
		}
		cmd = exec.CommandContext(ctx, wrapped.Path, args...)
		cmd.Env = wrapped.Env
		cmd.Stdin = wrapped.Stdin
		cmd.Stdout = wrapped.Stdout
		cmd.Stderr = wrapped.Stderr
		cmd.ExtraFiles = wrapped.ExtraFiles
		cmd.Dir = wrapped.Dir
	}
	// applyProcessGroup must run AFTER wrap: WrapCommandMode constructs a fresh
	// exec.Cmd and discards SysProcAttr, Cancel, and WaitDelay, so reaping
	// applied before the wrap would silently do nothing.
	applyProcessGroup(cmd)

	return cmd
}
