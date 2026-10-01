package tool

import (
	"context"
	"errors"
	"fmt"
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

func TestDelegationAdmissionAuthoritativeMetadataWins(t *testing.T) {
	for _, status := range []string{DelegationAdmissionAccepted, DelegationAdmissionRejected} {
		t.Run(status, func(t *testing.T) {
			provided := &DelegationAdmission{Status: status, BatchID: "authoritative"}
			registry := NewRegistry(ToolDef{Name: "delegate", IsDelegation: true, Handler: func(context.Context, map[string]any) (any, error) {
				return ExecutionResult{Value: "done", DelegationAdmission: provided}, nil
			}})
			result, err := NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", Unsandboxed{}).Execute(context.Background(), "delegate", "call", nil)
			if err != nil {
				t.Fatal(err)
			}
			got := result.(ExecutionResult).DelegationAdmission
			if got == nil || got.Status != status || got.BatchID != "authoritative" {
				t.Fatalf("admission = %#v", got)
			}
		})
	}
}

func TestDelegationAdmissionAcceptedSurvivesError(t *testing.T) {
	provided := &DelegationAdmission{Status: DelegationAdmissionAccepted, AgentID: "agent"}
	registry := NewRegistry(ToolDef{Name: "delegate", IsDelegation: true, Handler: func(context.Context, map[string]any) (any, error) {
		return ExecutionResult{DelegationAdmission: provided}, errors.New("post-admission failure")
	}})
	_, err := NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", Unsandboxed{}).Execute(context.Background(), "delegate", "call", nil)
	var toolErr *ToolExecutionError
	if errors.As(err, &toolErr) {
		t.Fatalf("unexpected ToolExecutionError %v", err)
	}
	admission := delegationAdmissionFromError(err)
	if admission == nil || admission.Status != DelegationAdmissionAccepted || admission.AgentID != "agent" {
		t.Fatalf("admission = %#v, err = %v", admission, err)
	}
}

func TestDelegationAdmissionRejectedSuccessSurvives(t *testing.T) {
	registry := NewRegistry(ToolDef{Name: "delegate", IsDelegation: true, Handler: func(context.Context, map[string]any) (any, error) {
		return ExecutionResult{Value: "no admission", DelegationAdmission: &DelegationAdmission{Status: DelegationAdmissionRejected}}, nil
	}})
	result, err := NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", Unsandboxed{}).Execute(context.Background(), "delegate", "call", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := result.(ExecutionResult).DelegationAdmission; got == nil || got.Status != DelegationAdmissionRejected {
		t.Fatalf("admission = %#v", got)
	}
}

func TestDelegationAdmissionDoesNotMutateSharedWrappedToolError(t *testing.T) {
	for _, wrap := range []struct {
		name string
		make func(error) error
	}{
		{name: "wrapped", make: func(err error) error { return fmt.Errorf("wrapped: %w", err) }},
		{name: "joined", make: func(err error) error { return errors.Join(errors.New("other"), err) }},
	} {
		t.Run(wrap.name, func(t *testing.T) {
			cause := &ToolExecutionError{Tool: "delegate", Kind: "provider", Message: "failed"}
			sharedErr := wrap.make(cause)
			calls := 0
			registry := NewRegistry(ToolDef{Name: "delegate", IsDelegation: true, Handler: func(context.Context, map[string]any) (any, error) {
				calls++
				agent := "agentA"
				if calls == 2 {
					agent = "agentB"
				}
				return ExecutionResult{DelegationAdmission: &DelegationAdmission{Status: DelegationAdmissionAccepted, AgentID: agent}}, sharedErr
			}})
			executor := NewExecutor(registry, config.Config{}, nil, t.TempDir(), "", Unsandboxed{})
			_, firstErr := executor.Execute(context.Background(), "delegate", "first", nil)
			_, secondErr := executor.Execute(context.Background(), "delegate", "second", nil)
			if !errors.Is(firstErr, cause) || !errors.Is(secondErr, cause) {
				t.Fatal("wrapped errors do not preserve errors.Is")
			}
			for _, check := range []struct {
				err   error
				agent string
			}{{firstErr, "agentA"}, {secondErr, "agentB"}} {
				got := delegationAdmissionFromError(check.err)
				if got == nil || got.AgentID != check.agent {
					t.Errorf("admission = %#v, want agent %s", got, check.agent)
				}
			}
			var original *ToolExecutionError
			if !errors.As(sharedErr, &original) || original.DelegationAdmission != nil {
				t.Fatalf("shared cause admission changed: %#v", original)
			}
			var projected *ToolExecutionError
			if !errors.As(firstErr, &projected) || projected.Kind != "provider" || projected.DelegationAdmission.AgentID != "agentA" {
				t.Fatalf("first projected error = %#v", projected)
			}
		})
	}
}

func TestWithDelegationAdmissionDoesNotMutateSharedToolError(t *testing.T) {
	cause := &ToolExecutionError{Tool: "delegate", Kind: "provider", Message: "failed"}
	metadata := &DelegationAdmission{Status: DelegationAdmissionAccepted, AgentID: "agent-a"}
	wrapped := WithDelegationAdmission(fmt.Errorf("wrapped: %w", cause), metadata)
	metadata.AgentID = "changed"
	var projected *ToolExecutionError
	if !errors.As(wrapped, &projected) || projected.Kind != "provider" || projected.DelegationAdmission == nil || projected.DelegationAdmission.AgentID != "agent-a" {
		t.Fatalf("projected error = %#v", projected)
	}
	if cause.DelegationAdmission != nil {
		t.Fatalf("shared cause was mutated: %#v", cause.DelegationAdmission)
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
