package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type executionInput struct {
	ToolName string
	Input    map[string]any
}

func (e *Executor) runPipeline(ctx context.Context, in executionInput) (any, error) {
	if e == nil || e.registry == nil {
		return nil, fmt.Errorf("tool executor is not configured")
	}

	def, ok := e.registry.Get(in.ToolName)
	if !ok {
		return nil, fmt.Errorf("tool %q is not registered", in.ToolName)
	}

	normalizedInput, err := e.pathPolicy.ValidateToolInput(def.Name, in.Input)
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
			return nil, &ToolExecutionError{
				Tool:    def.Name,
				Kind:    "approval_failed",
				Message: err.Error(),
			}
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
