package delegation

import "github.com/luispabon/steiner/internal/agent"

// DelegationSetupError marks an approved child setup failure for provider-safe projection.
type DelegationSetupError struct{ err error }

func (e *DelegationSetupError) Error() string { return e.err.Error() }
func (e *DelegationSetupError) Unwrap() error { return e.err }
func (e *DelegationSetupError) ProjectToolError() agent.DelegationResultEnvelope {
	return agent.DelegationResultEnvelope{Output: "", Status: "failed", Reason: "child setup failed"}
}

func childSetupError(err error) error {
	if err == nil {
		return nil
	}
	return &DelegationSetupError{err: err}
}
