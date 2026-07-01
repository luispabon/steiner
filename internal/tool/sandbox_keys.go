package tool

// BashUnsandboxedKey is a context key used to signal that the bash handler
// should skip the CommandWrapper and run without sandbox wrapping.
// Set when the user approves a sandbox boundary violation via the approver.
type BashUnsandboxedKey struct{}

// EffectivePolicyKey is a context key used to carry the effective (possibly relaxed)
// PathPolicy through the execution pipeline. Set when the user approves a path
// policy violation via the approver, so handlers can use the approved policy
// instead of the original restrictive one.
type EffectivePolicyKey struct{}

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
