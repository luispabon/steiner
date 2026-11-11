package interactive

import "strconv"

// ApprovalIdentity returns the coordinator identity for one approval request.
// Every request, parent or child, is encoded as its agent scope length, the
// scope, and the provider call ID, so the mapping is injective for arbitrary
// agent and call IDs (including separator characters) and a provider call ID
// that happens to look like an encoded identity cannot collide with a scoped
// request. The provider call ID carried by approval events is never rewritten;
// only the coordinator key is encoded. An empty call ID yields an empty
// identity, so an uncorrelatable request never becomes promptable.
func ApprovalIdentity(agentID, callID string) string {
	if callID == "" {
		return ""
	}
	return strconv.Itoa(len(agentID)) + ":" + agentID + ":" + callID
}
