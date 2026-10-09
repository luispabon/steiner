package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
)

func TestExecutorRejectsUnparsedInputBeforeHandlerAndApproval(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  ToolDef
	}{
		{name: "builtin", def: ToolDef{Name: "read"}},
		{name: "mcp", def: ToolDef{Name: "third_party", MCP: MCPProvenance{Server: "server", ToolName: "read"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			tc.def.Handler = func(context.Context, map[string]any) (any, error) {
				called = true
				return "executed", nil
			}
			approver := &testUnparsedApprover{}
			executor := NewExecutor(NewRegistry(tc.def), config.Config{}, approver, t.TempDir(), "", Unsandboxed{})
			input := map[string]any{
				provider.UnparsedToolInputKey: map[string]any{"raw": `{"path":`, "len": float64(8)},
			}
			_, err := executor.Execute(context.Background(), tc.def.Name, "call-unparsed", input)
			if err == nil {
				t.Fatal("Execute() error = nil, want reserved input error")
			}
			var toolErr *ToolExecutionError
			if !errors.As(err, &toolErr) || toolErr.Kind != "invalid_tool_input" {
				t.Fatalf("error = %T %v, want invalid_tool_input", err, err)
			}
			if called {
				t.Fatal("handler was invoked for reserved unparsed input")
			}
			if approver.called {
				t.Fatal("approval was requested for reserved unparsed input")
			}
		})
	}
}

func TestExecutorNormalInputStillInvokesHandler(t *testing.T) {
	called := false
	reg := NewRegistry(ToolDef{
		Name: "read",
		Handler: func(context.Context, map[string]any) (any, error) {
			called = true
			return "ok", nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "", Unsandboxed{})
	if _, err := executor.Execute(context.Background(), "read", "call-valid", map[string]any{"path": "x"}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("handler was not invoked for normal input")
	}
}

func TestExecutorOnlyRejectsExactUnparsedShape(t *testing.T) {
	called := false
	reg := NewRegistry(ToolDef{
		Name: "read",
		Handler: func(context.Context, map[string]any) (any, error) {
			called = true
			return "ok", nil
		},
	})
	executor := NewExecutor(reg, config.Config{}, nil, t.TempDir(), "", Unsandboxed{})
	input := map[string]any{
		provider.UnparsedToolInputKey: map[string]any{"raw": `{"path":`, "len": float64(8), "extra": true},
	}
	if _, err := executor.Execute(context.Background(), "read", "call-extra", input); err != nil {
		t.Fatalf("Execute() error = %v, want normal handler path", err)
	}
	if !called {
		t.Fatal("handler was not invoked for non-sentinel shape")
	}
}

type testUnparsedApprover struct{ called bool }

func (a *testUnparsedApprover) RequestApproval(context.Context, ApprovalRequest) error {
	a.called = true
	return nil
}
