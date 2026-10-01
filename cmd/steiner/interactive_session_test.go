package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestSessionRunnerForwardsExactDelegationGroupScope(t *testing.T) {
	supervisor := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1})
	fallback := supervisor.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	explicit := supervisor.NewGroupScope(agent.DelegationGroupLedger{Version: 1})
	cfg := testRuntimeConfig("test-model")
	cfg.SubAgent.Enabled = true
	cfg.SubAgent.MaxParallel = 1
	script := newAsyncScript("scope forwarding")
	script.parent = []func(provider.ChatRequest) provider.ChatResponse{
		step(toolCallsResponse(subAgentCall("call-group", "scope forwarding", "interactive-group"))),
		step(textResponse("done")),
	}
	rt := cliRuntime{
		cfg: cfg, provider: script, registry: tool.NewRegistry(), workDir: t.TempDir(), homeDir: t.TempDir(), events: output.NoopSink{},
		delegationSupervisor: supervisor, delegationFallbackGroupScope: fallback,
		providerFactory:        func(provider.ResolvedModel, string) (provider.Provider, error) { return script, nil },
		delegationSessionStore: delegation.NewSessionStore(), delegationCacheKeyStore: delegation.NewCacheKeyStore(),
		delegationActiveController: delegation.NewActiveController(),
	}
	result, err := (sessionRunner{runner: cliRunner{runtime: rt, runMode: "interactive"}}).Run(context.Background(), interactive.RunInput{
		Conversation: []agent.Message{{Role: agent.MessageRoleUser, Content: "dispatch"}}, DelegationGroupScope: explicit,
	})
	if err != nil {
		t.Fatalf("sessionRunner.Run() error = %v", err)
	}
	if result.StopReason != agent.StopReasonComplete {
		t.Fatalf("stop reason = %q, want complete", result.StopReason)
	}
	select {
	case got := <-script.started:
		if got != "scope forwarding" {
			t.Fatalf("child objective = %q, want scope forwarding", got)
		}
	default:
		t.Fatal("child did not start")
	}
	if got := supervisor.SnapshotGroupLedger(explicit).Names; len(got) != 1 || got[0] != "interactive-group" {
		t.Fatalf("explicit scope names = %v, want [interactive-group]", got)
	}
	if got := supervisor.SnapshotGroupLedger(fallback).Names; len(got) != 0 {
		t.Fatalf("fallback scope names = %v, want empty", got)
	}
	supervisor.CancelAll(delegation.CancelCauseUser)
}
