package delegation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestBuildAdvisorToolsPassesReductionTrigger(t *testing.T) {
	prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "ok"}, FinishReason: "stop"}}}
	cfg := advisorTestConfig()
	reg, err := BuildDelegateRegistry(DelegateDeps{
		BaseRegistry:    tool.NewRegistry(),
		AdvisorCfg:      config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, ReductionTriggerTokens: 50},
		Provider:        prov,
		ProviderFactory: func(provider.ResolvedModel, string) (provider.Provider, error) { return prov, nil },
		Events:          output.NoopSink{}, WorkDir: "/tmp/work",
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "testprov", EffectiveProviderType: config.ProviderTypeOpenAICompat},
		Config:        cfg, ResolveModel: resolveModelFunc(cfg),
	})
	if err != nil {
		t.Fatalf("BuildDelegateRegistry() error = %v", err)
	}
	def, ok := reg.Get(advisor.ToolName)
	if !ok {
		t.Fatal("advisor tool not registered")
	}
	bigOutput, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: strings.Repeat("x", 8000)})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	snapshot := []provider.Message{
		{Role: provider.MessageRoleUser, Content: "run it"},
		{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{ID: "b1", Name: "bash"}}},
		{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1", Content: string(bigOutput)},
		{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{ID: "b2", Name: "bash"}}},
		{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b2", Content: string(bigOutput)},
	}
	ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
	if _, err := def.Handler(ctx, nil); err != nil {
		t.Fatalf("advisor handler() error = %v", err)
	}
	if len(prov.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(prov.requests))
	}
	found := false
	for _, message := range prov.requests[0].Messages {
		if strings.Contains(message.Content, "omitted") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("advisor request was not reduced, want configured reduction trigger to reach the handler")
	}
}
