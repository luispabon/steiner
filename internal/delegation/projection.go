package delegation

import "github.com/luispabon/steiner/internal/agent"

// SetupError marks an approved child setup failure for provider-safe projection.
type SetupError struct{ err error }

func (e *SetupError) Error() string { return e.err.Error() }
func (e *SetupError) Unwrap() error { return e.err }

// ProjectToolError returns the compact provider-facing setup failure.
func (e *SetupError) ProjectToolError() agent.DelegationResultEnvelope {
	return agent.DelegationResultEnvelope{Output: "", Status: "failed", Reason: "child setup failed"}
}

func childSetupError(err error) error {
	if err == nil {
		return nil
	}
	return &SetupError{err: err}
}
