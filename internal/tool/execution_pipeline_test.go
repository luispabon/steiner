package tool

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

func TestRunPipelineUnknownTool(t *testing.T) {
	reg := NewRegistry()
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "is not registered") {
		t.Fatalf("error = %v, want 'is not registered'", err)
	}
}

func TestRunPipelinePolicyDenied(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:     "read",
		ExecPath: "cat",
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "read", map[string]any{
		"path": "/etc/passwd",
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}

func TestRunPipelineResolveAndNormalizeSuccess(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: helper,
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	result, err := executor.Execute(context.Background(), "probe", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if execResult.Metadata.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", execResult.Metadata.ExitCode)
	}
}

func TestRunPipelineApprovalDenied(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: "true",
		Approval: config.ApprovalModeDeny,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want approval_denied")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "approval_denied" {
		t.Fatalf("error kind = %q, want approval_denied", toolErr.Kind)
	}
	if toolErr.Message != "tool execution denied by approval policy" {
		t.Fatalf("error message = %q, want 'tool execution denied by approval policy'", toolErr.Message)
	}
}

func TestRunPipelineApprovalRequiredNoApprover(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: "true",
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModePrompt},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want approval_required")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "approval_required" {
		t.Fatalf("error kind = %q, want approval_required", toolErr.Kind)
	}
}

func TestRunPipelineApprovalContextCanceled(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: "true",
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModePrompt},
	}
	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(ctx context.Context, req ApprovalRequest) error {
		return nil
	}), t.TempDir())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := executor.Execute(ctx, "probe", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestExecuteToolHandlerSuccess(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name: "greeter",
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			return map[string]any{"message": "hello"}, nil
		},
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	result, err := executor.Execute(context.Background(), "greeter", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T, want map[string]any", result)
	}
	if resultMap["message"] != "hello" {
		t.Fatalf("result message = %v, want 'hello'", resultMap["message"])
	}
}

func TestExecuteToolSubprocessSuccess(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:     "probe",
		ExecPath: helper,
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	result, err := executor.Execute(context.Background(), "probe", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if execResult.Metadata.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", execResult.Metadata.ExitCode)
	}
}

func TestExecuteToolTimeout(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:       "probe",
		ExecPath:   helper,
		Subcommand: "sleep",
		Timeout:    100 * time.Millisecond,
		Approval:   config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want DeadlineExceeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestExecuteToolBashCwdOverride(t *testing.T) {
	helper := mustBuildHelperBinary(t)
	reg := NewRegistry(ToolDef{
		Name:     "bash",
		ExecPath: helper,
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	workDir := t.TempDir()
	executor := NewExecutor(reg, cfg, nil, workDir)
	result, err := executor.Execute(context.Background(), "bash", map[string]any{
		"cwd": workDir,
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if execResult.Metadata.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", execResult.Metadata.ExitCode)
	}
}
