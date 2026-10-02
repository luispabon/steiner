package main

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// TestOneshotPhaseDeliversAcceptedAndQueuedToParamsEvents drives two real
// sub_agent delegations through a phase runner built by newPhaseRunner with
// MaxParallel=1, so the second job is queued. The phase's runtime sink is
// swapped for a multi-sink after the supervisor was built; the supervisor must
// still publish Accepted and Queued on the sink the handler uses, which
// includes params.Events.
func TestOneshotPhaseDeliversAcceptedAndQueuedToParamsEvents(t *testing.T) {
	prov := newAsyncScript("one", "two")
	prov.parent = []func(provider.ChatRequest) provider.ChatResponse{
		step(toolCallsResponse(subAgentCall("call-one", "one", ""), subAgentCall("call-two", "two", ""))),
	}

	cfg := testRuntimeConfig("test-model")
	cfg.SubAgent.Enabled = true
	cfg.SubAgent.MaxParallel = 1
	cfg.SubAgent.MaxFollowUps = 4
	model := cfg.Models.Definitions["test-model"]
	model.Advanced.Limits.ContextWindow = 200_000
	model.Advanced.Limits.MaxOutputTokens = 4096
	cfg.Models.Definitions["test-model"] = model
	cfg.Limits.MaxTurns = 20
	cfg.Limits.MaxTokens = 1_000_000

	workDir := t.TempDir()
	controller := delegation.NewActiveController()
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1, Controller: controller})
	baseEvents := newAsyncEventRecorder()

	orig := buildPhaseRuntime
	t.Cleanup(func() { buildPhaseRuntime = orig })
	buildPhaseRuntime = func(context.Context, *cobra.Command, *cliFlags, string, string, string) (cliRuntime, error) {
		return cliRuntime{
			cfg:                        cfg,
			provider:                   prov,
			workDir:                    workDir,
			homeDir:                    t.TempDir(),
			events:                     baseEvents,
			delegationSessionStore:     delegation.NewSessionStore(),
			delegationActiveController: controller,
			delegationSupervisor:       sup,
			registry:                   runtimeRegistryWithSinkAndMode(cfg, workDir, output.NoopSink{}, true, nil, nil, nil, nil, withPendingSubAgents(sup)),
		}, nil
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	paramsEvents := newAsyncEventRecorder()
	runner, err := newPhaseRunner(context.Background(), cmd, &cliFlags{}, phaseRunnerParams{
		WorkDir:    workDir,
		ModelAlias: "test-model",
		MaxTurns:   20,
		RunMode:    "oneshot",
		SessionID:  "oneshot-phase",
		Events:     paramsEvents,
	})
	if err != nil {
		t.Fatalf("newPhaseRunner: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, runErr := runner.RunPhase(context.Background(), oneshot.PhaseRunInput{
			Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "delegate twice"}},
			Session:      oneshot.PhaseSession{ID: "phase", Save: func(context.Context, agent.DriverSnapshot) error { return nil }},
		})
		done <- runErr
	}()

	recvStarted(t, prov, "one")
	paramsEvents.waitFor(t, func(e output.Event) bool {
		q, ok := e.Payload.(output.DelegationQueuedEvent)
		return ok && q.CallID == "call-two"
	})
	prov.release("one")
	recvStarted(t, prov, "two")
	prov.release("two")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunPhase: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("phase did not finish")
	}

	got := paramsEvents.snapshot()
	for _, callID := range []string{"call-one", "call-two"} {
		accepted := 0
		for _, e := range got {
			if a, ok := e.Payload.(output.DelegationAcceptedEvent); ok && a.CallID == callID {
				accepted++
			}
		}
		if accepted != 1 {
			t.Errorf("params.Events accepted events for %s = %d, want 1", callID, accepted)
		}
	}
}
