package tool

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
)

// mockBashResult implements BashDenialResult for testing.
type mockBashResult struct {
	exitCode int
	output   string
}

func (r *mockBashResult) BashExitCode() int     { return r.exitCode }
func (r *mockBashResult) BashOutput() string    { return r.output }
func (r *mockBashResult) AppendOutput(s string) { r.output += s }

// testSandbox satisfies SandboxWrapper for testing.
type testSandbox struct{}

func (s *testSandbox) Enabled() bool                       { return true }
func (s *testSandbox) WrapCommand(cmd *exec.Cmd) *exec.Cmd { return cmd }

// mockApprover tracks calls to RequestApproval and sends a preconfigured response.
type mockApprover struct {
	called   bool
	lastReq  ApprovalRequest
	response ApprovalResponse
	err      error
}

func (m *mockApprover) RequestApproval(_ context.Context, req ApprovalRequest) error {
	m.called = true
	m.lastReq = req
	if m.err != nil {
		return m.err
	}
	req.Response <- m.response
	return nil
}

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

// --- Sandbox denial detection tests ---

func TestExecuteTool_BashDenialDetection_ApproverCalled(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	var handlerCallCount int
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			handlerCallCount++
			return &mockBashResult{
				exitCode: 1,
				output:   "Permission denied /host/path",
			}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir()).WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "cat /host/path"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !approver.called {
		t.Fatal("approver.RequestApproval() was not called, want called")
	}
	if approver.lastReq.Reason != "command blocked by sandbox" {
		t.Fatalf("req.Reason = %q, want 'command blocked by sandbox'", approver.lastReq.Reason)
	}
	if approver.lastReq.DeniedPath != "/host/path" {
		t.Fatalf("req.DeniedPath = %q, want '/host/path'", approver.lastReq.DeniedPath)
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if !strings.Contains(br.output, "host_mount") {
		t.Fatalf("result output = %q, want grant instructions appended", br.output)
	}
	if handlerCallCount != 1 {
		t.Fatalf("handler call count = %d, want 1 (no retry on deny)", handlerCallCount)
	}
}

func TestExecuteTool_BashDenialRetry_AllowRetries(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	var callContexts []context.Context
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			callContexts = append(callContexts, ctx)
			if ctx.Value(BashUnsandboxedKey{}) == true {
				return &mockBashResult{exitCode: 0, output: "success"}, nil
			}
			return &mockBashResult{exitCode: 1, output: "Permission denied /host/path"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir()).WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "cat /host/path"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if len(callContexts) != 2 {
		t.Fatalf("handler call count = %d, want 2 (initial + retry)", len(callContexts))
	}
	// Second call must carry the unsandboxed key.
	if callContexts[1].Value(BashUnsandboxedKey{}) != true {
		t.Fatal("retry context does not carry BashUnsandboxedKey")
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if br.exitCode != 0 || br.output != "success" {
		t.Fatalf("result = %+v, want exitCode=0 output='success'", br)
	}
}

func TestExecuteTool_BashDenialNoRetry_Denied(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &mockBashResult{exitCode: 1, output: "Permission denied /secret"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir()).WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "cat /secret"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if br.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", br.exitCode)
	}
	if !strings.Contains(br.output, "host_mount") {
		t.Fatalf("output = %q, want grant instructions appended", br.output)
	}
}

func TestExecuteTool_BashNoDenialPrompt_SandboxNil(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &mockBashResult{exitCode: 1, output: "Permission denied /host/path"}, nil
		},
	})
	// No sandbox set — unsafe mode.
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir())
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "cat /host/path"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if approver.called {
		t.Fatal("approver was called, want no call when sandbox is nil")
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	// Output must not have grant instructions appended.
	if strings.Contains(br.output, "host_mount") {
		t.Fatalf("output = %q, want no grant instructions when sandbox nil", br.output)
	}
}

func TestExecuteTool_BashNoDenialPrompt_ApproverNil(t *testing.T) {
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &mockBashResult{exitCode: 1, output: "Permission denied /host/path"}, nil
		},
	})
	// Sandbox set but no approver.
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir()).WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "cat /host/path"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if strings.Contains(br.output, "host_mount") {
		t.Fatalf("output = %q, want no grant instructions when approver nil", br.output)
	}
}

// --- Built-in tool path violation tests ---

func TestNormalizeExecutionInput_PathViolation_ApproverCalled(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, approver, workDir).WithSandbox(&testSandbox{})
	_, err := executor.Execute(context.Background(), "read", map[string]any{"path": "/etc/passwd"})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied")
	}
	if !approver.called {
		t.Fatal("approver.RequestApproval() was not called, want called")
	}
	if approver.lastReq.DeniedPath != "/etc/passwd" {
		t.Fatalf("req.DeniedPath = %q, want '/etc/passwd'", approver.lastReq.DeniedPath)
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}

func TestNormalizeExecutionInput_PathViolation_Allow(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	var handlerInput map[string]any
	reg := NewRegistry(ToolDef{
		Name: "read",
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			handlerInput = input
			return map[string]any{"content": "secret"}, nil
		},
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, approver, workDir).WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "read", map[string]any{"path": "/etc/passwd"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil on approval", err)
	}
	if !approver.called {
		t.Fatal("approver was not called")
	}
	// With relaxed policy, path should pass through unchanged.
	if handlerInput == nil {
		t.Fatal("handler was not called after approval")
	}
	if result == nil {
		t.Fatal("result is nil, want content")
	}
}

func TestNormalizeExecutionInput_PathViolation_Deny(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, approver, workDir).WithSandbox(&testSandbox{})
	_, err := executor.Execute(context.Background(), "read", map[string]any{"path": "/etc/passwd"})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied after denial")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}

func TestNormalizeExecutionInput_NonPromptableError_NoPrompt(t *testing.T) {
	// Blocked paths produce non-promptable errors.
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	reg := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	cfg := config.Config{}
	cfg.Paths.BlockedPaths = []string{workDir + "/secrets"}
	executor := NewExecutor(reg, cfg, approver, workDir).WithSandbox(&testSandbox{})
	_, err := executor.Execute(context.Background(), "read", map[string]any{"path": workDir + "/secrets/key.txt"})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied")
	}
	if approver.called {
		t.Fatal("approver was called for non-promptable error, want no call")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}
