package delegation

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

type lockedSink struct {
	mu     sync.Mutex
	events []output.Event
}

func (s *lockedSink) Emit(e output.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *lockedSink) delegationEvents() []output.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []output.Event
	for _, e := range s.events {
		if strings.HasPrefix(e.Type, "delegation") {
			out = append(out, e)
		}
	}
	return out
}

func TestBuildDelegateRegistryEmitsDelegationEventsOnChildEvents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		childEvents func(perRun, runtime *lockedSink) output.EventSink
		wantRuntime bool
	}{
		{name: "separate child sink", childEvents: func(_, runtime *lockedSink) output.EventSink { return runtime }, wantRuntime: true},
		{name: "defaults to events", childEvents: func(_, _ *lockedSink) output.EventSink { return nil }, wantRuntime: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newParallelHarness(delegationParentResponse("explore"), 1)
			close(h.done)
			perRun, runtime := &lockedSink{}, &lockedSink{}

			reg, err := BuildDelegateRegistry(DelegateDeps{
				BaseRegistry:       tool.NewRegistry(),
				SubAgentCfg:        config.SubAgentConfig{Enabled: true, MaxTurns: 1, MaxTokens: 1000, MaxFollowUps: 100, MaxParallel: 1},
				Provider:           h.provider,
				Config:             config.Config{},
				WorkDir:            h.workDir,
				Events:             perRun,
				ChildEvents:        tc.childEvents(perRun, runtime),
				StreamingPreferred: true,
			})
			if err != nil {
				t.Fatalf("BuildDelegateRegistry() error = %v", err)
			}
			executor := tool.NewExecutor(reg, config.Config{}, nil, h.workDir, "", tool.Unsandboxed{})
			if _, err := executor.Execute(context.Background(), SubAgentToolName, "call-0", delegationParentResponse("explore").Message.ToolCalls[0].Arguments); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			perRunEvents, runtimeEvents := perRun.delegationEvents(), runtime.delegationEvents()
			if tc.wantRuntime {
				if len(perRunEvents) != 1 {
					t.Fatalf("per-run delegation events = %+v, want one accepted event", perRunEvents)
				}
				accepted, ok := perRunEvents[0].Payload.(output.DelegationAcceptedEvent)
				if !ok || perRunEvents[0].Type != output.EventTypeDelegationAccepted {
					t.Fatalf("per-run first event = %+v, want DelegationAcceptedEvent", perRunEvents[0])
				}
				if accepted.CallID != "call-0" || accepted.AgentID == "" {
					t.Fatalf("accepted event identity = %+v, want call-0 and non-empty agent ID", accepted)
				}

				if len(runtimeEvents) != 2 {
					t.Fatalf("child delegation events = %+v, want started then complete", runtimeEvents)
				}
				if runtimeEvents[0].Type != output.EventTypeDelegationStarted || runtimeEvents[1].Type != output.EventTypeDelegationComplete {
					t.Fatalf("child delegation event order = %v, want started then complete", []string{runtimeEvents[0].Type, runtimeEvents[1].Type})
				}
				started, ok := runtimeEvents[0].Payload.(output.DelegationStartedEvent)
				if !ok || started.AgentID != accepted.AgentID || started.CallID != accepted.CallID {
					t.Fatalf("child started event = %+v, want accepted identity %+v", runtimeEvents[0], accepted)
				}
				return
			}
			if len(perRunEvents) == 0 {
				t.Error("no Delegation* events on the per-run sink when ChildEvents is nil")
			}
		})
	}
}
