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
	var reservationErr *groupReservationError
	switch {
	case errors.As(e.err, &reservationErr):
		reason = reservationErr.correctiveReason()
	case errors.Is(e.err, ErrAgentAlreadyActive):
		reason = "agent_id already has a call in flight: wait for that result before sending another follow_up to the same agent_id; end your turn unless you have an action outside pending scope that needs no pending result, and the harness resumes you when the result arrives"
	case errors.Is(e.err, ErrCodeWorktreeRequiresCommit):
		reason = "worktree-isolated sub-agent requires a Git repository with at least one commit; commit the project files and retry"
	}
	return agent.DelegationResultEnvelope{Output: "", Status: "failed", Reason: reason}
}

func childSetupError(err error) error {
	if err == nil {
		return nil
	}
	return &SetupError{err: err}
}
