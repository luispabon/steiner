package delegation

// AgentType identifies a specialized delegate agent type.
type AgentType string

const (
	// AgentTypeExplore is the agent type for exploration tasks.
	AgentTypeExplore AgentType = "explore"
	// AgentTypeResearch is the agent type for research tasks.
	AgentTypeResearch AgentType = "research"
	// AgentTypeCode is the agent type for coding tasks.
	AgentTypeCode AgentType = "code"
	// AgentTypePlan is the agent type for planning tasks.
	AgentTypePlan AgentType = "plan"
	// AgentTypeVerify is the agent type for verification tasks.
	AgentTypeVerify AgentType = "verify"
)

// AllAgentTypes returns all valid agent type values.
func AllAgentTypes() []AgentType {
	return []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeCode, AgentTypePlan, AgentTypeVerify}
}

// ValidAgentType reports whether s is a recognized agent type name.
func ValidAgentType(s string) bool {
	for _, t := range AllAgentTypes() {
		if string(t) == s {
			return true
		}
	}
	return false
}
