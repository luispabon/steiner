package delegation

import (
	"errors"

	"github.com/luispabon/steiner/internal/agent"
)

// SetupError marks an approved child setup failure for provider-safe projection.
type SetupError struct{ err error }

func (e *SetupError) Error() string { return e.err.Error() }
func (e *SetupError) Unwrap() error { return e.err }

// ProjectToolError returns the compact provider-facing setup failure.
func (e *SetupError) ProjectToolError() agent.DelegationResultEnvelope {
	reason := "child setup failed"
	if errors.Is(e.err, ErrAgentAlreadyActive) {
		reason = "agent_id already has a call in flight — a previous dispatch or follow_up to this agent hasn't returned yet; wait for that result before sending another follow_up to the same agent_id, or continue other independent work in the meantime"
	}
	return agent.DelegationResultEnvelope{Output: "", Status: "failed", Reason: reason}
}

func childSetupError(err error) error {
	if err == nil {
		return nil
	}
	return &SetupError{err: err}
}
