package delegation

import "github.com/luispabon/steiner/internal/output"

// jobOccurrence is the delegation occurrence identity of a supervised job.
func jobOccurrence(job ChildJob) output.DelegationOccurrence {
	return output.DelegationOccurrence{CallID: job.ParentCallID, BatchID: job.BatchID, AgentID: job.AgentID}
}

// specOccurrence is the delegation occurrence identity of a child spec.
func specOccurrence(spec Spec) output.DelegationOccurrence {
	return output.DelegationOccurrence{CallID: spec.ParentCallID, BatchID: spec.BatchID, AgentID: spec.AgentID}
}
