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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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

func TestExecuteToolHandlerSuccess(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name: "greeter",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return map[string]any{"message": "hello"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, nil, workDir)
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
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir())
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
