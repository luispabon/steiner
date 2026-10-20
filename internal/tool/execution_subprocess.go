package tool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

func runSubprocess(ctx context.Context, def ToolDef, payload []byte, workDir string, limit int, resolved ResolvedSandbox) ([]byte, ExecutionMetadata, error) {
	if def.ExecPath == "" {
		return nil, ExecutionMetadata{}, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "invalid_definition",
			Message: "tool exec path is empty",
		}
	}

	args := []string{}
	if def.Subcommand != "" {
		args = append(args, def.Subcommand)
	}

	cmd := exec.CommandContext(ctx, def.ExecPath, args...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	tracked, err := resolved.Wrap(cmd)
	if err != nil {
		return nil, ExecutionMetadata{}, fmt.Errorf("wrap command: %w", err)
	}
	cmd = rebuildWithContext(ctx, tracked)
	cmd.Stdin = bytes.NewReader(payload)

	stdoutCapture := newBoundedCapture(limit)
	stderrCapture := newBoundedCapture(limit)
	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	err = cmd.Start()
	// The sandbox tracks per-command resources against the command it
	// returned, not the rebuilt one.
	resolved.ReleaseCommandResources(tracked)
	if err == nil {
		err = cmd.Wait()
	}
	metadata := ExecutionMetadata{
		ExitCode: exitCodeFromError(err),
		Stdout:   stdoutCapture.Capture(),
		Stderr:   stderrCapture.Capture(),
	}
	if err != nil && ctx.Err() != nil {
		return stdoutCapture.Bytes(), metadata, ctx.Err()
	}

	return stdoutCapture.Bytes(), metadata, err
}

// rebuildWithContext returns a command bound to ctx carrying wrapped's
// program, arguments, environment, stdio, extra files and directory, with
// process-group cancellation applied. Sandbox wrappers return a fresh
// exec.Cmd literal that has no context, so cancellation and tool timeouts would
// otherwise never kill the process. Process-group setup must come after the
// rebuild because it would be discarded by a wrapper.
func rebuildWithContext(ctx context.Context, wrapped *exec.Cmd) *exec.Cmd {
	var args []string
	if len(wrapped.Args) > 0 {
		args = wrapped.Args[1:]
	}
	cmd := exec.CommandContext(ctx, wrapped.Path, args...)
	cmd.Env = wrapped.Env
	cmd.Stdin = wrapped.Stdin
	cmd.Stdout = wrapped.Stdout
	cmd.Stderr = wrapped.Stderr
	cmd.ExtraFiles = wrapped.ExtraFiles
	cmd.Dir = wrapped.Dir
	applySubprocessGroup(cmd)
	return cmd
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		return exitErr.ExitCode()
	}
	return 1
}
