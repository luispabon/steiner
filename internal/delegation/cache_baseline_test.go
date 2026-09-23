package delegation

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func cacheBaselineChildDeps(store *agent.CacheBaselineStore) SubAgentHandlerDeps {
	return SubAgentHandlerDeps{
		Provider:      stubProvider{name: "parent"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		ParentReg:     tool.NewRegistry(tool.ToolDef{Name: "read"}),
		CacheBaseline: store,
		Sandbox:       tool.Unsandboxed{},
	}
}

// TestBuildChildRunThreadsCacheBaseline proves a delegated child run request
// receives the parent session's baseline store.
func TestBuildChildRunThreadsCacheBaseline(t *testing.T) {
	store := agent.NewCacheBaselineStore()
	deps := cacheBaselineChildDeps(store)
	override := ChildBootstrapOverrides{
		AgentType:     AgentTypeExplore,
		AllowedTools:  []string{"read"},
		Provider:      stubProvider{name: "child"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
	}

	req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.CacheBaseline != store {
		t.Fatalf("child CacheBaseline = %p, want parent store %p", req.CacheBaseline, store)
	}
}

// TestBuildChildRunCacheBaselineNilWhenUnset proves a parent without a baseline
// store leaves the child request's baseline nil instead of minting one.
func TestBuildChildRunCacheBaselineNilWhenUnset(t *testing.T) {
	deps := cacheBaselineChildDeps(nil)
	override := ChildBootstrapOverrides{
		AgentType:     AgentTypeExplore,
		AllowedTools:  []string{"read"},
		Provider:      stubProvider{name: "child"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
	}

	req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.CacheBaseline != nil {
		t.Fatalf("child CacheBaseline = %p, want nil", req.CacheBaseline)
	}
}

// TestBuildChildRunCacheKeysIsolateAgentTypes proves children of different
// agent types get distinct prompt cache keys, so a shared baseline store cannot
// compare one agent type's outbound requests against another's.
func TestBuildChildRunCacheKeysIsolateAgentTypes(t *testing.T) {
	deps := cacheBaselineChildDeps(agent.NewCacheBaselineStore())
	deps.CacheKeyStore = NewCacheKeyStore()

	explore, _, err := BuildChildRun(context.Background(), deps, ChildBootstrapOverrides{
		AgentType:     AgentTypeExplore,
		AllowedTools:  []string{"read"},
		Provider:      stubProvider{name: "child"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
	}, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun(explore) error = %v", err)
	}
	review, _, err := BuildChildRun(context.Background(), deps, ChildBootstrapOverrides{
		AgentType:     AgentTypeReview,
		AllowedTools:  []string{"read"},
		Provider:      stubProvider{name: "child"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
	}, Spec{Task: "t", AgentID: "b", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun(review) error = %v", err)
	}
	if explore.PromptCacheKey == "" || review.PromptCacheKey == "" {
		t.Fatalf("child cache keys = %q/%q, want non-empty", explore.PromptCacheKey, review.PromptCacheKey)
	}
	if explore.PromptCacheKey == review.PromptCacheKey {
		t.Fatalf("agent types share prompt cache key %q", explore.PromptCacheKey)
	}
}

// TestBuildChildRunSameTypeSiblingsShareCacheKeyButNotBaselineIdentity proves
// two concurrent same-type sibling delegations intentionally share one
// prompt cache key (so they route to the same provider cache shard) while
// still getting distinct baseline identities, so one sibling's outbound
// requests can never be mistaken for the other's predecessor.
func TestBuildChildRunSameTypeSiblingsShareCacheKeyButNotBaselineIdentity(t *testing.T) {
	store := agent.NewCacheBaselineStore()
	deps := cacheBaselineChildDeps(store)
	deps.CacheKeyStore = NewCacheKeyStore()
	override := ChildBootstrapOverrides{
		AgentType:     AgentTypeExplore,
		AllowedTools:  []string{"read"},
		Provider:      stubProvider{name: "child"},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "child-model"},
	}

	siblingA, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "sibling-a", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun(sibling-a) error = %v", err)
	}
	siblingB, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "sibling-b", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun(sibling-b) error = %v", err)
	}

	if siblingA.PromptCacheKey == "" || siblingA.PromptCacheKey != siblingB.PromptCacheKey {
		t.Fatalf("sibling prompt cache keys = %q/%q, want equal and non-empty", siblingA.PromptCacheKey, siblingB.PromptCacheKey)
	}
	if siblingA.CacheBaseline != store || siblingB.CacheBaseline != store {
		t.Fatalf("sibling CacheBaseline pointers = %p/%p, want both %p", siblingA.CacheBaseline, siblingB.CacheBaseline, store)
	}
	if siblingA.AgentID == "" || siblingB.AgentID == "" || siblingA.AgentID == siblingB.AgentID {
		t.Fatalf("sibling AgentIDs = %q/%q, want distinct and non-empty", siblingA.AgentID, siblingB.AgentID)
	}
}
