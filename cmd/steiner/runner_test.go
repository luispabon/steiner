package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestToProviderConversationPreservesTurn(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{Role: agent.MessageRoleAssistant, Content: "world", Turn: 5},
		{
			Role:       agent.MessageRoleTool,
			Content:    "result",
			ToolCallID: "call_1",
			Turn:       6,
		},
	}

	got := toProviderConversation(messages)
	if len(got) != len(messages) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(messages))
	}
	for i, message := range got {
		if message.Turn != messages[i].Turn {
			t.Fatalf("message %d turn = %d, want %d", i, message.Turn, messages[i].Turn)
		}
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("tool call id = %q, want call_1", got[2].ToolCallID)
	}
	if got[0].Role != provider.MessageRoleUser || got[1].Role != provider.MessageRoleAssistant || got[2].Role != provider.MessageRoleTool {
		t.Fatalf("roles preserved incorrectly: %#v", got)
	}
}

// stubProvider satisfies provider.Provider for testing.
type stubProvider struct{}

func (stubProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (stubProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}
func (stubProvider) SupportsUsageStats() bool { return false }

type noopSink struct{}

func (noopSink) Emit(output.Event) {}

func TestBuildActiveRegistry_DelegatePresent_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)

	found := false
	for _, n := range reg.Names() {
		if n == delegation.DelegateToolName {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("delegate tool not found in registry when sub_agent.enabled=true; got %v", reg.Names())
	}

	// base registry must not be polluted
	for _, n := range base.Names() {
		if n == delegation.DelegateToolName {
			t.Error("delegate tool leaked into base registry")
		}
	}
}

func TestBuildActiveRegistry_SpecializedToolsPresent_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)

	names := reg.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	// Research agent is excluded when no searcher is configured.
	for _, agentType := range delegation.AllAgentTypes() {
		if agentType == delegation.AgentTypeResearch {
			if nameSet[string(agentType)] {
				t.Errorf("research tool should not be in registry when no searcher configured")
			}
			continue
		}
		if !nameSet[string(agentType)] {
			t.Errorf("specialized tool %q not found in registry; got %v", agentType, names)
		}
	}
}

func TestBuildActiveRegistry_WebSearchAbsent_WhenNoSearcher(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)

	for _, n := range reg.Names() {
		if n == "web_search" {
			t.Errorf("web_search should not be in registry when no search backend configured")
		}
	}
}

func TestBuildActiveRegistry_ResearchAbsent_WhenNoSearcher(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)

	for _, n := range reg.Names() {
		if n == "research" {
			t.Errorf("research agent should not be in registry when no search backend configured")
		}
	}
}

func TestBuildActiveRegistry_DelegateAbsent_WhenDisabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: false}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)

	for _, n := range reg.Names() {
		if n == delegation.DelegateToolName {
			t.Errorf("delegate tool present in registry when sub_agent.enabled=false")
		}
	}
}

func TestBuildActiveRegistry_DisabledReturnsSamePointer(t *testing.T) {
	base := tool.NewRegistry()
	subAgentCfg := config.SubAgentConfig{Enabled: false}
	cfg := config.Config{}
	reg := buildActiveRegistry(base, subAgentCfg, stubProvider{}, noopSink{}, "/tmp", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil)
	if reg != base {
		t.Error("expected same registry pointer when sub_agent disabled")
	}
}

// TestAgentTypesSyncWithValidation ensures that the agent types in delegation.AllAgentTypes
// are in sync with config validation. This prevents drift between the two duplicated lists.
func TestAgentTypesSyncWithValidation(t *testing.T) {
	// Each agent type from delegation.AllAgentTypes should validate without error.
	for _, agentType := range delegation.AllAgentTypes() {
		t.Run(string(agentType), func(t *testing.T) {
			_, err := loadConfigWithSubAgentAgents(t, map[string]string{
				string(agentType): "default",
			})
			if err != nil {
				t.Fatalf("validation failed for agent type %q: %v", agentType, err)
			}
		})
	}

	t.Run("unknown_agent", func(t *testing.T) {
		_, err := loadConfigWithSubAgentAgents(t, map[string]string{
			"unknown_agent": "default",
		})
		if err == nil {
			t.Fatal("validation should have failed for unknown agent type, but got no error")
		}
		if !strings.Contains(err.Error(), `sub_agent.agents contains unknown agent type "unknown_agent"`) {
			t.Fatalf("error = %v, want unknown agent type validation error", err)
		}
	})
}

func loadConfigWithSubAgentAgents(t *testing.T, agents map[string]string) (config.Config, error) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "steiner.yaml")
	var b strings.Builder
	b.WriteString("sub_agent:\n  agents:\n")
	for name, model := range agents {
		b.WriteString("    ")
		b.WriteString(name)
		b.WriteString(":\n      model: ")
		b.WriteString(model)
		b.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return config.Load(config.LoadOptions{
		ProjectConfigPath: path,
		WorkingDir:        dir,
		HomeDir:           dir,
	})
}

// Ensure delegation package's AgentRunner interface is satisfied by agent.Runner
// (compile-time check via assignment).
var _ delegation.AgentRunner = agent.NewRunner()
