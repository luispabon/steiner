package agent

// DelegationSpec interface avoids circular imports with internal/delegation.
type DelegationSpec interface {
	GetAgentID() string
}

// SubAgentState tracks the execution state of a delegated child agent.
type SubAgentState struct {
	// AgentID is the unique identifier for this delegation.
	AgentID string

	// Spec is the delegation specification that spawned this child.
	Spec DelegationSpec

	// TurnCount is the number of turns executed by the child.
	TurnCount int

	// TokenCount is the total tokens consumed by the child.
	TokenCount int
}

// NewSubAgentState creates a new SubAgentState from a DelegationSpec.
func NewSubAgentState(spec DelegationSpec) *SubAgentState {
	agentID := ""
	if spec != nil {
		agentID = spec.GetAgentID()
	}
	return &SubAgentState{
		AgentID:    agentID,
		Spec:       spec,
		TurnCount:  0,
		TokenCount: 0,
	}
}
