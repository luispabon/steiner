package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

// SandboxWrapper wraps commands with sandbox execution. A nil SandboxWrapper means unsafe mode.
type SandboxWrapper interface {
	// Enabled reports whether sandboxing is active.
	Enabled() bool
	// WrapCommand wraps cmd for sandboxed execution.
	WrapCommand(cmd *exec.Cmd) *exec.Cmd
	// TmpDir returns the session-scoped temporary directory for sandbox.
	TmpDir() string
}

// Executor runs tool definitions through a resolution, normalization, and dispatch
// pipeline. The caller-facing seam is Execute.
type Executor struct {
	registry    *Registry
	approver    ApprovalResponder
	sandbox     SandboxWrapper
	workDir     string
	pathPolicy  PathPolicy
	outputLimit int
}

// NewExecutor creates a new tool executor with the given registry, config, approver, and working directory.
// sandboxTmpDir is optional; when non-empty, /tmp paths in tool input are rewritten to sandboxTmpDir.
func NewExecutor(registry *Registry, cfg config.Config, approver ApprovalResponder, workDir, sandboxTmpDir string) *Executor {
	root := normalizeExecutionRoot(workDir)
	outputLimit := cfg.Limits.ToolOutputMaxBytes
	if outputLimit < 1 {
		outputLimit = 65536
	}
	return &Executor{
		registry:    registry,
		approver:    approver,
		workDir:     root,
		pathPolicy:  NewPathPolicyWithSandbox(root, cfg.Paths, sandboxTmpDir),
		outputLimit: outputLimit,
	}
}

// WithSandbox sets the sandbox wrapper on the executor and returns it for chaining.
func (e *Executor) WithSandbox(s SandboxWrapper) *Executor {
	e.sandbox = s
	return e
}

// Sandbox returns the sandbox wrapper currently set on this executor.
// A nil return means no sandboxing is active (unsafe mode).
func (e *Executor) Sandbox() SandboxWrapper {
	return e.sandbox
}

// WorkDir returns the resolved working directory root for this executor.
func (e *Executor) WorkDir() string {
	return e.workDir
}

// Execute runs toolName with the given input through the full execution pipeline
// and returns the decoded result or a structured ToolExecutionError.
func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	return e.runPipeline(ctx, executionInput{ToolName: toolName, Input: input})
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
