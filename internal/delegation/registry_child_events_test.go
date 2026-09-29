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

			gotPerRun, gotRuntime := len(perRun.delegationEvents()), len(runtime.delegationEvents())
			if tc.wantRuntime {
				if gotRuntime == 0 {
					t.Error("no Delegation* events on the child sink")
				}
				if gotPerRun != 0 {
					t.Errorf("per-run sink received %d Delegation* events, want 0", gotPerRun)
				}
				return
			}
			if gotPerRun == 0 {
				t.Error("no Delegation* events on the per-run sink when ChildEvents is nil")
			}
		})
	}
}
