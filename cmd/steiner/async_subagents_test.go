package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/tool"
)

func TestNewDelegateDepsAsyncFollowsRunMode(t *testing.T) {
	tests := []struct {
		runMode   string
		wantAsync bool
	}{
		{runMode: "", wantAsync: false},
		{runMode: "exec", wantAsync: false},
		{runMode: "interactive", wantAsync: true},
		{runMode: "oneshot", wantAsync: true},
	}
	for _, tc := range tests {
		t.Run(tc.runMode, func(t *testing.T) {
			deps := (cliRunner{runMode: tc.runMode}).newDelegateDeps(runnerSetup{}, nil, nil, nil, "")
			if deps.AsyncSubAgents != tc.wantAsync {
				t.Fatalf("AsyncSubAgents = %v, want %v", deps.AsyncSubAgents, tc.wantAsync)
			}
		})
	}
}

func TestWorkflowHandoffRefusedWhileSubAgentsOutstanding(t *testing.T) {
	sup := delegation.NewSupervisor(delegation.SupervisorOptions{MaxParallel: 1})
	release := make(chan struct{})
	started := make(chan struct{})
	if _, err := sup.Spawn(context.Background(), delegation.ChildJob{
		AgentID:   "child-1",
		AgentType: delegation.AgentTypeExplore,
		Execute: func(ctx context.Context) (tool.ExecutionResult, error) {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return tool.ExecutionResult{}, nil
		},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	<-started
	t.Cleanup(func() { close(release) })

	registry := runtimeRegistryWithSinkAndMode(registryTestConfig(), t.TempDir(), nil, false, nil, nil, nil, nil, withPendingSubAgents(sup))
	def, ok := registry.Get("workflow_handoff")
	if !ok {
		t.Fatal("workflow_handoff not registered")
	}
	_, err := def.Handler(context.Background(), map[string]any{"next": "implement", "target": "."})
	if err == nil || !strings.Contains(err.Error(), "outstanding") {
		t.Fatalf("handler error = %v, want an outstanding sub-agents error", err)
	}
}

func TestAsyncToolDefinitionsStableAcrossTurns(t *testing.T) {
	cfg := registryTestConfig()
	cfg.SubAgent.Enabled = true
	r := cliRunner{runMode: "interactive", runtime: cliRuntime{cfg: cfg, registry: runtimeRegistry(cfg, t.TempDir())}}
	build := func() []tool.ToolDef {
		reg, err := delegation.BuildDelegateRegistry(r.newDelegateDeps(runnerSetup{}, nil, nil, nil, ""))
		if err != nil {
			t.Fatalf("BuildDelegateRegistry: %v", err)
		}
		return reg.Definitions()
	}
	first, second := build(), build()
	if len(first) != len(second) {
		t.Fatalf("definition count %d != %d", len(first), len(second))
	}
	var sawGroup bool
	for i := range first {
		if first[i].Name != second[i].Name || !reflect.DeepEqual(first[i].ParameterSchema, second[i].ParameterSchema) {
			t.Fatalf("definition %q differs between turns", first[i].Name)
		}
		if first[i].Name == "sub_agent" {
			props, _ := first[i].ParameterSchema["properties"].(map[string]any)
			_, sawGroup = props["group"]
		}
	}
	if !sawGroup {
		t.Fatal("async sub_agent schema lacks the group property")
	}
}
