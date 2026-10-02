package interactive

import (
	"fmt"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// legacyReplayBatchID synthesises a deterministic batch ID for a delegation
// that predates admission metadata and has no ledger entry. messageIndex is
// the index of the assistant message holding the call (or, for a result
// envelope with no matching call, of the user message carrying it).
func legacyReplayBatchID(messageIndex int) string {
	return fmt.Sprintf("replay#%d", messageIndex)
}

// replayOccurrenceFor resolves the occurrence stamped on every replayed
// delegation event for one call. Each field comes from the accepted admission
// first, then the outstanding-sub-agent ledger entry, then legacy synthesis
// (the batch from messageIndex, the agent from fallbackAgentID).
func replayOccurrenceFor(callID string, messageIndex int, admission *tool.DelegationAdmission, entry agent.SubAgentLedgerEntry, fallbackAgentID string) output.DelegationOccurrence {
	occ := output.DelegationOccurrence{CallID: callID}
	if admission != nil && admission.Status == tool.DelegationAdmissionAccepted {
		occ.BatchID, occ.AgentID = admission.BatchID, admission.AgentID
	}
	if occ.BatchID == "" {
		occ.BatchID = entry.BatchID
	}
	if occ.AgentID == "" {
		occ.AgentID = entry.AgentID
	}
	if occ.BatchID == "" {
		occ.BatchID = legacyReplayBatchID(messageIndex)
	}
	if occ.AgentID == "" {
		occ.AgentID = fallbackAgentID
	}
	return occ
}
