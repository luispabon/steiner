package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("tool executor is not configured")
	}

	def, ok := e.registry.Get(toolName)
	if !ok {
		return nil, fmt.Errorf("tool %q is not registered", toolName)
	}

	normalizedInput, err := e.pathPolicy.ValidateToolInput(def.Name, input)
	if err != nil {
		return nil, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "policy_denied",
			Message: err.Error(),
		}
	}

	mode := e.approval.ModeFor(def)
	preview, err := e.approval.PreviewFor(def, normalizedInput, e.pathPolicy)
	if err != nil {
		return nil, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "policy_denied",
			Message: err.Error(),
		}
	}

	switch {
	case IsApprovalDenied(mode):
		return nil, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "approval_denied",
			Message: "tool execution denied by approval policy",
		}
	case IsApprovalPrompt(mode):
		if e.approver == nil {
			return nil, &ToolExecutionError{
				Tool:    def.Name,
				Kind:    "approval_required",
				Message: "tool execution requires approval",
			}
		}
		responseCh := make(chan ApprovalResponse, 1)
		if err := e.approver.RequestApproval(ctx, ApprovalRequest{
			Tool:     def,
			Mode:     mode,
			Input:    CloneJSONMap(normalizedInput),
			WorkDir:  e.pathPolicy.Root(),
			Preview:  preview,
			Response: responseCh,
		}); err != nil {
			return nil, err
		}
		decision := ApprovalResponse{}
		select {
		case decision = <-responseCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if !decision.Allow {
			message := strings.TrimSpace(decision.Message)
			if message == "" {
				message = "tool execution denied"
			}
			return nil, &ToolExecutionError{
				Tool:    def.Name,
				Kind:    "approval_denied",
				Message: message,
			}
		}
	}

	if def.Handler != nil {
		return def.Handler(ctx, normalizedInput)
	}

	payload, err := json.Marshal(CloneJSONMap(normalizedInput))
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %q: %w", def.Name, err)
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if def.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, def.Timeout)
		defer cancel()
	}

	workDir := e.pathPolicy.Root()
	if def.Name == "bash" {
		if cwd, ok := normalizedInput["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			workDir = cwd
		}
	}

	stdout, _, metadata, runErr := runSubprocess(execCtx, def, payload, workDir, e.outputLimit)
	if runErr != nil && !isExitStatusError(runErr) {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return nil, runErr
		}
		return nil, &ToolExecutionError{
			Tool:     def.Name,
			Kind:     "subprocess_failed",
			Message:  runErr.Error(),
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	var envelope JSONEnvelope
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		return nil, &ToolExecutionError{
			Tool:     def.Name,
			Kind:     "invalid_json",
			Message:  "tool output was not valid JSON",
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	if !envelope.OK {
		if envelope.Error == nil {
			envelope.Error = &JSONEnvelopeError{
				Kind:    "tool_failed",
				Message: "tool reported a failure without an error payload",
			}
		}
		return nil, &ToolExecutionError{
			Tool:     def.Name,
			Kind:     envelope.Error.Kind,
			Message:  envelope.Error.Message,
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  envelope.Error.Details,
		}
	}

	return ExecutionResult{
		Value:    envelope.Result,
		Metadata: metadata,
	}, nil
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
