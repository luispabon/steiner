package main

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/oneshot"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// probeScopeSealed reports whether batch #1 is closed to new group members in
// scope: any batch the stream sealed has a sequence at or above 1, while a
// scope no stream sealed still accepts it.
func probeScopeSealed(t *testing.T, sup *delegation.Supervisor, scope string) bool {
	t.Helper()
	name := "probe-" + scope
	job := delegation.ChildJob{AgentID: "agent-" + name, Group: name, GroupScope: scope, Execute: func(context.Context) (tool.ExecutionResult, error) {
		return tool.ExecutionResult{}, nil
	}}
	_, _, err := sup.Spawn(agent.WithToolBatchID(context.Background(), "probe#1"), job)
	return err != nil
}

// toolBatchScript is a parent script that runs one plain tool batch and answers.
func toolBatchScript() *asyncScript {
	script := newAsyncScript()
	script.parent = []func(provider.ChatRequest) provider.ChatResponse{
		step(toolCallsResponse(provider.ToolCall{ID: "ls-1", Name: "ls", Arguments: map[string]any{"path": "."}})),
		step(textResponse("done")),
	}
	return script
}

func TestEveryModeSealsItsOwnScopeAndNoOther(t *testing.T) {
	user := []agent.Message{{Role: agent.MessageRoleUser, Content: "go"}}
	modes := []struct {
		name string
		run  func(t *testing.T, runner cliRunner, scope string)
	}{
		{"exec", func(t *testing.T, runner cliRunner, scope string) {
			if _, err := runner.run(context.Background(), user, nil, runHooks{delegationGroupScope: scope}); err != nil {
				t.Fatalf("run: %v", err)
			}
		}},
		{"interactive", func(t *testing.T, runner cliRunner, scope string) {
			_, err := sessionRunner{runner: runner}.Run(context.Background(), interactive.RunInput{Conversation: user, DelegationGroupScope: scope})
			if err != nil {
				t.Fatalf("sessionRunner.Run: %v", err)
			}
		}},
		{"oneshot phase", func(t *testing.T, runner cliRunner, scope string) {
			rec := &driverRunRecord{}
			host := phaseDriverHost{
				run:        runner.driverRun(nil, scope, rec),
				record:     rec,
				background: runner.runtime.delegationSupervisor,
				shutdown:   func(context.Context, delegation.CancelCause) {},
			}
			in := oneshot.PhaseRunInput{
				Conversation: user,
				Session:      oneshot.PhaseSession{ID: "phase", Save: func(context.Context, agent.DriverSnapshot) error { return nil }},
			}
			if _, err := runPhaseOnDriver(context.Background(), in, host); err != nil {
				t.Fatalf("runPhaseOnDriver: %v", err)
			}
		}},
	}
	for _, mode := range modes {
		t.Run(mode.name, func(t *testing.T) {
			runner, sup, own, _ := newGroupScopeTestRunner(t)
			t.Cleanup(func() { sup.CancelAll(delegation.CancelCauseUser) })
			runner.runtime.provider = toolBatchScript()
			runner.runtime.providerFactory = func(provider.ResolvedModel, string) (provider.Provider, error) { return runner.runtime.provider, nil }
			other := sup.NewGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})

			mode.run(t, runner, own)

			if !probeScopeSealed(t, sup, own) {
				t.Fatalf("%s run did not seal its own scope %s", mode.name, own)
			}
			if probeScopeSealed(t, sup, other) {
				t.Fatalf("%s run sealed scope %s it does not own", mode.name, other)
			}
		})
	}
}

func TestRunWithoutScopeSealsNothing(t *testing.T) {
	runner, sup, scope, _ := newGroupScopeTestRunner(t)
	t.Cleanup(func() { sup.CancelAll(delegation.CancelCauseUser) })
	script := toolBatchScript()
	runner.runtime.provider = script
	runner.runtime.providerFactory = func(provider.ResolvedModel, string) (provider.Provider, error) { return script, nil }
	user := []agent.Message{{Role: agent.MessageRoleUser, Content: "go"}}
	if _, err := runner.run(context.Background(), user, nil, runHooks{}); err != nil {
		t.Fatalf("run without scope: %v", err)
	}
	if probeScopeSealed(t, sup, scope) {
		t.Fatal("a run with no scope sealed an existing scope")
	}
}
