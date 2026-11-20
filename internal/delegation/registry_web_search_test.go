package delegation

import (
	"context"
	"testing"

	"github.com/deepnoodle-ai/wonton/web"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// stubSearcher satisfies web.Searcher; the tests only assert registration.
type stubSearcher struct{}

func (stubSearcher) Search(context.Context, *web.SearchInput) (*web.SearchOutput, error) {
	return &web.SearchOutput{}, nil
}

func TestBuildDelegateRegistryRegistersWebSearch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		subAgent    bool
		advisor     bool
		searcher    web.Searcher
		wantPresent bool
		wantBase    bool
	}{
		{name: "searcher with delegation disabled", wantPresent: true, searcher: stubSearcher{}},
		{name: "searcher with advisor only", advisor: true, wantPresent: true, searcher: stubSearcher{}},
		{name: "searcher with sub-agents", subAgent: true, wantPresent: true, searcher: stubSearcher{}},
		{name: "no searcher with delegation disabled", wantBase: true},
		{name: "no searcher with sub-agents", subAgent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := advisorTestConfig()
			cfg.Models.Effective.DefaultModel = "advisor"
			base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
			registry, err := BuildDelegateRegistry(DelegateDeps{
				BaseRegistry:  base,
				SubAgentCfg:   config.SubAgentConfig{Enabled: tc.subAgent, MaxFollowUps: 1},
				AdvisorCfg:    config.AdvisorConfig{Enabled: tc.advisor, MaxUsesPerRun: 2},
				Provider:      &fakeProvider{},
				Events:        output.NoopSink{},
				WorkDir:       t.TempDir(),
				HomeDir:       t.TempDir(),
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "testprov", EffectiveProviderType: config.ProviderTypeOpenAICompat},
				MaxTokens:     1024,
				Config:        cfg,
				ResolveModel:  resolveModelFunc(cfg),
				Searcher:      tc.searcher,
				Sandbox:       tool.Unsandboxed{},
			})
			if err != nil {
				t.Fatalf("BuildDelegateRegistry() error = %v", err)
			}
			_, present := registry.Get("web_search")
			if present != tc.wantPresent {
				t.Errorf("web_search present = %v, want %v; registry = %v", present, tc.wantPresent, registry.Names())
			}
			if (registry == base) != tc.wantBase {
				t.Errorf("registry is base pointer = %v, want %v", registry == base, tc.wantBase)
			}
			if _, ok := base.Get("web_search"); ok {
				t.Errorf("web_search leaked into base registry; got %v", base.Names())
			}
		})
	}
}
