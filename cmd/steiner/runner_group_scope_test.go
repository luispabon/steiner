package main

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestCLIRunnerRunReusesDelegationGroupScopeWithinStream(t *testing.T) {
	runner, supervisor, scope, script := newGroupScopeTestRunner(t)
	t.Cleanup(func() { supervisor.CancelAll(delegation.CancelCauseUser) })
	runGroup := func(objective string) runResult {
		t.Helper()
		result, err := runner.run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "dispatch " + objective}}, nil, runHooks{delegationGroupScope: scope})
		if err != nil {
			t.Fatalf("cliRunner.run(%s) error = %v", objective, err)
		}
		return result
	}

	first := runGroup("first objective")
	if got := toolResultInConversation(first.Conversation, "first"); !strings.Contains(got, `"status":"running"`) {
		t.Fatalf("first sub_agent result = %q, want running ack", got)
	}
	recvStarted(t, script, "first objective")

	second := runGroup("second objective")
	if got := toolResultInConversation(second.Conversation, "second"); !strings.Contains(got, `"status":"failed"`) {
		t.Fatalf("second sub_agent result = %q, want failed ack for reused group name", got)
	}
	select {
	case got := <-script.started:
		t.Fatalf("second child %q started despite reused group name", got)
	default:
	}
	if got := supervisor.SnapshotGroupLedger(scope).Names; len(got) != 1 || got[0] != "shared-group" {
		t.Fatalf("scope names = %v, want [shared-group]", got)
	}
}

func newGroupScopeTestRunner(t *testing.T) (cliRunner, *delegation.Supervisor, string, *asyncScript) {
	t.Helper()
	cfg := testRuntimeConfig("test-model")
	cfg.SubAgent.Enabled = true
	cfg.SubAgent.MaxParallel = 2
	cfg.SubAgent.MaxFollowUps = 4
	cfg.Limits.MaxTurns = 10
	model := cfg.Models.Definitions["test-model"]
	model.Advanced.Limits.ContextWindow = 200_000
	model.Advanced.Limits.MaxOutputTokens = 4096
	cfg.Models.Definitions["test-model"] = model
	supervisor := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 2})
	scope := supervisor.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	script := newAsyncScript("first objective")
	script.parent = []func(provider.ChatRequest) provider.ChatResponse{
		step(toolCallsResponse(subAgentCall("first", "first objective", "shared-group"))),
		step(textResponse("first done")),
		step(toolCallsResponse(subAgentCall("second", "second objective", "shared-group"))),
		step(textResponse("second done")),
	}
	workDir := t.TempDir()
	rt := cliRuntime{
		cfg: cfg, provider: script, registry: runtimeRegistryWithSinkAndMode(cfg, workDir, nil, true, nil, nil, nil, nil),
		workDir: workDir, homeDir: t.TempDir(), events: output.NoopSink{},
		delegationSupervisor:   supervisor,
		providerFactory:        func(provider.ResolvedModel, string) (provider.Provider, error) { return script, nil },
		delegationSessionStore: delegation.NewSessionStore(), delegationCacheKeyStore: delegation.NewCacheKeyStore(),
		delegationActiveController: delegation.NewActiveController(),
	}
	return cliRunner{runtime: rt, runMode: "interactive"}, supervisor, scope, script
}
