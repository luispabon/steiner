package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/config"
)

func TestDelegationAdmissionMetadataOnExecutorOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler func(context.Context, map[string]any) (any, error)
		status  string
		notice  bool
	}{
		{name: "accepted", handler: func(context.Context, map[string]any) (any, error) { return "ok", nil }, status: DelegationAdmissionAccepted},
		{name: "rejected", handler: func(context.Context, map[string]any) (any, error) { return nil, errors.New("failure") }, status: DelegationAdmissionRejected},
		{name: "policy", handler: func(context.Context, map[string]any) (any, error) {
			return nil, policyDeniedError("delegate", errors.New("blocked"))
		}, status: DelegationAdmissionRejected, notice: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry(ToolDef{Name: "delegate", IsDelegation: true, Handler: test.handler})
			result, err := NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", Unsandboxed{}).Execute(context.Background(), "delegate", "call", nil)
			var admission *DelegationAdmission
			if err != nil {
				var toolErr *ToolExecutionError
				if errors.As(err, &toolErr) {
					admission = toolErr.DelegationAdmission
				} else {
					admission = delegationAdmissionFromError(err)
				}
			}
			if execution, ok := result.(ExecutionResult); ok {
				admission = execution.DelegationAdmission
			}
			if admission == nil || admission.Status != test.status || admission.PolicyNotice != test.notice {
				t.Fatalf("admission = %#v, err = %v", admission, err)
			}
		})
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
