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
		AdvisorCfg:      config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, ContextReductionThresholdTokens: 50},
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

// TestAdvisorReductionProtectsDelegationToolNames pins the delegation tool
// names protected from advisor context reduction to their owning constants.
// internal/advisor restates the names because internal/delegation imports it,
// so hardcoded drift there must fail here.
func TestAdvisorReductionProtectsDelegationToolNames(t *testing.T) {
	cases := []struct {
		name string
		tool string
	}{
		{name: "sub_agent", tool: SubAgentToolName},
		{name: "follow_up", tool: FollowUpToolName},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prov := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Content: "ok"}, FinishReason: "stop"}}}
			cfg := advisorTestConfig()
			reg, err := BuildDelegateRegistry(DelegateDeps{
				BaseRegistry:    tool.NewRegistry(),
				AdvisorCfg:      config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 1, ContextReductionThresholdTokens: 50},
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
			ordinaryOutput, err := json.Marshal(builtin.BashResult{ArtifactStatus: "none", Output: strings.Repeat("x", 8000)})
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			delegationContent := "delegation result for " + tc.tool + ": " + strings.Repeat("d", 6000)
			// The delegation result comes first so the tail protection window can
			// only ever cover the last ordinary bash result. If the advisor stopped
			// protecting the delegation name, its result becomes an ordinary
			// candidate and the reduction assertions below fail.
			snapshot := []provider.Message{
				{Role: provider.MessageRoleUser, Content: "run it"},
				{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{
					{ID: "b1", Name: "bash"},
					{ID: "b2", Name: "bash"},
					{ID: "b3", Name: "bash"},
					{ID: "b4", Name: "bash"},
					{ID: "d1", Name: tc.tool},
				}},
				{Role: provider.MessageRoleTool, Name: tc.tool, ToolCallID: "d1", Content: delegationContent},
				{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b1", Content: string(ordinaryOutput)},
				{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b2", Content: string(ordinaryOutput)},
				{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b3", Content: string(ordinaryOutput)},
				{Role: provider.MessageRoleTool, Name: "bash", ToolCallID: "b4", Content: string(ordinaryOutput)},
			}
			ctx := agent.WithConversationSnapshot(context.Background(), snapshot)
			if _, err := def.Handler(ctx, nil); err != nil {
				t.Fatalf("advisor handler() error = %v", err)
			}
			if len(prov.requests) != 1 {
				t.Fatalf("captured %d requests, want 1", len(prov.requests))
			}
			ordinaryReduced := false
			delegationIntact := false
			wantDelegation := "[tool_result: " + tc.tool + "]\n" + delegationContent
			for _, message := range prov.requests[0].Messages {
				if strings.HasPrefix(message.Content, "[tool_result: bash]") && strings.Contains(message.Content, "omitted") {
					ordinaryReduced = true
				}
				if message.Content == wantDelegation {
					delegationIntact = true
				}
			}
			if !ordinaryReduced {
				t.Fatal("ordinary bash results were not reduced, want over-threshold reduction to run as the control")
			}
			if !delegationIntact {
				t.Fatalf("advisor request did not carry the %s result unchanged, want %s protected from reduction", tc.tool, tc.tool)
			}
		})
	}
}
