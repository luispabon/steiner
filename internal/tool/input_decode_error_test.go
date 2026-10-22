package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestExecutorRejectsDecodeErrorBeforeHandlerAndApproval(t *testing.T) {
	called := false
	def := ToolDef{
		Name: "read",
		Handler: func(context.Context, map[string]any) (any, error) {
			called = true
			return "executed", nil
		},
	}
	approver := &testUnparsedApprover{}
	executor := NewExecutor(NewRegistry(def), config.Config{}, approver, t.TempDir(), "", Unsandboxed{})
	const decodeErr = `decode "read" arguments: unexpected end of JSON input`
	ctx := WithToolInputDecodeError(context.Background(), decodeErr)

	_, err := executor.Execute(ctx, "read", "call-bad", map[string]any{})
	var toolErr *ToolExecutionError
	if !errors.As(err, &toolErr) || toolErr.Kind != "invalid_tool_input" {
		t.Fatalf("error = %T %v, want invalid_tool_input", err, err)
	}
	if want := `tool call arguments could not be parsed as JSON: ` + decodeErr + `; resend the call with valid JSON arguments`; toolErr.Message != want {
		t.Fatalf("Message = %q, want %q", toolErr.Message, want)
	}
	if called {
		t.Fatal("handler was invoked for a call with a decode error")
	}
	if approver.called {
		t.Fatal("approval was requested for a call with a decode error")
	}
}

func TestWithToolInputDecodeErrorEmptyLeavesContextUnmarked(t *testing.T) {
	ctx := WithToolInputDecodeError(context.Background(), "")
	if msg, ok := toolInputDecodeError(ctx); ok {
		t.Fatalf("toolInputDecodeError() = %q, want no marker", msg)
	}
}

func TestExecuteEmitsToolRecord_InvalidToolInput(t *testing.T) {
	root := t.TempDir()
	reg := NewRegistry(ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil },
	})
	executor, diagDir := newToolStreamExecutor(t, reg, root)
	ctx := WithToolInputDecodeError(context.Background(), "decode arguments: boom")

	if _, err := executor.Execute(ctx, "read", "", map[string]any{}); err == nil {
		t.Fatal("Execute() error = nil, want invalid_tool_input")
	}

	records := readToolRecords(t, diagDir)
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	got := records[0]
	if got.Outcome != "error" || got.OpsFailed != 1 || len(got.Failures) != 1 {
		t.Fatalf("record = %+v, want outcome=error with one failure", got)
	}
	if reason := got.Failures[0].Reason; reason != "invalid_input" {
		t.Errorf("failure reason = %q, want invalid_input", reason)
	}
}
