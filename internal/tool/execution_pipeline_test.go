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
	executor := NewExecutor(reg, cfg, ApprovalResponderFunc(func(_ context.Context, _ ApprovalRequest) error {
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
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
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

func TestDecodeExecutionOutputCommandNotFound(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:     "nonexistent",
		ExecPath: "/path/to/nonexistent/binary",
		Approval: config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "nonexistent", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want subprocess_failed")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "subprocess_failed" {
		t.Fatalf("error kind = %q, want subprocess_failed", toolErr.Kind)
	}
}

func TestDecodeExecutionOutputEnvelopeNotOK(t *testing.T) {
	metadata := ExecutionMetadata{ExitCode: 1}
	_, err := decodeExecutionOutput([]byte(`{"ok":false,"error":{"kind":"custom_fail","message":"nope"}}`), metadata, "test_tool")
	if err == nil {
		t.Fatal("decodeExecutionOutput() error = nil, want ToolExecutionError")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "custom_fail" {
		t.Fatalf("error kind = %q, want custom_fail", toolErr.Kind)
	}
	if toolErr.Message != "nope" {
		t.Fatalf("error message = %q, want 'nope'", toolErr.Message)
	}
	if toolErr.ExitCode != 1 {
		t.Fatalf("error exit code = %d, want 1", toolErr.ExitCode)
	}
}

func TestDecodeExecutionOutputSuccess(t *testing.T) {
	metadata := ExecutionMetadata{ExitCode: 0}
	result, err := decodeExecutionOutput([]byte(`{"ok":true,"result":{"status":"ok"}}`), metadata, "test_tool")
	if err != nil {
		t.Fatalf("decodeExecutionOutput() error = %v", err)
	}
	execResult, ok := result.(ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	resultMap, ok := execResult.Value.(map[string]any)
	if !ok {
		t.Fatalf("result value type = %T, want map[string]any", execResult.Value)
	}
	if resultMap["status"] != "ok" {
		t.Fatalf("result status = %v, want 'ok'", resultMap["status"])
	}
	if execResult.Metadata.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", execResult.Metadata.ExitCode)
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

func TestDecodeExecutionOutputInvalidJSON(t *testing.T) {
	metadata := ExecutionMetadata{ExitCode: 1}
	_, err := decodeExecutionOutput([]byte(`not json`), metadata, "test_tool")
	if err == nil {
		t.Fatal("decodeExecutionOutput() error = nil, want ToolExecutionError")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "invalid_json" {
		t.Fatalf("error kind = %q, want invalid_json", toolErr.Kind)
	}
	if toolErr.ExitCode != 1 {
		t.Fatalf("error exit code = %d, want 1", toolErr.ExitCode)
	}
	if !strings.Contains(toolErr.Message, "not valid JSON") {
		t.Fatalf("error message = %q, want 'not valid JSON'", toolErr.Message)
	}
}

func TestRunPipelineInvalidJSON(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name:       "echoer",
		ExecPath:   "echo",
		Subcommand: "not json",
		Approval:   config.ApprovalModeAuto,
	})
	cfg := config.Config{
		Approval: config.ApprovalConfig{Default: config.ApprovalModeAuto},
	}
	executor := NewExecutor(reg, cfg, nil, t.TempDir())
	_, err := executor.Execute(context.Background(), "echoer", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid_json")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "invalid_json" {
		t.Fatalf("error kind = %q, want invalid_json", toolErr.Kind)
	}
	if !strings.Contains(toolErr.Error(), "not valid JSON") {
		t.Fatalf("error = %v, want 'not valid JSON'", toolErr.Error())
	}
}

func TestRunPipelineNilRegistry(t *testing.T) {
	_, err := NewExecutor(nil, config.Config{}, nil, t.TempDir()).Execute(context.Background(), "test", nil)
	if err == nil {
		t.Fatal("Execute() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("error = %v, want 'not configured'", err)
	}
}
