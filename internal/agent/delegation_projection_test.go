package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type projectionTestError struct{}

func (projectionTestError) Error() string { return "raw secret" }
func (projectionTestError) ProjectToolError() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "", Status: "failed", Reason: "child setup failed"}
}

type projectedTestResult struct{}

func (projectedTestResult) ProjectToolResult() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "exact"}
}

func TestProjectedToolErrorUsesEnvelope(t *testing.T) {
	content, ok := projectedToolError(errors.Join(projectionTestError{}, errors.New("other")))
	if !ok || content != `{"output":"","status":"failed","reason":"child setup failed"}` {
		t.Fatalf("projected error = %q, %v", content, ok)
	}
}

func TestExecutorAdmissionErrorPreservesActualProviderProjection(t *testing.T) {
	registry := tool.NewRegistry(tool.ToolDef{Name: "delegate", Handler: func(context.Context, map[string]any) (any, error) {
		return nil, projectionTestError{}
	}})
	executor := tool.NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{})
	_, err := executor.Execute(context.Background(), "delegate", "call", nil)
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	var events []output.Event
	p := newTurnProgressor(RunRequest{ParallelClassOf: delegateClassifier, Events: output.SinkFunc(func(event output.Event) { events = append(events, event) })}, prompt.AssemblyOptions{}, nil)
	message := p.buildToolMessage(1, provider.ToolCall{ID: "call", Name: "delegate"}, nil, err, nil)
	if message.Content != `{"output":"","status":"failed","reason":"child setup failed"}` {
		t.Fatalf("provider content = %s", message.Content)
	}
	if message.DelegationAdmission == nil || message.DelegationAdmission.Status != tool.DelegationAdmissionRejected {
		t.Fatalf("admission = %#v", message.DelegationAdmission)
	}
	found := false
	for _, event := range events {
		if event.Type == output.EventTypeToolCallFinished {
			finished := event.Payload.(output.ToolCallFinishedEvent)
			if finished.DelegationAdmission == nil || finished.DelegationAdmission.Status != tool.DelegationAdmissionRejected {
				t.Fatalf("finished admission = %#v", finished.DelegationAdmission)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing finished event")
	}
}

func TestAcceptedAdmissionSurvivesExecutorErrorIntoMessageAndEvent(t *testing.T) {
	registry := tool.NewRegistry(tool.ToolDef{Name: "delegate", Handler: func(context.Context, map[string]any) (any, error) {
		return tool.ExecutionResult{DelegationAdmission: &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, AgentID: "agent", BatchID: "batch", Group: "group"}}, errors.New("after admission")
	}})
	result, err := tool.NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", tool.Unsandboxed{}).Execute(context.Background(), "delegate", "call", nil)
	if err == nil {
		t.Fatal("Execute() error = nil")
	}
	var events []output.Event
	p := newTurnProgressor(RunRequest{ParallelClassOf: delegateClassifier, Events: output.SinkFunc(func(event output.Event) { events = append(events, event) })}, prompt.AssemblyOptions{}, nil)
	message := p.buildToolMessage(1, provider.ToolCall{ID: "call", Name: "delegate"}, result, err, nil)
	if message.DelegationAdmission == nil || message.DelegationAdmission.Status != tool.DelegationAdmissionAccepted {
		t.Fatalf("message admission = %#v", message.DelegationAdmission)
	}
	for _, event := range events {
		if event.Type == output.EventTypeToolCallFinished {
			finished := event.Payload.(output.ToolCallFinishedEvent)
			if finished.DelegationAdmission == nil || finished.DelegationAdmission.AgentID != "agent" {
				t.Fatalf("finished admission = %#v", finished.DelegationAdmission)
			}
			return
		}
	}
	t.Fatal("missing finished event")
}

func TestProjectedToolErrorSurvivesAdmissionWrapper(t *testing.T) {
	wrapped := errors.Join(projectionTestError{}, errors.New("other"))
	wrapped = &projectionAdmissionTestError{error: wrapped}
	content, ok := projectedToolError(wrapped)
	if !ok || content != `{"output":"","status":"failed","reason":"child setup failed"}` {
		t.Fatalf("projected error = %q, %v", content, ok)
	}
}

type projectionAdmissionTestError struct{ error }

func (e projectionAdmissionTestError) Unwrap() error { return e.error }

func (projectionAdmissionTestError) DelegationAdmissionMetadata() *tool.DelegationAdmission {
	return &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected}
}

func TestNormalizeToolResultProjectionUsesMarkerNotJSONShape(t *testing.T) {
	projected := normalizeToolResult(projectedTestResult{})
	if !projected.Projected {
		t.Fatal("projected result was not marked")
	}
	generic := normalizeToolResult(map[string]any{"output": "generic"})
	if generic.Projected {
		t.Fatal("generic JSON-shaped result was marked projected")
	}
}

type projectedResultWithWorktree struct{}

func (projectedResultWithWorktree) ProjectToolResult() DelegationResultEnvelope {
	return DelegationResultEnvelope{
		Output:       "done",
		WorktreePath: ".steiner/worktrees/abc/main/agent-1",
	}
}

func TestWorktreePathSerializesWhenPopulated(t *testing.T) {
	content, ok := projectedToolResult(projectedResultWithWorktree{})
	if !ok {
		t.Fatal("projection failed")
	}
	if !strings.Contains(content, `"worktree_path":".steiner/worktrees/abc/main/agent-1"`) {
		t.Fatalf("projected result missing worktree_path: %s", content)
	}
}

type projectedResultNoWorktree struct{}

func (projectedResultNoWorktree) ProjectToolResult() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "done"}
}

func TestWorktreePathOmittedWhenEmpty(t *testing.T) {
	content, ok := projectedToolResult(projectedResultNoWorktree{})
	if !ok {
		t.Fatal("projection failed")
	}
	if strings.Contains(content, `"worktree_path"`) {
		t.Fatalf("projected result should omit worktree_path: %s", content)
	}
}
