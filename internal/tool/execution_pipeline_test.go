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
func (s *testSandbox) TmpDir() string                      { return "" }

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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
	_, err := executor.Execute(context.Background(), "mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
		},
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, workDir, "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
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
	_, err := NewExecutor(nil, config.Config{}, nil, t.TempDir(), "").Execute(context.Background(), "test", nil)
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
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
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
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
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
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
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
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "")
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
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "").WithSandbox(&testSandbox{})
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

func TestExecuteTool_BashSSHConfigDenial_AllowRetriesOutsideSandbox(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	var callContexts []context.Context
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			callContexts = append(callContexts, ctx)
			if ctx.Value(BashUnsandboxedKey{}) == true {
				return &mockBashResult{exitCode: 0, output: "ssh config accepted"}, nil
			}
			return &mockBashResult{exitCode: 1, output: "Bad owner or permissions on /etc/ssh/ssh_config.d/10-main.conf"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "ssh -G github.com"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if !approver.called {
		t.Fatal("approver.RequestApproval() was not called, want called")
	}
	if approver.lastReq.Reason != "SSH config rejected inside sandbox" {
		t.Fatalf("req.Reason = %q, want SSH config rejected inside sandbox", approver.lastReq.Reason)
	}
	if approver.lastReq.GrantInstructions != "Re-run outside the sandbox with --unsafe" {
		t.Fatalf("req.GrantInstructions = %q, want SSH rerun instructions", approver.lastReq.GrantInstructions)
	}
	if len(callContexts) != 2 {
		t.Fatalf("handler call count = %d, want 2 (initial + retry)", len(callContexts))
	}
	if callContexts[1].Value(BashUnsandboxedKey{}) != true {
		t.Fatal("retry context does not carry BashUnsandboxedKey")
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if br.exitCode != 0 || br.output != "ssh config accepted" {
		t.Fatalf("result = %+v, want exitCode=0 output='ssh config accepted'", br)
	}
}

func TestExecuteTool_BashSSHConfigDenial_DeniedAppendsExplanation(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &mockBashResult{exitCode: 1, output: "Bad owner or permissions on /etc/ssh/ssh_config.d/10-main.conf"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "ssh -G github.com"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if !strings.Contains(br.output, "OpenSSH rejected config ownership inside the sandbox") {
		t.Fatalf("output = %q, want SSH explanation appended", br.output)
	}
	if strings.Contains(br.output, "host_mount") {
		t.Fatalf("output = %q, want no host_mount instructions for SSH denial", br.output)
	}
}

func TestExecuteTool_BashSSHConfigSyntaxErrorNotClassified(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: true}}
	reg := NewRegistry(ToolDef{
		Name: "bash",
		Handler: func(_ context.Context, _ map[string]any) (any, error) {
			return &mockBashResult{exitCode: 1, output: "Bad configuration option: FooBar"}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, t.TempDir(), "").WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "bash", map[string]any{"command": "ssh -G github.com"})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}
	if approver.called {
		t.Fatal("approver.RequestApproval() was called, want no call for config syntax errors")
	}
	br, ok := result.(*mockBashResult)
	if !ok {
		t.Fatalf("result type = %T, want *mockBashResult", result)
	}
	if strings.Contains(br.output, "outside the sandbox") {
		t.Fatalf("output = %q, want no SSH fallback text for config syntax errors", br.output)
	}
}

// --- Built-in tool path violation tests ---

func mutateOutsideRoot(_ string) map[string]any {
	return map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "/etc/passwd", "content": "x"},
		},
	}
}

func TestNormalizeExecutionInput_PathViolation_ApproverCalled(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, approver, workDir, "").WithSandbox(&testSandbox{})
	_, err := executor.Execute(context.Background(), "mutate", mutateOutsideRoot(workDir))
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
	workDir := t.TempDir()
	var handlerInput map[string]any
	var handlerCalled bool
	reg := NewRegistry(ToolDef{
		Name: "mutate",
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			handlerCalled = true
			handlerInput = input
			effectivePolicy, ok := ctx.Value(EffectivePolicyKey{}).(*PathPolicy)
			if !ok || effectivePolicy == nil {
				t.Fatal("handler: expected EffectivePolicyKey in context after approval")
			}
			_, err := effectivePolicy.ResolvePath("/tmp/test.txt", true)
			if err != nil {
				t.Fatalf("handler: resolvePath with effective policy failed: %v", err)
			}
			return map[string]any{"ok": true}, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, approver, workDir, "").WithSandbox(&testSandbox{})
	result, err := executor.Execute(context.Background(), "mutate", mutateOutsideRoot(workDir))
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil on approval", err)
	}
	if !approver.called {
		t.Fatal("approver was not called")
	}
	if !handlerCalled {
		t.Fatal("handler was not called after approval")
	}
	if handlerInput == nil {
		t.Fatal("handler input is nil")
	}
	if result == nil {
		t.Fatal("result is nil, want content")
	}
}

func TestNormalizeExecutionInput_PathViolation_Deny(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, approver, workDir, "").WithSandbox(&testSandbox{})
	_, err := executor.Execute(context.Background(), "mutate", mutateOutsideRoot(workDir))
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

