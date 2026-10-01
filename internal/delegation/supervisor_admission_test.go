package delegation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestSupervisorAcceptedAdmissionSurvivesOutcomes(t *testing.T) {
	failure := errors.New("child failed")
	tests := []struct {
		name    string
		prepare func() ChildJob
		wantErr bool
	}{
		{name: "success", prepare: func() ChildJob {
			return ChildJob{Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{Value: "done"}, nil }}
		}},
		{name: "empty success", prepare: func() ChildJob {
			return ChildJob{Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }}
		}},
		{name: "prepare failure", wantErr: true, prepare: func() ChildJob {
			return ChildJob{Prepare: func(context.Context) (CodeWorktree, error) { return CodeWorktree{}, failure }}
		}},
		{name: "execute failure", wantErr: true, prepare: func() ChildJob {
			return ChildJob{Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, failure }}
		}},
		{name: "controller registration failure", wantErr: true, prepare: func() ChildJob { return ChildJob{} }},
		{name: "shutdown error", wantErr: true, prepare: func() ChildJob {
			return ChildJob{Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, controller := newTestSupervisor(1, 0)
			job := tt.prepare()
			job.AgentID, job.Group = "captured-agent", " g "
			if tt.name == "controller registration failure" {
				if err := controller.RegisterWithCancel("captured-agent", func() {}, AgentTypeCode, CodeWorktree{}); err != nil {
					t.Fatal(err)
				}
			}
			job.GroupScope = s.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
			if tt.name == "shutdown error" {
				s.joinTimeout = time.Millisecond
				job.Execute = func(context.Context) (tool.ExecutionResult, error) {
					<-time.After(20 * time.Millisecond)
					return tool.ExecutionResult{}, nil
				}
			}
			waiter := spawn(batchCtx("captured-batch"), s, job)
			if tt.name == "shutdown error" {
				waitOutstanding(t, s, 1)
				s.Shutdown(context.Background(), CancelCauseSystem)
			}
			got := recv(t, waiter, "job result")
			if (got.err != nil) != tt.wantErr || (got.err != nil && tt.name != "controller registration failure" && tt.name != "shutdown error" && !errors.Is(got.err, failure)) {
				t.Fatalf("error = %v", got.err)
			}
			want := &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, BatchID: "captured-batch", Group: "g", AgentID: "captured-agent"}
			if got.result.DelegationAdmission == nil || *got.result.DelegationAdmission != *want {
				t.Fatalf("admission = %+v, want %+v", got.result.DelegationAdmission, want)
			}
			if got.err != nil {
				var carrier tool.DelegationAdmissionCarrier
				if !errors.As(got.err, &carrier) || *carrier.DelegationAdmissionMetadata() != *want {
					t.Fatalf("error admission = %+v, want %+v", carrier, want)
				}
			}
		})
	}
}

func TestSpawnWithAdmissionCapturesAcceptedAndRejected(t *testing.T) {
	s, _ := newTestSupervisor(1, 0)
	job := ChildJob{AgentID: "a", Group: " g ", Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }}
	_, got, err := s.SpawnWithAdmission(batchCtx("b"), job)
	if err != nil || got == nil || got.Status != tool.DelegationAdmissionAccepted || got.BatchID != "b" || got.Group != "g" || got.AgentID != "a" {
		t.Fatalf("accepted = %+v, %v", got, err)
	}
	_, rejected, err := s.SpawnWithAdmission(batchCtx("b"), job)
	want := &tool.DelegationAdmission{Status: tool.DelegationAdmissionRejected, BatchID: "b", Group: "g", AgentID: "a"}
	var carrier tool.DelegationAdmissionCarrier
	if err == nil || rejected == nil || *rejected != *want || !errors.As(err, &carrier) || *carrier.DelegationAdmissionMetadata() != *want {
		t.Fatalf("rejected = %+v, err = %v", rejected, err)
	}
}

func TestSupervisorAdmissionDoesNotMutateSharedToolExecutionError(t *testing.T) {
	for _, wrap := range []struct {
		name string
		make func(error) error
	}{
		{name: "wrapped", make: func(err error) error { return fmt.Errorf("wrapped: %w", err) }},
		{name: "joined", make: func(err error) error { return errors.Join(errors.New("other"), err) }},
	} {
		t.Run(wrap.name, func(t *testing.T) {
			cause := &tool.ToolExecutionError{Tool: "delegate", Kind: "provider", Message: "failed"}
			shared := wrap.make(cause)
			s, _ := newTestSupervisor(2, 0)
			var calls sync.Mutex
			count := 0
			makeJob := func(id string) ChildJob {
				return ChildJob{AgentID: id, Execute: func(context.Context) (tool.ExecutionResult, error) {
					calls.Lock()
					count++
					calls.Unlock()
					return tool.ExecutionResult{}, shared
				}}
			}
			first := spawn(batchCtx("batch-a"), s, makeJob("agent-a"))
			second := spawn(batchCtx("batch-b"), s, makeJob("agent-b"))
			outcomes := []spawnResult{recv(t, first, "first"), recv(t, second, "second")}
			for i, outcome := range outcomes {
				if !errors.Is(outcome.err, cause) {
					t.Fatalf("wrapped cause not preserved: %T %v (shared %T %v)", outcome.err, outcome.err, shared, shared)
				}
				var projected *tool.ToolExecutionError
				if !errors.As(outcome.err, &projected) || projected.Kind != "provider" {
					t.Fatalf("projected error = %#v", projected)
				}
				want := &tool.DelegationAdmission{Status: tool.DelegationAdmissionAccepted, BatchID: []string{"batch-a", "batch-b"}[i], AgentID: []string{"agent-a", "agent-b"}[i]}
				if projected.DelegationAdmission == nil || *projected.DelegationAdmission != *want {
					t.Fatalf("projected admission = %+v, want %+v", projected.DelegationAdmission, want)
				}
			}
			if cause.DelegationAdmission != nil {
				t.Fatalf("shared cause was mutated: %+v", cause.DelegationAdmission)
			}
		})
	}
}
