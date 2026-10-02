package delegation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func registryGroupScopeDeps(t *testing.T, supervisor *Supervisor, scope string) DelegateDeps {
	t.Helper()
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"test": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://example.invalid"},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{
				DefaultModel: "default",
				SubAgents:    map[string]string{string(AgentTypeVision): "vision"},
			},
			Definitions: map[string]config.ModelConfig{
				"default": {Provider: "test", ID: "model"},
				"vision":  {Provider: "test", ID: "vision-model"},
			},
		},
	}
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "done"}, FinishReason: "stop"}}}
	imagesDir := t.TempDir()
	imagePath := filepath.Join(imagesDir, "image.png")
	if err := os.WriteFile(imagePath, []byte("fake image"), 0o600); err != nil {
		t.Fatal(err)
	}
	imageStore := agent.NewImageStore(imagesDir)
	imageStore.Register(imagePath, "image/png", 1, 1, 1)
	return DelegateDeps{
		BaseRegistry:  tool.NewRegistry(),
		SubAgentCfg:   config.SubAgentConfig{Enabled: true, MaxParallel: 2, MaxTurns: 1, MaxTokens: 32, MaxFollowUps: 4},
		Provider:      prov,
		Events:        output.NoopSink{},
		WorkDir:       t.TempDir(),
		HomeDir:       t.TempDir(),
		ResolvedModel: provider.ResolvedModel{Alias: "default", ProviderAlias: "test", EffectiveProviderType: config.ProviderTypeOpenAICompat},
		MaxTokens:     32,
		Config:        cfg,
		ResolveModel:  resolveModelFunc(cfg),
		ProviderFactory: func(provider.ResolvedModel, string) (provider.Provider, error) {
			return prov, nil
		},
		Supervisor:   supervisor,
		GroupScope:   scope,
		SessionStore: NewSessionStore(),
		ImageStore:   imageStore,
	}
}

//revive:disable-next-line:context-as-argument
func registryCall(t *testing.T, def tool.ToolDef, ctx context.Context, input map[string]any) (any, error) {
	t.Helper()
	return def.Handler(ctx, input)
}

func batchContext(batch string) context.Context {
	return agent.WithToolBatchID(context.Background(), batch)
}

func TestRegistryGroupScopeSharedAcrossRegisteredHandlers(t *testing.T) {
	supervisor := NewSupervisor(SupervisorOptions{MaxParallel: 2})
	scope, release := supervisor.OpenGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion})
	t.Cleanup(release)
	deps := registryGroupScopeDeps(t, supervisor, scope)
	registry, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	specialized, ok := registry.Get(SubAgentToolName)
	if !ok {
		t.Fatal("sub_agent tool not registered")
	}
	followUp, ok := registry.Get(FollowUpToolName)
	if !ok {
		t.Fatal("follow_up tool not registered")
	}

	exploreInput := subAgentTask(AgentTypeExplore, "inspect")
	exploreInput["group"] = "explore-group"
	result, err := registryCall(t, specialized, batchContext(testBatchID(1)), exploreInput)
	if err != nil {
		t.Fatalf("registered explore handler() error = %v", err)
	}
	delegationResult, ok := result.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("explore result = %T, want tool.ExecutionResult", result)
	}
	explore, ok := delegationResult.Value.(Result)
	if !ok || explore.AgentID == "" {
		t.Fatalf("explore result value = %#v, want Result with agent ID", delegationResult.Value)
	}

	visionInput := subAgentTaskWithImageID("describe", deps.ImageStore.All()[0].ID)
	visionInput["group"] = "explore-group"
	if _, err := registryCall(t, specialized, batchContext(testBatchID(2)), visionInput); err == nil || !containsGroupReuseError(err) {
		t.Fatalf("vision reusing explore group error = %v, want used group name rejection", err)
	}
	visionInput["group"] = "vision-group"
	if _, err := registryCall(t, specialized, batchContext(testBatchID(3)), visionInput); err != nil {
		t.Fatalf("registered vision handler() error = %v", err)
	}

	warmFollowUpInput := map[string]any{"agent_id": explore.AgentID, "message": "continue", "group": "vision-group"}
	if _, err := registryCall(t, followUp, batchContext(testBatchID(4)), warmFollowUpInput); err == nil || !containsGroupReuseError(err) {
		t.Fatalf("follow_up reusing vision group error = %v, want used group name rejection", err)
	}
	warmFollowUpInput["group"] = "warm-follow-up"
	warmResult, err := registryCall(t, followUp, batchContext(testBatchID(5)), warmFollowUpInput)
	if err != nil {
		t.Fatalf("registered warm follow_up handler() error = %v", err)
	}
	if _, ok := warmResult.(tool.ExecutionResult); !ok {
		t.Fatalf("warm follow_up result = %T, want tool.ExecutionResult", warmResult)
	}

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{name: "repeated specialized", call: func() error {
			_, err := registryCall(t, specialized, batchContext(testBatchID(6)), exploreInput)
			return err
		}},
		{name: "vision", call: func() error {
			_, err := registryCall(t, specialized, batchContext(testBatchID(7)), visionInput)
			return err
		}},
		{name: "follow_up", call: func() error {
			input := map[string]any{"agent_id": explore.AgentID, "message": "continue", "group": "warm-follow-up"}
			_, err := registryCall(t, followUp, batchContext(testBatchID(8)), input)
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil || !containsGroupReuseError(err) {
				t.Fatalf("call error = %v, want used group name rejection", err)
			}
		})
	}
}

func containsGroupReuseError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "was already used")
}

func TestRegistryGroupScopeExplicitScopePreserved(t *testing.T) {
	supervisor := NewSupervisor(SupervisorOptions{MaxParallel: 2})
	scope := supervisor.newGroupScope(agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion, Names: []string{"reserved"}})
	deps := registryGroupScopeDeps(t, supervisor, scope)
	registry, err := BuildDelegateRegistry(deps)
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	def, ok := registry.Get(SubAgentToolName)
	if !ok {
		t.Fatal("sub_agent tool not registered")
	}
	input := subAgentTask(AgentTypeExplore, "inspect")
	input["group"] = "reserved"
	if _, err := registryCall(t, def, batchContext(testBatchID(9)), input); err == nil || !containsGroupReuseError(err) {
		t.Fatalf("registered handler using seeded group error = %v, want used group name rejection", err)
	}

	input["group"] = "accepted"
	if _, err := registryCall(t, def, batchContext(testBatchID(10)), input); err != nil {
		t.Fatalf("registered handler with explicit scope: %v", err)
	}
	got := supervisor.SnapshotGroupLedger(scope)
	want := agent.DelegationGroupLedger{Version: agent.DelegationGroupLedgerVersion, Names: []string{"accepted", "reserved"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit scope ledger = %+v, want %+v", got, want)
	}
}
