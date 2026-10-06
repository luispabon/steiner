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

func TestWithModelGuidance(t *testing.T) {
	if WithModelGuidance(nil) != nil {
		t.Fatal("WithModelGuidance(nil) is not nil")
	}
	sentinel := errors.New("sentinel")
	cause := fmt.Errorf("agent gone: %w", sentinel)
	marked := WithModelGuidance(cause)
	if marked.Error() != cause.Error() {
		t.Fatalf("Error() = %q, want unchanged %q", marked.Error(), cause.Error())
	}
	if !errors.Is(marked, sentinel) {
		t.Fatal("marker hides the wrapped cause from errors.Is")
	}
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unmarked", err: cause, want: false},
		{name: "marked", err: marked, want: true},
		{name: "wrapped marked", err: fmt.Errorf("outer: %w", marked), want: true},
		{name: "admission over marked", err: WithDelegationAdmission(marked, &DelegationAdmission{Status: DelegationAdmissionRejected}), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := IsModelGuidance(test.err); got != test.want {
				t.Fatalf("IsModelGuidance = %v, want %v", got, test.want)
			}
		})
	}
}
