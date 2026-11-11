package interactive

import "strconv"

// ApprovalIdentity returns the coordinator identity for one approval request.
// Parent requests keep their provider call ID, so existing FIFO matching is
// unchanged. Child requests qualify that call ID with their agent scope using a
// length-prefixed encoding, which stays injective for arbitrary agent IDs and
// call IDs (including separator characters) and keeps identical provider call
// IDs from different agents distinct. An empty call ID yields an empty
// identity, so an uncorrelatable request never becomes promptable.
func ApprovalIdentity(agentID, callID string) string {
	if callID == "" {
		return ""
	}
	if agentID == "" {
		return callID
	}
	return strconv.Itoa(len(agentID)) + ":" + agentID + ":" + callID
}
