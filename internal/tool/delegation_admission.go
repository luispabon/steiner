package tool

import "errors"

const (
	DelegationAdmissionAccepted = "accepted"
	DelegationAdmissionRejected = "rejected"
)

// DelegationAdmission records authoritative admission metadata for one call.
type DelegationAdmission struct {
	Status       string `json:"status"`
	BatchID      string `json:"batch_id"`
	Group        string `json:"group"`
	AgentID      string `json:"agent_id"`
	PolicyNotice bool   `json:"policy_notice"`
}

// Clone returns an independent copy of the admission metadata.
func (m *DelegationAdmission) Clone() *DelegationAdmission {
	if m == nil {
		return nil
	}
	cloned := *m
	return &cloned
}

// DelegationAdmissionCarrier exposes metadata attached to an error.
type DelegationAdmissionCarrier interface {
	DelegationAdmissionMetadata() *DelegationAdmission
}

type delegationAdmissionError struct {
	err      error
	metadata *DelegationAdmission
}

func (e *delegationAdmissionError) Error() string { return e.err.Error() }
func (e *delegationAdmissionError) Unwrap() error { return e.err }
func (e *delegationAdmissionError) DelegationAdmissionMetadata() *DelegationAdmission {
	return e.metadata.Clone()
}

func withDelegationAdmission(err error, metadata *DelegationAdmission) error {
	if err == nil || metadata == nil {
		return err
	}
	return &delegationAdmissionError{err: err, metadata: metadata.Clone()}
}

func delegationAdmissionFromError(err error) *DelegationAdmission {
	var toolErr *ToolExecutionError
	if errors.As(err, &toolErr) && toolErr.DelegationAdmission != nil {
		return toolErr.DelegationAdmission.Clone()
	}
	var carrier DelegationAdmissionCarrier
	if errors.As(err, &carrier) {
		return carrier.DelegationAdmissionMetadata()
	}
	return nil
}
