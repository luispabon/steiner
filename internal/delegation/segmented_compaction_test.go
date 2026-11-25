package delegation

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// segmentedCompactionEnabledPolicy returns a non-default policy so a propagated
// value cannot be confused with the zero value a dropped field would leave.
func segmentedCompactionEnabledPolicy() agent.SegmentedCompactionPolicy {
	return agent.SegmentedCompactionPolicy{
		Enabled:                  true,
		MaxSegments:              5,
		MaxChainPercent:          20,
		MaxPostCompactionPercent: 40,
	}
}

// TestBuildChildRunThreadsSegmentedCompaction proves an ordinary child run
// request carries the parent's segmented compaction policy, and that the
// flag-off zero value stays disabled.
func TestBuildChildRunThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		policy agent.SegmentedCompactionPolicy
	}{
		{name: "flag off", policy: agent.SegmentedCompactionPolicy{}},
		{name: "enabled", policy: segmentedCompactionEnabledPolicy()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			deps := SubAgentHandlerDeps{
				Provider:            stubProvider{},
				ResolvedModel:       provider.ResolvedModel{BackendModelID: "child-model"},
				ParentReg:           tool.NewRegistry(tool.ToolDef{Name: "read"}),
				Sandbox:             tool.Unsandboxed{},
				WorkDir:             t.TempDir(),
				SegmentedCompaction: tt.policy,
			}
			override := ChildBootstrapOverrides{
				AgentType:     AgentTypeExplore,
				AllowedTools:  []string{"read"},
				Provider:      stubProvider{},
				ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
			}

			req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			if req.SegmentedCompaction != tt.policy {
				t.Fatalf("child SegmentedCompaction = %+v, want %+v", req.SegmentedCompaction, tt.policy)
			}
		})
	}
}

// TestSpecializedChildRunThreadsSegmentedCompaction proves the specialized
// sub_agent path (buildSpecializedRun) forwards the policy too.
func TestSpecializedChildRunThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	policy := segmentedCompactionEnabledPolicy()
	var captured agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		captured = req
		return successRunState(), nil
	}}
	deps := minimalDeps(runner)
	deps.WorkDir = t.TempDir()
	deps.SegmentedCompaction = policy

	if _, err := SubAgentToolDef(deps, nil).Handler(context.Background(), subAgentTask(AgentTypeExplore, "inspect the repo")); err != nil {
		t.Fatalf("sub_agent handler() error = %v", err)
	}
	if captured.SegmentedCompaction != policy {
		t.Fatalf("child SegmentedCompaction = %+v, want %+v", captured.SegmentedCompaction, policy)
	}
}

// TestBuildDelegateRegistryThreadsSegmentedCompaction proves BuildDelegateRegistry
// copies the policy from DelegateDeps into the spawned child request.
func TestBuildDelegateRegistryThreadsSegmentedCompaction(t *testing.T) {
	t.Parallel()
	var builds atomic.Int32
	policy := segmentedCompactionEnabledPolicy()
	base := compactionWiringRegistryDeps(t, compactionWiringRegistryConfig(), &builds, nil)
	base.SegmentedCompaction = policy

	reg, store := buildCompactionWiringRegistry(t, base)
	req := spawnCompactionWiringChild(t, reg, store, subAgentTask(AgentTypeExplore, "inspect the codebase"), "explore-segments")
	if req.SegmentedCompaction != policy {
		t.Fatalf("child SegmentedCompaction = %+v, want %+v", req.SegmentedCompaction, policy)
	}
}
