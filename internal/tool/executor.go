package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
)

// SandboxWrapper wraps commands for sandboxed or unsandboxed execution. Every
// Executor carries an explicit, non-nil SandboxWrapper: Unsandboxed{} means
// sandboxing is deliberately off, not merely unconfigured.
type SandboxWrapper interface {
	// Enabled reports whether sandboxing is active.
	Enabled() bool
	// WrapCommandMode wraps cmd for execution, optionally with the project
	// mounted read-only.
	WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) (*exec.Cmd, error)
}

// SandboxCommandResourceReleaser releases resources owned by a wrapped command.
// It is optional so existing SandboxWrapper implementations remain compatible.
type SandboxCommandResourceReleaser interface {
	ReleaseCommandResources(*exec.Cmd)
}

// Unsandboxed is a SandboxWrapper that runs commands unwrapped. Passing it to
// NewExecutor is an explicit choice that sandboxing is off for this executor,
// not an absence of configuration.
type Unsandboxed struct{}

// Enabled always reports false: Unsandboxed never sandboxes.
func (Unsandboxed) Enabled() bool { return false }

// WrapCommandMode returns cmd unchanged.
func (Unsandboxed) WrapCommandMode(cmd *exec.Cmd, _ bool) (*exec.Cmd, error) { return cmd, nil }

// Executor runs tool definitions through a resolution, normalization, and dispatch
// pipeline. The caller-facing seam is Execute.
type Executor struct {
	registry    *Registry
	approver    ApprovalResponder
	sandbox     SandboxWrapper
	modeGetter  func() config.ExecutionMode
	workDir     string
	pathPolicy  PathPolicy
	writeScope  *WriteScope
	outputLimit int
	// diagnostics receives one tool-stream record per Execute call, on every
	// outcome (issue #707 stage 4). A nil writer is a no-op, so unwired paths
	// and tests need no special handling.
	diagnostics *diagnostics.Writer
	// scope tags every tool-stream record this executor writes with its
	// caller: the top-level parent, or a sub-agent's ID and type. Set once at
	// construction via WithDiagnosticsScope.
	scope diagnosticsScope
}

// NewExecutor creates a new tool executor with the given registry, config, approver,
// working directory, sandbox temp directory, and sandbox wrapper. sandboxTmpDir is
// optional; when non-empty, /tmp paths in tool input are rewritten to sandboxTmpDir.
// sandbox must not be nil; callers should pass Unsandboxed{} explicitly when
// sandboxing is off. If sandbox is nil, NewExecutor defaults to Unsandboxed{}.
func NewExecutor(registry *Registry, cfg config.Config, approver ApprovalResponder, workDir, sandboxTmpDir string, sandbox SandboxWrapper) *Executor {
	return NewExecutorWithConfig(registry, ExecutorConfigFromConfig(cfg), approver, workDir, sandboxTmpDir, sandbox)
}

// NewExecutorWithConfig creates a tool executor from a prepared ExecutorConfig.
// Arguments and sandbox defaulting match NewExecutor. Parents and children share
// one ExecutorConfig built by ExecutorConfigFromConfig.
func NewExecutorWithConfig(registry *Registry, cfg ExecutorConfig, approver ApprovalResponder, workDir, sandboxTmpDir string, sandbox SandboxWrapper) *Executor {
	if sandbox == nil {
		sandbox = Unsandboxed{}
	}
	root := normalizeExecutionRoot(workDir)
	outputLimit := cfg.outputLimit
	if outputLimit < 1 {
		outputLimit = defaultToolOutputMaxBytes
	}
	return &Executor{
		registry:    registry,
		approver:    approver,
		sandbox:     sandbox,
		workDir:     root,
		pathPolicy:  NewPathPolicyWithSandbox(root, cfg.paths, sandboxTmpDir),
		outputLimit: outputLimit,
	}
}

// WithDiagnostics sets the diagnostics writer on the executor and returns it
// for chaining, mirroring WithModeGetter. The composition root supplies a
// writer only when the tool stream is enabled.
func (e *Executor) WithDiagnostics(w *diagnostics.Writer) *Executor {
	e.diagnostics = w
	return e
}

// WithDiagnosticsScope tags every tool-stream record this executor writes with
// its caller: parent, or a sub-agent's ID and type. It returns the executor
// for chaining, mirroring WithDiagnostics.
func (e *Executor) WithDiagnosticsScope(source diagnostics.Source, agentID, agentType string) *Executor {
	e.scope = diagnosticsScope{source: source, agentID: agentID, agentType: agentType}
	return e
}

// WithModeGetter sets the execution mode getter on the executor and returns it
// for chaining. The getter is called during execution to determine the current
// mode (plan or build). When non-nil, the mode is threaded through the execution
// context for handlers to check.
func (e *Executor) WithModeGetter(getter func() config.ExecutionMode) *Executor {
	e.modeGetter = getter
	return e
}

// WithWriteScope restricts every writable path this executor resolves to files
// matching scope, and returns the executor for chaining, mirroring
// WithModeGetter. The scope is applied to the effective policy after plan-mode
// restrictions, so both hold. A scope with no rules clears any previous scope
// and leaves writes unrestricted.
func (e *Executor) WithWriteScope(scope WriteScope) *Executor {
	scope = cloneWriteScope(scope)
	if !scope.active() {
		e.writeScope = nil
		return e
	}
	e.writeScope = &scope
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
	start := time.Now()
	ctx = e.withDiagnosticsCapture(ctx)
	result, err := e.runPipeline(ctx, executionInput{ToolName: toolName, CallID: callID, Input: input})
	e.recordDiagnostics(ctx, toolName, input, result, err, time.Since(start))
	return result, err
}

// withDiagnosticsCapture stamps ctx with the tool diagnostics capture level so
// mutate can decide whether to compute match features. Off when the tool
// stream is disabled, Bodies when capture_bodies is on, Scalars otherwise.
// Both writer probes are nil-safe, so an unwired executor yields Off.
func (e *Executor) withDiagnosticsCapture(ctx context.Context) context.Context {
	switch {
	case !e.diagnostics.Enabled(diagnostics.KindTool):
		return WithDiagnosticsCapture(ctx, DiagnosticsCaptureOff)
	case e.diagnostics.CaptureBodies():
		return WithDiagnosticsCapture(ctx, DiagnosticsCaptureBodies)
	default:
		return WithDiagnosticsCapture(ctx, DiagnosticsCaptureScalars)
	}
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
