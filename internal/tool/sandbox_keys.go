package tool

import (
	"context"
	"os/exec"
)

// BashReadOnlyProjectKey is a context key used to signal that bash must force the
// read-only project mount.
type BashReadOnlyProjectKey struct{}

// WithReadOnlyProjectBash returns a context that requests read-only project bash.
func WithReadOnlyProjectBash(ctx context.Context, on bool) context.Context {
	if !on {
		return ctx
	}
	return context.WithValue(ctx, BashReadOnlyProjectKey{}, on)
}

// EffectivePolicyKey is a context key used to carry the effective (possibly relaxed)
// PathPolicy through the execution pipeline. Set when the user approves a path
// policy violation via the approver, so handlers can use the approved policy
// instead of the original restrictive one.
type EffectivePolicyKey struct{}

// ExecutionModeKey is a context key used to carry the current execution mode
// (plan or build) through the execution pipeline. Set when a mode getter is
// configured on the executor, allowing tool handlers to make mode-aware decisions.
type ExecutionModeKey struct{}

// ExecutionCallIDKey is a context key used to carry the originating tool-call
// ID through handler execution for approval correlation.
type ExecutionCallIDKey struct{}

// ResolvedSandbox is the sandbox decision resolved for a single tool call: which
// SandboxWrapper applies, and whether the project must be mounted read-only. It
// bakes readOnlyProject into the decision so call sites only ever call Wrap(cmd)
// and cannot pass a mismatched or stale readOnlyProject value.
type ResolvedSandbox struct {
	Wrapper         SandboxWrapper
	ReadOnlyProject bool
}

// Wrap applies the resolved sandbox decision to cmd.
func (r ResolvedSandbox) Wrap(cmd *exec.Cmd) *exec.Cmd {
	return r.Wrapper.WrapCommandMode(cmd, r.ReadOnlyProject)
}

// SandboxWrapperKey is a context key carrying the ResolvedSandbox decision for
// the current tool call. It is set exactly once per call, by the execution
// pipeline in runPipeline, and is always non-nil there (Wrapper is Unsandboxed{}
// when sandboxing is off). Its absence from context means the handler was
// invoked outside the pipeline; callers must fail closed rather than assume
// unsandboxed execution.
type SandboxWrapperKey struct{}

// BashDenialResult is implemented by builtin.BashResult to allow sandbox
// denial detection in the execution pipeline without an import cycle.
type BashDenialResult interface {
	// BashExitCode returns the exit code of the bash command.
	BashExitCode() int
	// BashOutput returns the combined stdout/stderr output of the bash command.
	BashOutput() string
	// AppendOutput appends s to the command output (used to add grant instructions).
	AppendOutput(s string)
}
