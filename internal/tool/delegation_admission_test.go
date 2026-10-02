package tool

import (
	"errors"
	"fmt"
	"testing"
)

func TestWithDelegationAdmissionDoesNotMutateSharedToolError(t *testing.T) {
	cause := &ToolExecutionError{Tool: "delegate", Kind: "provider", Message: "failed"}
	metadata := &DelegationAdmission{Status: DelegationAdmissionAccepted, AgentID: "agent-a"}
	wrapped := WithDelegationAdmission(fmt.Errorf("wrapped: %w", cause), metadata)
	metadata.AgentID = "changed"
	var got *ToolExecutionError
	if !errors.As(wrapped, &got) || got != cause {
		t.Fatalf("errors.As did not return the original cause: %#v", got)
	}
	if admission := DelegationAdmissionFromError(wrapped); admission == nil || admission.AgentID != "agent-a" {
		t.Fatalf("admission = %#v", admission)
	}
}

func TestDelegationAdmissionCloneAndUnknown(t *testing.T) {
	if (*DelegationAdmission)(nil).Clone() != nil {
		t.Fatal("nil metadata clone is not nil")
	}
	metadata := &DelegationAdmission{Status: DelegationAdmissionAccepted}
	cloned := metadata.Clone()
	cloned.Status = DelegationAdmissionRejected
	if metadata.Status != DelegationAdmissionAccepted {
		t.Fatal("clone aliased source metadata")
	}
}
