package tool

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

type ApprovalResponder interface {
	RequestApproval(ctx context.Context, req ApprovalRequest) error
}

type ApprovalResponderFunc func(ctx context.Context, req ApprovalRequest) error

func (f ApprovalResponderFunc) RequestApproval(ctx context.Context, req ApprovalRequest) error {
	return f(ctx, req)
}

type Executor struct {
	registry    *Registry
	approval    ApprovalResolver
	approver    ApprovalResponder
	workDir     string
	pathPolicy  PathPolicy
	outputLimit int
}

// NewExecutor creates a new tool executor with the given registry, config, approver, and working directory.
func NewExecutor(registry *Registry, cfg config.Config, approver ApprovalResponder, workDir string) *Executor {
	root := normalizeExecutionRoot(workDir)
	outputLimit := cfg.Limits.ToolOutputMaxBytes
	if outputLimit < 1 {
		outputLimit = 65536
	}
	return &Executor{
		registry:    registry,
		approval:    NewApprovalResolver(cfg),
		approver:    approver,
		workDir:     root,
		pathPolicy:  NewPathPolicy(root, cfg.Paths),
		outputLimit: outputLimit,
	}
}

func (e *Executor) Execute(ctx context.Context, toolName string, input map[string]any) (any, error) {
	return e.runPipeline(ctx, executionInput{ToolName: toolName, Input: input})
}

func runSubprocess(ctx context.Context, def ToolDef, payload []byte, workDir string, limit int) ([]byte, []byte, ExecutionMetadata, error) {
	if def.ExecPath == "" {
		return nil, nil, ExecutionMetadata{}, &ToolExecutionError{
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
	cmd.Stdin = bytes.NewReader(payload)

	stdoutCapture := newBoundedCapture(limit)
	stderrCapture := newBoundedCapture(limit)
	cmd.Stdout = stdoutCapture
	cmd.Stderr = stderrCapture

	err := cmd.Run()
	metadata := ExecutionMetadata{
		ExitCode: exitCodeFromError(err),
		Stdout:   stdoutCapture.Capture(),
		Stderr:   stderrCapture.Capture(),
	}
	if err != nil && ctx.Err() != nil {
		return stdoutCapture.Bytes(), stderrCapture.Bytes(), metadata, ctx.Err()
	}

	return stdoutCapture.Bytes(), stderrCapture.Bytes(), metadata, err
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
		return exitErr.ProcessState.ExitCode()
	}
	return 1
}

func outputDetails(metadata ExecutionMetadata) map[string]any {
	details := map[string]any{
		"stdout": metadata.Stdout,
		"stderr": metadata.Stderr,
	}
	if metadata.ExitCode != 0 {
		details["exit_code"] = metadata.ExitCode
	}
	return details
}

func isExitStatusError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
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
