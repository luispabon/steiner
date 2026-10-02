package delegation

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tool"
)

func TestRegistryWithoutGroupScopeRejectsNamedGroups(t *testing.T) {
	supervisor := NewSupervisor(SupervisorOptions{MaxParallel: 2})
	t.Cleanup(func() { supervisor.Shutdown(t.Context(), CancelCauseSystem) })
	deps := registryGroupScopeDeps(t, supervisor, "")
	registry, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	subAgent, ok := registry.Get(SubAgentToolName)
	if !ok {
		t.Fatal("sub_agent tool not registered")
	}
	followUp, ok := registry.Get(FollowUpToolName)
	if !ok {
		t.Fatal("follow_up tool not registered")
	}

	tests := []struct {
		name  string
		def   tool.ToolDef
		input map[string]any
	}{
		{name: "specialized", def: subAgent, input: subAgentTask(AgentTypeExplore, "inspect")},
		{name: "vision", def: subAgent, input: subAgentTaskWithImageID("describe", deps.ImageStore.All()[0].ID)},
		{name: "follow_up", def: followUp, input: map[string]any{"agent_id": "agent-x", "message": "continue"}},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.input["group"] = "named"
			_, err := registryCall(t, tc.def, batchContext(testBatchID(uint64(i)+1)), tc.input)
			if err == nil || !strings.Contains(err.Error(), "run-stream group scope") {
				t.Fatalf("error = %v, want run-stream group scope rejection", err)
			}
			supervisor.mu.Lock()
			defer supervisor.mu.Unlock()
			if n := len(supervisor.jobs) + len(supervisor.queue); n != 0 {
				t.Fatalf("jobs after rejected call = %d, want 0", n)
			}
			if n := len(supervisor.scopes); n != 0 {
				t.Fatalf("scopes after rejected call = %d, want 0", n)
			}
		})
	}
}

func TestRegistryWithoutGroupScopeRunsUngroupedDelegation(t *testing.T) {
	supervisor := NewSupervisor(SupervisorOptions{MaxParallel: 2})
	t.Cleanup(func() { supervisor.Shutdown(t.Context(), CancelCauseSystem) })
	registry, err := BuildDelegateRegistry(registryGroupScopeDeps(t, supervisor, ""))
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	def, ok := registry.Get(SubAgentToolName)
	if !ok {
		t.Fatal("sub_agent tool not registered")
	}
	result, err := registryCall(t, def, batchContext(testBatchID(1)), subAgentTask(AgentTypeExplore, "inspect"))
	if err != nil {
		t.Fatalf("ungrouped call without scope: %v", err)
	}
	execResult, ok := result.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result = %T, want tool.ExecutionResult", result)
	}
	if r, ok := execResult.Value.(Result); !ok || r.AgentID == "" {
		t.Fatalf("result value = %#v, want Result with agent ID", execResult.Value)
	}
}
