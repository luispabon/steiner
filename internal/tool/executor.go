package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"github.com/luispabon/steiner/internal/config"
)

type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error)
}

type ApproverFunc func(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error)

func (f ApproverFunc) Approve(ctx context.Context, req ApprovalRequest) (ApprovalResponse, error) {
	return f(ctx, req)
}

type Executor struct {
	registry *Registry
	approval ApprovalResolver
	approver Approver
	workDir  string
}

func NewExecutor(registry *Registry, cfg config.Config, approver Approver, workDir string) *Executor {
	return &Executor{
		registry: registry,
		approval: NewApprovalResolver(cfg),
		approver: approver,
		workDir:  workDir,
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

	mode := e.approval.ModeFor(def)
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
		decision, err := e.approver.Approve(ctx, ApprovalRequest{
			Tool:    def,
			Mode:    mode,
			Input:   cloneInputMap(input),
			WorkDir: e.workDir,
		})
		if err != nil {
			return nil, err
		}
		if !decision.Allow {
			message := decision.Message
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

	payload, err := json.Marshal(cloneInputMap(input))
	if err != nil {
		return nil, fmt.Errorf("marshal tool input for %q: %w", def.Name, err)
	}

	stdout, stderr, exitCode, runErr := runSubprocess(ctx, def, payload, e.workDir)
	if runErr != nil && !isExitStatusError(runErr) {
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return nil, runErr
		}
		return nil, &ToolExecutionError{
			Tool:     def.Name,
			Kind:     "subprocess_failed",
			Message:  runErr.Error(),
			ExitCode: exitCode,
			Stdout:   string(stdout),
			Stderr:   string(stderr),
		}
	}

	var envelope JSONEnvelope
	if err := json.Unmarshal(stdout, &envelope); err != nil {
		return nil, &ToolExecutionError{
			Tool:     def.Name,
			Kind:     "invalid_json",
			Message:  "tool output was not valid JSON",
			ExitCode: exitCode,
			Stdout:   string(stdout),
			Stderr:   string(stderr),
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
			ExitCode: exitCode,
			Stdout:   string(stdout),
			Stderr:   string(stderr),
			Details:  envelope.Error.Details,
		}
	}

	return envelope.Result, nil
}

func runSubprocess(ctx context.Context, def ToolDef, payload []byte, workDir string) ([]byte, []byte, int, error) {
	if def.ExecPath == "" {
		return nil, nil, 1, &ToolExecutionError{
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

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		exitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ProcessState != nil {
			exitCode = exitErr.ProcessState.ExitCode()
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return stdout.Bytes(), stderr.Bytes(), exitCode, ctxErr
		}
	}

	return stdout.Bytes(), stderr.Bytes(), exitCode, err
}

func cloneInputMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

func cloneJSONValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return cloneInputMap(v)
	case []any:
		cloned := make([]any, len(v))
		for i, child := range v {
			cloned[i] = cloneJSONValue(child)
		}
		return cloned
	case json.RawMessage:
		return append(json.RawMessage(nil), v...)
	default:
		return value
	}
}

func isExitStatusError(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr)
}
