package delegation

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/tool"
)

func TestHandlerFallbackGroupScopesAreUniqueAndStable(t *testing.T) {
	supervisor := NewSupervisor(SupervisorOptions{MaxParallel: 1})
	deps := minimalDeps(nil).SubAgentHandlerDeps
	deps.Supervisor = supervisor
	ensureSupervisor(&deps)
	firstScope := deps.GroupScope
	if firstScope == "" {
		t.Fatal("fallback scope is empty")
	}

	// Repeated ensure on one handler deps keeps its fallback scope.
	ensureSupervisor(&deps)
	if deps.GroupScope != firstScope {
		t.Fatalf("repeated ensure scope = %q, want %q", deps.GroupScope, firstScope)
	}

	// A second handler with the same Supervisor and no injected scope gets an
	// independent name ledger.
	secondDeps := minimalDeps(nil).SubAgentHandlerDeps
	secondDeps.Supervisor = supervisor
	ensureSupervisor(&secondDeps)
	if secondDeps.GroupScope == firstScope {
		t.Fatalf("fallback scope reused: %q", firstScope)
	}
	job := ChildJob{AgentID: "first", Group: "same", Execute: func(context.Context) (tool.ExecutionResult, error) { return tool.ExecutionResult{}, nil }}
	job.GroupScope = firstScope
	if _, _, err := supervisor.SpawnWithAdmission(agent.WithToolBatchID(context.Background(), "batch"), job); err != nil {
		t.Fatalf("first scope admission: %v", err)
	}
	job.AgentID, job.GroupScope = "second", secondDeps.GroupScope
	if _, _, err := supervisor.SpawnWithAdmission(agent.WithToolBatchID(context.Background(), "batch"), job); err != nil {
		t.Fatalf("independent scope admission: %v", err)
	}
	supervisor.Shutdown(context.Background(), CancelCauseSystem)
}
