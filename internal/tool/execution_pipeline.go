package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/luispabon/steiner/internal/config"
)

type executionInput struct {
	ToolName string
	Input    map[string]any
}

type executionContext struct {
	Def             ToolDef
	NormalizedInput map[string]any
	ApprovalMode    config.ApprovalMode
	Preview         ApprovalPreview
}

func (e *Executor) authorizeExecution(ctx context.Context, ec *executionContext) error {
	switch {
	case IsApprovalDenied(ec.ApprovalMode):
		return &ToolExecutionError{
			Tool:    ec.Def.Name,
			Kind:    "approval_denied",
			Message: "tool execution denied by approval policy",
		}
	case IsApprovalPrompt(ec.ApprovalMode):
		if e.approver == nil {
			return &ToolExecutionError{
				Tool:    ec.Def.Name,
				Kind:    "approval_required",
				Message: "tool execution requires approval",
			}
		}
		responseCh := make(chan ApprovalResponse, 1)
		if err := e.approver.RequestApproval(ctx, ApprovalRequest{
			Tool:     ec.Def,
			Mode:     ec.ApprovalMode,
			Input:    CloneJSONMap(ec.NormalizedInput),
			WorkDir:  e.pathPolicy.Root(),
			Preview:  ec.Preview,
			Response: responseCh,
		}); err != nil {
			return &ToolExecutionError{
				Tool:    ec.Def.Name,
				Kind:    "approval_failed",
				Message: err.Error(),
			}
		}
		decision := ApprovalResponse{}
		select {
		case decision = <-responseCh:
		case <-ctx.Done():
			return ctx.Err()
		}
		if !decision.Allow {
			message := strings.TrimSpace(decision.Message)
			if message == "" {
				message = "tool execution denied"
			}
			return &ToolExecutionError{
				Tool:    ec.Def.Name,
				Kind:    "approval_denied",
				Message: message,
			}
		}
	}
	return nil
}

func (e *Executor) resolveDefinition(in executionInput) (ToolDef, error) {
	if e == nil || e.registry == nil {
		return ToolDef{}, fmt.Errorf("tool executor is not configured")
	}
	def, ok := e.registry.Get(in.ToolName)
	if !ok {
		return ToolDef{}, fmt.Errorf("tool %q is not registered", in.ToolName)
	}
	return def, nil
}

func (e *Executor) normalizeExecutionInput(def ToolDef, input map[string]any) (map[string]any, error) {
	normalizedInput, err := e.pathPolicy.ValidateToolInput(def.Name, input)
	if err != nil {
		return nil, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "policy_denied",
			Message: err.Error(),
		}
	}
	return normalizedInput, nil
}

func (e *Executor) resolveApprovalState(def ToolDef, normalizedInput map[string]any) (config.ApprovalMode, ApprovalPreview, error) {
	mode := e.approval.ModeFor(def)
	preview, err := e.approval.PreviewFor(def, normalizedInput, e.pathPolicy)
	if err != nil {
		return mode, ApprovalPreview{}, &ToolExecutionError{
			Tool:    def.Name,
			Kind:    "policy_denied",
			Message: err.Error(),
		}
	}
	return mode, preview, nil
}

func (e *Executor) runPipeline(ctx context.Context, in executionInput) (any, error) {
	def, err := e.resolveDefinition(in)
	if err != nil {
		return nil, err
	}

	normalizedInput, err := e.normalizeExecutionInput(def, in.Input)
	if err != nil {
		return nil, err
	}

	mode, preview, err := e.resolveApprovalState(def, normalizedInput)
	if err != nil {
		return nil, err
	}

	ec := executionContext{
		Def:             def,
		NormalizedInput: normalizedInput,
		ApprovalMode:    mode,
		Preview:         preview,
	}

	if err := e.authorizeExecution(ctx, &ec); err != nil {
		return nil, err
	}

	return e.executeTool(ctx, &ec)
}

// executeTool dispatches tool execution to the appropriate phase:
//   - handler execution, if the tool defines a Handler
//   - subprocess execution, with JSON input marshaling, per-tool timeout,
//     work-dir selection, and output decoding via decodeExecutionOutput
func (e *Executor) executeTool(ctx context.Context, ec *executionContext) (any, error) {
	if ec.Def.Handler != nil {
		return ec.Def.Handler(ctx, ec.NormalizedInput)
	}

	payload, err := json.Marshal(CloneJSONMap(ec.NormalizedInput))
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %q: %w", ec.Def.Name, err)
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if ec.Def.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, ec.Def.Timeout)
		defer cancel()
	}

	workDir := e.pathPolicy.Root()
	if ec.Def.Name == "bash" {
		if cwd, ok := ec.NormalizedInput["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
			workDir = cwd
		}
	}

	stdout, _, metadata, runErr := runSubprocess(execCtx, ec.Def, payload, workDir, e.outputLimit)
	if runErr != nil && !isExitStatusError(runErr) {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return nil, runErr
		}
		return nil, &ToolExecutionError{
			Tool:     ec.Def.Name,
			Kind:     "subprocess_failed",
			Message:  runErr.Error(),
			ExitCode: metadata.ExitCode,
			Output:   metadata,
			Details:  outputDetails(metadata),
		}
	}

	return decodeExecutionOutput(stdout, metadata, ec.Def.Name)
}

// decodeExecutionOutput decodes the JSON envelope from subprocess stdout
// and shapes the result into either an ExecutionResult or a structured error
// carrying execution metadata.
func decodeExecutionOutput(stdout []byte, metadata ExecutionMetadata, toolName string) (any, error) {
	var envelope JSONEnvelope
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		return nil, &ToolExecutionError{
			Tool:     toolName,
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
			Tool:     toolName,
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
		return exitErr.ExitCode()
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
