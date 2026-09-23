package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// TestInvokeToolCarriesCallDiagnostics pins that invokeTool stamps the
// agent-loop turn and backend model onto the tool execution context, so the
// executor's tool-stream record can attribute the call.
func TestInvokeToolCarriesCallDiagnostics(t *testing.T) {
	var got tool.CallDiagnostics
	var present bool
	executor := parallelTestExecutor{fn: func(ctx context.Context, _ string) (any, error) {
		got, present = tool.CallDiagnosticsFromContext(ctx)
		return "ok", nil
	}}

	req := RunRequest{
		Executor:      executor,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Events:        output.NoopSink{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)
	if _, err := p.invokeTool(context.Background(), 3, provider.ToolCall{ID: "1", Name: "read"}); err != nil {
		t.Fatalf("invokeTool() error = %v", err)
	}

	if !present {
		t.Fatal("CallDiagnostics absent from tool context, want present")
	}
	if got.Turn != 3 || got.Model != "test-model" {
		t.Errorf("CallDiagnostics = %+v, want {Turn:3 Model:test-model}", got)
	}
}
