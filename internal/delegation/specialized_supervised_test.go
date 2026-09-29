package delegation

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type scopableProvider struct {
	stubProvider
	sink output.EventSink
}

func (p scopableProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	p.sink.Emit(output.NewAPIResponseEvent(provider.Message{}, nil, "stop", nil))
	return provider.ChatResponse{}, nil
}

func (p scopableProvider) WithEventSink(wrap func(output.EventSink) output.EventSink) provider.Provider {
	p.sink = wrap(p.sink)
	return p
}

func TestChildProviderEventsCarryAgentScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		agentType AgentType
	}{
		{name: "explore", agentType: AgentTypeExplore},
		{name: "review", agentType: AgentTypeReview},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var apiResponses []output.Event
			var startedID string
			sink := output.SinkFunc(func(event output.Event) {
				mu.Lock()
				defer mu.Unlock()
				switch event.Type {
				case output.EventTypeAPIResponse:
					apiResponses = append(apiResponses, event)
				case output.EventTypeDelegationStarted:
					startedID = event.Scope.AgentID
				}
			})
			deps := minimalDeps(&mockRunner{runFunc: func(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
				if _, err := req.Provider.ChatCompletion(ctx, provider.ChatRequest{}); err != nil {
					return agent.RunState{}, err
				}
				return successRunState(), nil
			}})
			deps.Provider = scopableProvider{sink: sink}
			deps.Events = sink

			if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(tt.agentType, "scope")); err != nil {
				t.Fatalf("handler returned error: %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(apiResponses) != 1 {
				t.Fatalf("api response events = %d, want 1", len(apiResponses))
			}
			got := apiResponses[0].Scope
			if startedID == "" || got.AgentID != startedID || got.AgentType != string(tt.agentType) {
				t.Errorf("api response scope = %+v, want agent %q type %q", got, startedID, tt.agentType)
			}
		})
	}
}

func TestSpecializedHandlerMaxParallelOneRunsBatchSerially(t *testing.T) {
	t.Parallel()
	var runs atomic.Int32
	var firstDone atomic.Bool
	var overlapped atomic.Bool
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	deps := minimalDeps(&mockRunner{runFunc: func(context.Context, agent.RunRequest) (agent.RunState, error) {
		if runs.Add(1) == 1 {
			entered <- struct{}{}
			<-release
			firstDone.Store(true)
			return successRunState(), nil
		}
		if !firstDone.Load() {
			overlapped.Store(true)
		}
		entered <- struct{}{}
		return successRunState(), nil
	}})
	deps.SubAgentCfg.MaxParallel = 1
	deps.ActiveController = NewActiveController()
	handler := SubAgentToolDef(deps, nil).Handler

	errs := make(chan error, 2)
	call := func() {
		_, err := handler(context.Background(), subAgentTask(AgentTypeExplore, "serial"))
		errs <- err
	}
	go call()
	<-entered
	go call()
	for len(deps.ActiveController.ActiveAgentIDs()) < 2 {
		runtime.Gosched()
	}
	close(release)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	}
	if overlapped.Load() {
		t.Error("second child ran before the first finished with max_parallel=1")
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("runner calls = %d, want 2", got)
	}
}

func TestCancelDuringRemediationIsAccepted(t *testing.T) {
	t.Parallel()
	controller := NewActiveController()
	deps := minimalDeps(nil)
	deps.ActiveController = controller
	remediating := make(chan struct{})
	var calls atomic.Int32
	stopped := make(chan struct{})
	deps.Runner = &mockRunner{runFunc: func(ctx context.Context, _ agent.RunRequest) (agent.RunState, error) {
		if calls.Add(1) == 1 {
			return successRunState(), nil
		}
		close(remediating)
		<-ctx.Done()
		close(stopped)
		return agent.RunState{}, ctx.Err()
	}}
	ensureSupervisor(&deps.SubAgentHandlerDeps)

	remediation := &RemediationConfig{
		WorktreePath: t.TempDir(),
		IsDirty:      func(context.Context) ([]string, error) { return []string{"a.go"}, nil },
		Head:         func(context.Context) (string, error) { return "abc", nil },
		Committed:    func(context.Context, string, []string) (bool, error) { return false, nil },
	}
	spec := remediationTestSpec()
	spec.AgentType = AgentTypeCode

	type outcome struct {
		result tool.ExecutionResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := runRegisteredDelegate(context.Background(), deps, spec, agent.RunRequest{}, CodeWorktree{}, nil, remediation, "code", func(r tool.ExecutionResult) tool.ExecutionResult { return r })
		done <- outcome{result, err}
	}()

	<-remediating
	if got := deps.Supervisor.CancelAgent(spec.AgentID, false, CancelCauseUser); got != CancelAccepted {
		t.Fatalf("CancelAgent during remediation = %v, want CancelAccepted", got)
	}
	<-stopped
	got := <-done
	if got.err != nil {
		t.Fatalf("runRegisteredDelegate returned error: %v", got.err)
	}
	if got := deps.Supervisor.CancelAgent(spec.AgentID, false, CancelCauseUser); got == CancelAccepted {
		t.Errorf("CancelAgent after finish = %v, want not accepted", got)
	}
}
