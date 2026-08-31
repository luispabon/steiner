package agent

import (
	"errors"
	"testing"
)

type projectionTestError struct{}

func (projectionTestError) Error() string { return "raw secret" }
func (projectionTestError) ProjectToolError() DelegationResultEnvelope {
	return DelegationResultEnvelope{Output: "", Status: "failed", Reason: "child setup failed"}
}

func TestProjectedToolErrorUsesEnvelope(t *testing.T) {
	content, ok := projectedToolError(errors.Join(projectionTestError{}, errors.New("other")))
	if !ok || content != `{"output":"","status":"failed","reason":"child setup failed"}` {
		t.Fatalf("projected error = %q, %v", content, ok)
	}
}

func TestProjectedDelegationResultIsNotContextShaped(t *testing.T) {
	content := `{"output":"  exact\n☃  ","continuation":{"agent_id":"child-1"}}`
	if !isProjectedDelegationResult(content) {
		t.Fatal("projected envelope not recognized")
	}
}