func TestNormalizeExecutionInput_PathViolation_ApprovalWithoutSandbox(t *testing.T) {
	approver := &mockApprover{response: ApprovalResponse{Allow: false}}
	reg := NewRegistry(ToolDef{
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	// No sandbox — approval must still be requested.
	executor := NewExecutor(reg, config.Config{}, approver, workDir, "")
	_, err := executor.Execute(context.Background(), "mutate", mutateOutsideRoot(workDir))
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied")
	}
	if !approver.called {
		t.Fatal("approver.RequestApproval() was not called, want called even without sandbox")
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
	executor := NewExecutor(reg, cfg, approver, workDir, "").WithSandbox(&testSandbox{})
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

func TestRunPipeline_ModeGetter_NilGetter_NoModeInContext(t *testing.T) {
	// With no mode getter, ExecutionModeKey should not be set
	reg := NewRegistry(ToolDef{
		Name: "probe",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			if _, ok := ctx.Value(ExecutionModeKey{}).(config.ExecutionMode); ok {
				t.Error("ExecutionModeKey found in context when getter is nil")
			}
			return nil, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "")
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunPipeline_ModeGetter_BuildMode_NoRestriction(t *testing.T) {
	// In build mode, writes should be unrestricted
	reg := NewRegistry(ToolDef{
		Name: "probe",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			mode, ok := ctx.Value(ExecutionModeKey{}).(config.ExecutionMode)
			if !ok {
				t.Error("ExecutionModeKey not found in context")
				return nil, nil
			}
			if mode != config.ExecutionModeBuild {
				t.Errorf("mode = %q, want build", mode)
			}
			return nil, nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "").
		WithModeGetter(func() config.ExecutionMode { return config.ExecutionModeBuild })
	_, err := executor.Execute(context.Background(), "probe", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestRunPipeline_ModeGetter_PlanMode_RestrictsWrites(t *testing.T) {
	// In plan mode, writes outside .steiner should be denied
	reg := NewRegistry(ToolDef{
		Name:    "mutate",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, nil, workDir, "").
		WithModeGetter(func() config.ExecutionMode { return config.ExecutionModePlan })
	_, err := executor.Execute(context.Background(), "mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": "src/main.go", "content": "x"},
		},
	})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied in plan mode")
	}
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
	if !strings.Contains(toolErr.Message, "plan mode") {
		t.Fatalf("error message = %q, want 'plan mode'", toolErr.Message)
	}
}

func TestRunPipeline_ModeGetter_PlanMode_AllowsSteinerWrites(t *testing.T) {
	// In plan mode, writes under .steiner should be allowed
	var handlerCalled bool
	reg := NewRegistry(ToolDef{
		Name: "mutate",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			handlerCalled = true
			policy, ok := ctx.Value(EffectivePolicyKey{}).(*PathPolicy)
			if !ok || policy == nil {
				t.Error("EffectivePolicyKey not in context")
				return nil, nil
			}
			// Verify that the handler sees the restricted policy
			_, err := policy.ResolvePath(".steiner/plans/test.md", true)
			if err != nil {
				t.Errorf("ResolvePath .steiner/plans in plan mode failed: %v", err)
			}
			return map[string]any{"ok": true}, nil
		},
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, nil, workDir, "").
		WithModeGetter(func() config.ExecutionMode { return config.ExecutionModePlan })
	result, err := executor.Execute(context.Background(), "mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": ".steiner/plans/test.md", "content": "# Plan"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil for .steiner write in plan mode", err)
	}
	if !handlerCalled {
		t.Error("handler not called")
	}
	if result == nil {
		t.Error("result is nil")
	}
}

func TestRunPipeline_ModeGetter_PlanMode_AllowsSteinerSubdirs(t *testing.T) {
	// In plan mode, writes to any .steiner subdir should be allowed
	reg := NewRegistry(ToolDef{
		Name: "mutate",
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			policy, ok := ctx.Value(EffectivePolicyKey{}).(*PathPolicy)
			if !ok || policy == nil {
				t.Error("EffectivePolicyKey not in context")
				return nil, nil
			}
			// Test multiple .steiner subdirectories
			testPaths := []string{
				".steiner/home/note.txt",
				".steiner/bookkeeping/log.txt",
				".steiner",
			}
			for _, path := range testPaths {
				_, err := policy.ResolvePath(path, true)
				if err != nil {
					t.Errorf("ResolvePath %s in plan mode failed: %v", path, err)
				}
			}
			return map[string]any{"ok": true}, nil
		},
	})
	workDir := t.TempDir()
	executor := NewExecutor(reg, config.Config{}, nil, workDir, "").
		WithModeGetter(func() config.ExecutionMode { return config.ExecutionModePlan })
	_, err := executor.Execute(context.Background(), "mutate", map[string]any{
		"operations": []any{
			map[string]any{"type": "write", "path": ".steiner/home/note.txt", "content": "note"},
		},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v, want nil for .steiner/* writes in plan mode", err)
	}
}
