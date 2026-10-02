package tool

import "errors"

// DelegationAdmissionAccepted and DelegationAdmissionRejected are admission statuses.
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

func (e *delegationAdmissionError) As(target any) bool {
	toolErrTarget, ok := target.(**ToolExecutionError)
	if !ok {
		return false
	}
	var toolErr *ToolExecutionError
	if !errors.As(e.err, &toolErr) {
		return false
	}
	cloned := *toolErr
	cloned.DelegationAdmission = e.metadata.Clone()
	*toolErrTarget = &cloned
	return true
}

// WithDelegationAdmission returns an error carrying a cloned admission outcome.
func WithDelegationAdmission(err error, metadata *DelegationAdmission) error {
	if err == nil || metadata == nil {
		return err
	}
	return &delegationAdmissionError{err: err, metadata: metadata.Clone()}
}

// DelegationAdmissionFromError extracts a cloned admission outcome attached to
// err, or nil when none is attached.
func DelegationAdmissionFromError(err error) *DelegationAdmission {
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
