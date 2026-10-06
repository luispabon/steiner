package delegation

import (
	"context"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func withInput(base map[string]any, extra map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return merged
}

// TestDelegationDenialsModelGuidance pins which pre-dispatch denials are
// recovery guidance for the model (hidden from the TUI) and which stay visible.
func TestDelegationDenialsModelGuidance(t *testing.T) {
	t.Parallel()
	mutateTools := []provider.ToolSpec{{Function: provider.ToolFunctionSpec{Name: "mutate"}}}
	followUpDeps := func() SubAgentHandlerDeps {
		store := NewSessionStore()
		store.Save(&ChildSession{Spec: Spec{AgentID: "exhausted", AgentType: AgentTypeReview}, FollowUpCount: 1})
		store.Save(&ChildSession{Spec: Spec{AgentID: "coder", AgentType: AgentTypeCode}, Request: agent.RunRequest{Tools: mutateTools}})
		return SubAgentHandlerDeps{SessionStore: store, SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 1}}
	}
	planCtx := context.WithValue(context.Background(), tool.ExecutionModeKey{}, config.ExecutionModePlan)
	dispatch := newSubAgentDispatchHandler(SpecializedToolDeps{}, nil)
	vision := newVisionHandler(SpecializedToolDeps{ImageStore: agent.NewImageStore(t.TempDir())})

	tests := []struct {
		name string
		call func() error
		want bool
	}{
		{name: "follow_up missing agent_id", want: true, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(context.Background(), map[string]any{"message": "go"})
			return err
		}},
		{name: "follow_up missing message", want: true, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(context.Background(), map[string]any{"agent_id": "exhausted"})
			return err
		}},
		{name: "follow_up unknown session", want: true, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(context.Background(), map[string]any{"agent_id": "missing", "message": "go"})
			return err
		}},
		{name: "follow_up max follow-ups", want: true, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(context.Background(), map[string]any{"agent_id": "exhausted", "message": "go"})
			return err
		}},
		{name: "follow_up group without scope", want: true, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(context.Background(), map[string]any{"agent_id": "exhausted", "message": "go", "group": "g"})
			return err
		}},
		{name: "follow_up plan mode stays visible", want: false, call: func() error {
			_, err := NewFollowUpHandler(followUpDeps())(planCtx, map[string]any{"agent_id": "coder", "message": "go"})
			return err
		}},
		{name: "sub_agent missing type", want: true, call: func() error {
			_, err := dispatch(context.Background(), validStructuredTask("t"))
			return err
		}},
		{name: "sub_agent unknown type", want: true, call: func() error {
			_, err := dispatch(context.Background(), withInput(validStructuredTask("t"), map[string]any{"type": "wizard"}))
			return err
		}},
		{name: "sub_agent vision without image_id", want: true, call: func() error {
			_, err := dispatch(context.Background(), withInput(validStructuredTask("t"), map[string]any{"type": "vision"}))
			return err
		}},
		{name: "sub_agent invalid brief", want: true, call: func() error {
			_, err := newSpecializedHandler(AgentTypeExplore, SpecializedToolDeps{})(context.Background(), map[string]any{"objective": "t"})
			return err
		}},
		{name: "sub_agent group without scope", want: true, call: func() error {
			_, err := newSpecializedHandler(AgentTypeExplore, SpecializedToolDeps{})(context.Background(), withInput(validStructuredTask("t"), map[string]any{"group": "g"}))
			return err
		}},
		{name: "sub_agent code in plan mode stays visible", want: false, call: func() error {
			_, err := newSpecializedHandler(AgentTypeCode, SpecializedToolDeps{})(planCtx, validStructuredTask("t"))
			return err
		}},
		{name: "vision invalid brief", want: true, call: func() error {
			_, err := vision(context.Background(), map[string]any{"image_id": "img-1"})
			return err
		}},
		{name: "vision missing image_id", want: true, call: func() error {
			_, err := vision(context.Background(), validStructuredTask("t"))
			return err
		}},
		{name: "vision unregistered image stays visible", want: false, call: func() error {
			_, err := vision(context.Background(), withInput(validStructuredTask("t"), map[string]any{"image_id": "img-9"}))
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.call()
			if err == nil {
				t.Fatal("call succeeded, want a denial")
			}
			if got := tool.IsModelGuidance(err); got != tt.want {
				t.Fatalf("IsModelGuidance(%q) = %v, want %v", err, got, tt.want)
			}
		})
	}
}

func TestSupervisorRejectionsModelGuidance(t *testing.T) {
	t.Run("outstanding cap", func(t *testing.T) {
		s, _ := newAsyncSupervisor(1)
		a, b := newAsyncChild("a", ""), newAsyncChild("b", "")
		spawnAsync(context.Background(), t, s, a)
		spawnAsync(context.Background(), t, s, b)
		_, _, err := s.Spawn(context.Background(), newAsyncChild("over", "").job)
		if !errors.Is(err, ErrOutstandingCap) || !tool.IsModelGuidance(err) {
			t.Fatalf("err = %v, want ErrOutstandingCap marked as model guidance", err)
		}
		<-a.started
		close(a.release)
		close(b.release)
		waitFinished(t, s, "a")
		waitFinished(t, s, "b")
	})
	t.Run("group name reuse", func(t *testing.T) {
		s, _ := newAsyncSupervisor(2)
		scope := s.newGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
		a, b := newAsyncChild("a", "g"), newAsyncChild("b", "g")
		a.job.GroupScope, b.job.GroupScope = scope, scope
		spawnAsync(batchCtx(testBatchID(1)), t, s, a)
		_, _, err := s.Spawn(batchCtx(testBatchID(2)), b.job)
		var reservationErr *groupReservationError
		if !errors.As(err, &reservationErr) || !tool.IsModelGuidance(err) {
			t.Fatalf("err = %v, want groupReservationError marked as model guidance", err)
		}
		if admission := tool.DelegationAdmissionFromError(err); admission == nil || admission.Status != tool.DelegationAdmissionRejected {
			t.Fatalf("admission = %#v, want rejected", admission)
		}
		<-a.started
		close(a.release)
		waitFinished(t, s, "a")
	})
	t.Run("agent already active stays visible", func(t *testing.T) {
		s, _ := newAsyncSupervisor(1)
		a := newAsyncChild("a", "")
		spawnAsync(context.Background(), t, s, a)
		_, _, err := s.Spawn(context.Background(), newAsyncChild("a", "").job)
		if !errors.Is(err, ErrAgentAlreadyActive) || tool.IsModelGuidance(err) {
			t.Fatalf("err = %v, want unmarked ErrAgentAlreadyActive", err)
		}
		<-a.started
		close(a.release)
		waitFinished(t, s, "a")
	})
}
