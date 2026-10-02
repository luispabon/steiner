package tui

import "github.com/luispabon/steiner/internal/output"

// testBatchID is the tool batch every test occurrence belongs to unless a test
// needs distinct batches.
const testBatchID = "batch"

// callOcc is the occurrence of agentID's delegation for parent call callID.
func callOcc(callID, agentID string) output.DelegationOccurrence {
	return output.DelegationOccurrence{BatchID: testBatchID, CallID: callID, AgentID: agentID}
}

// agentOcc is the occurrence of agentID's delegation for a parent call that
// tests without a parent tool call name after the agent.
func agentOcc(agentID string) output.DelegationOccurrence {
	return callOcc("call-"+agentID, agentID)
}
