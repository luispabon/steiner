package agent

import (
	"errors"
	"strings"
	"testing"
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
