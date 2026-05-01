package tool

import (
	"context"
	"errors"
	"strings"
	"testing"

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
