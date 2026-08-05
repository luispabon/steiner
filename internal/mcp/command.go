package mcp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"sort"
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
//
// spec.Env is declared config, not inherited host state, so it must not go
// through wrap's allowlist filtering: it is appended after the wrap, onto
// whatever env the wrapper produced from the host environment. The one
// exception is a sandbox control that removes a variable by name regardless
// of source (currently only DOCKER_HOST, via bwrap's --unsetenv in
// internal/sandbox/docker.go) — such a var is still removed even if it also
// appears in spec.Env, since bwrap applies --unsetenv after receiving the
// process environment.
func buildCommand(ctx context.Context, spec ServerSpec, wrap func(*exec.Cmd) *exec.Cmd, stderr io.Writer) *exec.Cmd {
	cmd := exec.CommandContext(ctx, spec.Command, spec.Args...)

	cmd.Env = os.Environ()
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
		env := make([]string, 0, len(wrapped.Env)+len(spec.Env))
		env = append(env, wrapped.Env...)
		env = append(env, sortedEnvPairs(spec.Env)...)
		cmd.Env = env
		cmd.Stdin = wrapped.Stdin
		cmd.Stdout = wrapped.Stdout
		cmd.Stderr = wrapped.Stderr
		cmd.ExtraFiles = wrapped.ExtraFiles
		cmd.Dir = wrapped.Dir
	} else {
		cmd.Env = append(cmd.Env, sortedEnvPairs(spec.Env)...)
	}
	// applyProcessGroup must run AFTER wrap: WrapCommandMode constructs a fresh
	// exec.Cmd and discards SysProcAttr, Cancel, and WaitDelay, so reaping
	// applied before the wrap would silently do nothing.
	applyProcessGroup(cmd)

	return cmd
}

// sortedEnvPairs renders m as KEY=VALUE pairs in sorted key order, so the
// resulting env is deterministic instead of depending on map iteration order.
func sortedEnvPairs(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+m[k])
	}
	return pairs
}
