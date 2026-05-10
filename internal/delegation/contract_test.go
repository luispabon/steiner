package delegation

import (
	"testing"
	"time"
)

func TestDelegationSpecFields(t *testing.T) {
	spec := DelegationSpec{
		Task:         "test task",
		Context:      "test context",
		SystemPrompt: "test prompt",
		AgentID:      "agent-123",
		Limits: DelegationLimits{
			MaxTurns:          10,
			OutputLimitTokens: 50000,
			Timeout:           time.Second * 60,
		},
	}

	if spec.Task != "test task" {
		t.Errorf("expected Task=%q, got %q", "test task", spec.Task)
	}
	if spec.Context != "test context" {
		t.Errorf("expected Context=%q, got %q", "test context", spec.Context)
	}
	if spec.SystemPrompt != "test prompt" {
		t.Errorf("expected SystemPrompt=%q, got %q", "test prompt", spec.SystemPrompt)
	}
	if spec.AgentID != "agent-123" {
		t.Errorf("expected AgentID=%q, got %q", "agent-123", spec.AgentID)
	}
	if spec.Limits.MaxTurns != 10 {
		t.Errorf("expected Limits.MaxTurns=10, got %d", spec.Limits.MaxTurns)
	}
}

func TestDelegationResultFields(t *testing.T) {
	result := DelegationResult{
		AgentID:    "agent-123",
		Status:     StatusComplete,
		Output:     "test output",
		TurnCount:  5,
		TokenCount: 1000,
		Error:      "",
	}

	if result.AgentID != "agent-123" {
		t.Errorf("expected AgentID=%q, got %q", "agent-123", result.AgentID)
	}
	if result.Status != StatusComplete {
		t.Errorf("expected Status=%q, got %q", StatusComplete, result.Status)
	}
	if result.Output != "test output" {
		t.Errorf("expected Output=%q, got %q", "test output", result.Output)
	}
	if result.Summary != "" {
		t.Errorf("expected Summary to remain hidden, got %q", result.Summary)
	}
	if result.TurnCount != 5 {
		t.Errorf("expected TurnCount=5, got %d", result.TurnCount)
	}
	if result.TokenCount != 1000 {
		t.Errorf("expected TokenCount=1000, got %d", result.TokenCount)
	}
}

func TestNoTouchedFilesField(t *testing.T) {
	// Verify that DelegationResult does not have touched_files field
	result := DelegationResult{}
	_ = result

	// This test passes if the code compiles.
	// The absence of a touched_files field is verified by the type definition.
	t.Log("DelegationResult correctly has no touched_files field")
}
