package delegation

import (
	"context"
	"errors"
	"testing"

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
		{name: "prepare failure", wantErr: true, prepare: func() ChildJob {
			return ChildJob{Prepare: func(context.Context) (CodeWorktree, error) { return CodeWorktree{}, failure }}
		}},
		{name: "execute failure", wantErr: true, prepare: func() ChildJob {
			return ChildJob{Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, failure }}
		}},
		{name: "controller registration failure", wantErr: true, prepare: func() ChildJob { return ChildJob{} }},
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
			got := recv(t, spawn(batchCtx("captured-batch"), s, job), "job result")
			if (got.err != nil) != tt.wantErr || (got.err != nil && tt.name != "controller registration failure" && !errors.Is(got.err, failure)) {
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
	if err == nil || rejected == nil || rejected.Status != tool.DelegationAdmissionRejected || rejected.Group != "g" {
		t.Fatalf("rejected = %+v, %v", rejected, err)
	}
}
