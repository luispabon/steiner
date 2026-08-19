package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// SandboxWrapper wraps commands for sandboxed or unsandboxed execution. Every
// Executor carries an explicit, non-nil SandboxWrapper: Unsandboxed{} means
// sandboxing is deliberately off, not merely unconfigured.
type SandboxWrapper interface {
	// Enabled reports whether sandboxing is active.
	Enabled() bool
	// WrapCommandMode wraps cmd for execution, optionally with the project
	// mounted read-only.
	WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) *exec.Cmd
}

// Unsandboxed is a SandboxWrapper that runs commands unwrapped. Passing it to
// NewExecutor is an explicit choice that sandboxing is off for this executor,
// not an absence of configuration.
type Unsandboxed struct{}

// Enabled always reports false: Unsandboxed never sandboxes.
func (Unsandboxed) Enabled() bool { return false }

// WrapCommandMode returns cmd unchanged.
func (Unsandboxed) WrapCommandMode(cmd *exec.Cmd, _ bool) *exec.Cmd { return cmd }

// Executor runs tool definitions through a resolution, normalization, and dispatch
// pipeline. The caller-facing seam is Execute.
type Executor struct {
	registry    *Registry
	approver    ApprovalResponder
	sandbox     SandboxWrapper
	modeGetter  func() config.ExecutionMode
	workDir     string
	pathPolicy  PathPolicy
	outputLimit int
}

// NewExecutor creates a new tool executor with the given registry, config, approver,
// working directory, sandbox temp directory, and sandbox wrapper. sandboxTmpDir is
// optional; when non-empty, /tmp paths in tool input are rewritten to sandboxTmpDir.
// sandbox must not be nil; callers should pass Unsandboxed{} explicitly when
// sandboxing is off.
func NewExecutor(registry *Registry, cfg config.Config, approver ApprovalResponder, workDir, sandboxTmpDir string, sandbox SandboxWrapper) *Executor {
	root := normalizeExecutionRoot(workDir)
	outputLimit := cfg.Limits.ToolOutputMaxBytes
	if outputLimit < 1 {
		outputLimit = 65536
	}
	return &Executor{
		registry:    registry,
		approver:    approver,
		sandbox:     sandbox,
		workDir:     root,
		pathPolicy:  NewPathPolicyWithSandbox(root, cfg.Paths, sandboxTmpDir),
		outputLimit: outputLimit,
	}
}

// WithModeGetter sets the execution mode getter on the executor and returns it
// for chaining. The getter is called during execution to determine the current
// mode (plan or build). When non-nil, the mode is threaded through the execution
// context for handlers to check.
func (e *Executor) WithModeGetter(getter func() config.ExecutionMode) *Executor {
	e.modeGetter = getter
	return e
}

// WorkDir returns the resolved working directory root for this executor.
func (e *Executor) WorkDir() string {
	return e.workDir
}

// Execute runs toolName with the given input through the full execution pipeline
// and returns the decoded result or a structured ToolExecutionError. callID
// identifies the originating tool call for approval correlation and may be
// empty when no call ID is available.
func (e *Executor) Execute(ctx context.Context, toolName, callID string, input map[string]any) (any, error) {
	return e.runPipeline(ctx, executionInput{ToolName: toolName, CallID: callID, Input: input})
}

func normalizeExecutionRoot(workDir string) string {
	if strings.TrimSpace(workDir) == "" {
		return ""
	}
	root, err := filepath.Abs(workDir)
	if err != nil {
		return filepath.Clean(workDir)
	}
	return filepath.Clean(root)
}
