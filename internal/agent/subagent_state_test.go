package agent

import (
	"testing"
)

// mockDelegationSpec implements the DelegationSpec interface for testing
type mockDelegationSpec struct {
	agentID string
}

func (m mockDelegationSpec) GetAgentID() string { return m.agentID }

func TestNewSubAgentState(t *testing.T) {
	spec := mockDelegationSpec{agentID: "test-agent-123"}

	state := NewSubAgentState(spec)

	if state == nil {
		t.Fatal("NewSubAgentState returned nil")
	}
	if state.AgentID != "test-agent-123" {
		t.Errorf("AgentID=%q, want %q", state.AgentID, "test-agent-123")
	}
	if state.Spec.GetAgentID() != "test-agent-123" {
		t.Errorf("Spec.GetAgentID()=%q, want %q", state.Spec.GetAgentID(), "test-agent-123")
	}
	if state.TurnCount != 0 {
		t.Errorf("TurnCount=%d, want 0", state.TurnCount)
	}
	if state.TokenCount != 0 {
		t.Errorf("TokenCount=%d, want 0", state.TokenCount)
	}
}

func TestSubAgentStateNoMutableRefs(t *testing.T) {
	originalSpec := mockDelegationSpec{agentID: "agent-1"}
	spec := originalSpec

	state := NewSubAgentState(spec)

	// Verify that the state captured the spec at initialization
	if state.Spec.GetAgentID() != "agent-1" {
		t.Errorf("state.Spec.GetAgentID()=%q, want %q", state.Spec.GetAgentID(), "agent-1")
	}

	// Verify state is not sharing a mutable reference
	if state.AgentID != originalSpec.agentID {
		t.Errorf("state.AgentID differs from original spec")
	}
}

func TestSubAgentStateFields(t *testing.T) {
	spec := mockDelegationSpec{agentID: "test-agent"}

	state := NewSubAgentState(spec)

	if state.AgentID != "test-agent" {
		t.Errorf("AgentID=%q, want %q", state.AgentID, "test-agent")
	}
	if state.TurnCount != 0 {
		t.Errorf("TurnCount=%d, want 0", state.TurnCount)
	}
	if state.TokenCount != 0 {
		t.Errorf("TokenCount=%d, want 0", state.TokenCount)
	}
}

func TestNewSubAgentStateWithNilSpec(t *testing.T) {
	state := NewSubAgentState(nil)

	if state == nil {
		t.Fatal("NewSubAgentState returned nil")
	}
	if state.AgentID != "" {
		t.Errorf("AgentID=%q, want empty string", state.AgentID)
	}
	if state.Spec != nil {
		t.Errorf("Spec=%v, want nil", state.Spec)
	}
}
