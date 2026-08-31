package agent

import (
	"encoding/json"
	"errors"
)

// DelegationResultEnvelope is the provider-facing result of a delegated tool.
// Output is preserved exactly; the other fields are present only when useful.
type DelegationResultEnvelope struct {
	Output       string                  `json:"output"`
	Status       string                  `json:"status,omitempty"`
	Reason       string                  `json:"reason,omitempty"`
	Continuation *DelegationContinuation `json:"continuation,omitempty"`
}

// DelegationContinuation identifies a child session that can be resumed.
type DelegationContinuation struct {
	AgentID string `json:"agent_id"`
}

// ToolResultProjector lets a result owner define its provider-facing projection.
type ToolResultProjector interface {
	ProjectToolResult() DelegationResultEnvelope
}

// ToolErrorProjector lets an error owner define its provider-facing projection.
type ToolErrorProjector interface {
	ProjectToolError() DelegationResultEnvelope
}

func projectedToolResult(value any) (string, bool) {
	projector, ok := value.(ToolResultProjector)
	if !ok {
		return "", false
	}
	data, err := json.Marshal(projector.ProjectToolResult())
	if err != nil {
		return "", false
	}
	return string(data), true
}

func projectedToolError(err error) (string, bool) {
	var projector ToolErrorProjector
	if !errors.As(err, &projector) {
		return "", false
	}
	data, marshalErr := json.Marshal(projector.ProjectToolError())
	if marshalErr != nil {
		return "", false
	}
	return string(data), true
}
