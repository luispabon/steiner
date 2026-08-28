package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/advisor"
	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/sandbox"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestBuildRunRequestDelegationParallelism(t *testing.T) {
	for _, tt := range []struct {
		name    string
		enabled bool
		width   int
		wantFn  bool
		wantMax int
	}{
		{name: "enabled", enabled: true, width: 5, wantFn: true, wantMax: 5},
		{name: "disabled", enabled: false, width: 5, wantFn: false, wantMax: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := cliRunner{runtime: cliRuntime{cfg: config.Config{
				SubAgent: config.SubAgentConfig{Enabled: tt.enabled, MaxParallel: tt.width},
			}}}
			req := buildRunRequest(r, runnerSetup{}, tool.NewRegistry(), nil, nil)
			if (req.ParallelTool != nil) != tt.wantFn {
				t.Fatalf("ParallelTool set = %v, want %v", req.ParallelTool != nil, tt.wantFn)
			}
			if req.MaxParallelTools != tt.wantMax {
				t.Fatalf("MaxParallelTools = %d, want %d", req.MaxParallelTools, tt.wantMax)
			}
			if tt.enabled {
				if !req.ParallelTool("code") {
					t.Fatal("ParallelTool(code) = false, want true")
				}
				if req.ParallelTool("read") {
					t.Fatal("ParallelTool(read) = true, want false")
				}
			}
		})
	}
}

func TestBuildRunRequestSnapshotsVisionCapabilities(t *testing.T) {
	shared := agent.NewVisionCapabilities(false)
	shared.SetDerived("known", agent.VisionCapable)
	shared.LatchIncapable("latched")
	r := cliRunner{runtime: cliRuntime{visionCapabilities: shared}}

	first := buildRunRequest(r, runnerSetup{}, tool.NewRegistry(), nil, nil)
	if first.VisionCapabilities == nil {
		t.Fatal("first request vision capabilities = nil, want snapshot")
	}
	if first.VisionCapabilities == shared {
		t.Fatal("first request shares vision capabilities with runtime")
	}
	if got := first.VisionCapabilities.Get("known"); got != agent.VisionCapable {
		t.Fatalf("first request known capability = %v, want VisionCapable", got)
	}
	if got := first.VisionCapabilities.Get("latched"); got != agent.VisionIncapable {
		t.Fatalf("first request latched capability = %v, want VisionIncapable", got)
	}

	if !first.VisionCapabilities.TakeNotify("latched") {
		t.Fatal("first request notification = false, want true")
	}
	if shared.TakeNotify("latched") {
		t.Fatal("shared notification repeated after first request")
	}
	if !first.VisionCapabilities.LatchIncapable("runtime-latched") {
		t.Fatal("first request runtime latch = false, want true")
	}

	shared.SetSubAgentConfigured(true)
	if first.VisionCapabilities.SubAgentConfigured() {
		t.Fatal("first request vision capabilities changed after runtime update")
	}

	second := buildRunRequest(r, runnerSetup{}, tool.NewRegistry(), nil, nil)
	if second.VisionCapabilities == nil {
		t.Fatal("second request vision capabilities = nil, want snapshot")
	}
	if second.VisionCapabilities == shared {
		t.Fatal("second request shares vision capabilities with runtime")
	}
	if !second.VisionCapabilities.SubAgentConfigured() {
		t.Fatal("second request vision capabilities = false, want live runtime value")
	}
	if got := second.VisionCapabilities.Get("runtime-latched"); got != agent.VisionIncapable {
		t.Fatalf("second request runtime-latched capability = %v, want VisionIncapable", got)
	}
	if second.VisionCapabilities.TakeNotify("latched") {
		t.Fatal("second request repeated notification from first request")
	}
}

func TestBuildRunRequestLimitsModelCallTimeout(t *testing.T) {
	for _, tt := range []struct {
		name        string
		turnTimeout string
		wantTimeout time.Duration
	}{
		{name: "default 10m", turnTimeout: "10m", wantTimeout: 10 * time.Minute},
		{name: "custom 5m", turnTimeout: "5m", wantTimeout: 5 * time.Minute},
		{name: "zero disables", turnTimeout: "0s", wantTimeout: 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			timeout := config.MustDuration(tt.turnTimeout)
			r := cliRunner{runtime: cliRuntime{cfg: config.Config{
				Limits: config.LimitsConfig{ModelCallTimeout: timeout},
			}}}
			req := buildRunRequest(r, runnerSetup{}, tool.NewRegistry(), nil, nil)
			if req.Limits.ModelCallTimeout != tt.wantTimeout {
				t.Errorf("ModelCallTimeout = %v, want %v", req.Limits.ModelCallTimeout, tt.wantTimeout)
			}
		})
	}
}

func TestNewDelegateDepsDelegationModelIsBaseResolvedModel(t *testing.T) {
	setup := runnerSetup{
		resolvedModel: provider.ResolvedModel{
			BackendModelID:           "override-model",
			ReasoningEffectiveEffort: "max",
			EffectiveLimits: provider.EffectiveLimits{
				ContextWindow:   32768,
				MaxOutputTokens: 4096,
			},
		},
		baseResolvedModel: provider.ResolvedModel{
			BackendModelID:           "base-model",
			ReasoningEffectiveEffort: "high",
			EffectiveLimits: provider.EffectiveLimits{
				ContextWindow:   32768,
				MaxOutputTokens: 2048,
			},
		},
	}

	deps := (cliRunner{}).newDelegateDeps(setup, nil, nil, nil, "")
	if deps.ResolvedModel.BackendModelID != "base-model" {
		t.Errorf("ResolvedModel.BackendModelID = %q, want base-model", deps.ResolvedModel.BackendModelID)
	}
	if deps.ResolvedModel.ReasoningEffectiveEffort != "high" {
		t.Errorf("ResolvedModel.ReasoningEffectiveEffort = %q, want high", deps.ResolvedModel.ReasoningEffectiveEffort)
	}
	if deps.MaxTokens != setup.resolvedModel.EffectiveLimits.MaxOutputTokens {
		t.Errorf("MaxTokens = %d, want %d", deps.MaxTokens, setup.resolvedModel.EffectiveLimits.MaxOutputTokens)
	}
}

func TestNewDelegateDepsUsesCurrentEffectiveAssignments(t *testing.T) {
	frozen := config.EffectiveModelAssignments{ProfileName: "default", DefaultModel: "base"}
	live := config.EffectiveModelAssignments{
		ProfileName:             "fast",
		DefaultModel:            "fast",
		Advisor:                 "advisor-fast",
		SubAgents:               map[string]string{"code": "code-fast", string(delegation.AgentTypeVision): "vision-fast"},
		OneShot:                 map[string]string{"plan": "plan-fast"},
		WorkflowHandoff:         map[string]string{"implement": "handoff-fast"},
		ActiveOrchestratorModel: "active",
	}
	r := cliRunner{
		runtime:          cliRuntime{cfg: config.Config{Models: config.ModelsConfig{Effective: frozen}}},
		currentEffective: func() config.EffectiveModelAssignments { return live },
	}

	deps := r.newDelegateDeps(runnerSetup{}, nil, nil, nil, "")
	if !reflect.DeepEqual(deps.Config.Models.Effective, live) {
		t.Fatalf("delegate effective assignments = %#v, want %#v", deps.Config.Models.Effective, live)
	}
	got := deps.Config.Models.Effective
	if got.ProfileName != "fast" || got.DefaultModel != "fast" || got.Advisor != "advisor-fast" {
		t.Fatalf("delegate general assignments = %#v, want live profile/default/advisor", got)
	}
	if got.SubAgents["code"] != "code-fast" || got.SubAgents[string(delegation.AgentTypeVision)] != "vision-fast" {
		t.Fatalf("delegate sub-agent assignments = %#v, want live code and vision models", got.SubAgents)
	}
	if got.OneShot["plan"] != "plan-fast" {
		t.Fatalf("delegate oneshot assignments = %#v, want live plan model", got.OneShot)
	}
	if got.WorkflowHandoff["implement"] != "handoff-fast" {
		t.Fatalf("delegate workflow handoff assignments = %#v, want live implement model", got.WorkflowHandoff)
	}
	if got.ActiveOrchestratorModel != "active" || got.DefaultModel == got.ActiveOrchestratorModel {
		t.Fatalf("delegate active/default models = %q/%q, want distinct active/default assignments", got.ActiveOrchestratorModel, got.DefaultModel)
	}
	if !reflect.DeepEqual(r.runtime.cfg.Models.Effective, frozen) {
		t.Fatalf("runtime effective assignments changed: got %#v, want %#v", r.runtime.cfg.Models.Effective, frozen)
	}
}

func TestToProviderConversationPreservesTurn(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{
			Role:    agent.MessageRoleAssistant,
			Content: "world",
			ToolCalls: []agent.ToolCall{
				{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "file.txt"}},
			},
			Turn: 5,
		},
		{
			Role:       agent.MessageRoleTool,
			Content:    "result",
			ToolCallID: "call_1",
			Turn:       5,
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

func TestToProviderConversationSanitizesDanglingToolCalls(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{
			Role:    agent.MessageRoleAssistant,
			Content: "searching",
			ToolCalls: []agent.ToolCall{
				{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
			},
			Turn: 5,
		},
		{Role: agent.MessageRoleTool, Content: "wrong result", ToolCallID: "other", Turn: 5},
		{Role: agent.MessageRoleAssistant, Content: "retrying", Turn: 6},
	}

	got := toProviderConversation(messages)
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3", len(got))
	}
	if len(got[1].ToolCalls) != 0 {
		t.Fatalf("got[1].ToolCalls = %#v, want cleared dangling tool calls", got[1].ToolCalls)
	}
	if got[2].Role != provider.MessageRoleAssistant || got[2].Content != "retrying" {
		t.Fatalf("got[2] = %#v, want trailing assistant preserved", got[2])
	}
}

func TestToProviderConversationPreservesPairedToolResults(t *testing.T) {
	messages := []agent.Message{
		{Role: agent.MessageRoleUser, Content: "hello", Turn: 4},
		{
			Role:    agent.MessageRoleAssistant,
			Content: "searching",
			ToolCalls: []agent.ToolCall{
				{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "a.txt"}},
			},
			Turn: 5,
		},
		{Role: agent.MessageRoleTool, Content: "file contents", ToolCallID: "call_1", Turn: 5},
		{Role: agent.MessageRoleAssistant, Content: "done", Turn: 6},
	}

	got := toProviderConversation(messages)
	if len(got) != 4 {
		t.Fatalf("len(got) = %d, want 4", len(got))
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("got[1].ToolCalls = %#v, want paired tool call preserved", got[1].ToolCalls)
	}
	if got[2].Role != provider.MessageRoleTool || got[2].ToolCallID != "call_1" {
		t.Fatalf("got[2] = %#v, want paired tool result preserved", got[2])
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

func TestBuildActiveRegistry_DelegateNotRegistered_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	found := false
	foundFollowUp := false
	for _, n := range reg.Names() {
		if n == "delegate" {
			found = true
		}
		if n == delegation.FollowUpToolName {
			foundFollowUp = true
		}
	}
	if found {
		t.Errorf("delegate tool found in registry after removal; should not be registered; got %v", reg.Names())
	}
	if !foundFollowUp {
		t.Errorf("follow_up tool not found in registry when sub_agent.enabled=true; got %v", reg.Names())
	}

	// base registry must not be polluted
	for _, n := range base.Names() {
		if n == "delegate" || n == delegation.FollowUpToolName {
			t.Errorf("delegation tool %q leaked into base registry", n)
		}
	}
}

func TestBuildActiveRegistry_SpecializedToolsPresent_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	subAgentCfg := config.SubAgentConfig{Enabled: true}
	cfg := config.Config{}
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	names := reg.Names()
	nameSet := make(map[string]bool, len(names))
	for _, n := range names {
		nameSet[n] = true
	}

	// Research and vision agents are excluded when not configured.
	for _, agentType := range delegation.AllAgentTypes() {
		if agentType == delegation.AgentTypeResearch {
			if nameSet[string(agentType)] {
				t.Errorf("research tool should not be in registry when no searcher configured")
			}
			continue
		}
		if agentType == delegation.AgentTypeVision {
			if nameSet[string(agentType)] {
				t.Errorf("vision tool should not be in registry when no vision model configured")
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
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

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
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

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
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	for _, n := range reg.Names() {
		if n == "delegate" || n == delegation.FollowUpToolName {
			t.Errorf("delegation tool %q present in registry when sub_agent.enabled=false", n)
		}
	}
}

func TestBuildActiveRegistry_DisabledReturnsSamePointer(t *testing.T) {
	base := tool.NewRegistry()
	subAgentCfg := config.SubAgentConfig{Enabled: false}
	cfg := config.Config{}
	reg, err := buildActiveRegistry(base, subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}
	if reg != base {
		t.Error("expected same registry pointer when sub_agent disabled")
	}
}

func TestBuildActiveRegistry_AdvisorPresent_WhenEnabled(t *testing.T) {
	base := tool.NewRegistry(tool.ToolDef{Name: "bash", Description: "run bash"})
	advisorCfg := config.AdvisorConfig{Enabled: true, MaxUsesPerRun: 2}
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"testprov": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{
			Effective: config.EffectiveModelAssignments{Advisor: "advisor-alias"},
			Definitions: map[string]config.ModelConfig{
				"advisor-alias": {Provider: "testprov", ID: "advisor-model"},
			},
		},
	}

	reg, err := buildActiveRegistry(base, config.SubAgentConfig{}, advisorCfg, stubProvider{}, noopSink{}, "/tmp", "", provider.ResolvedModel{ProviderAlias: "testprov", EffectiveProviderType: config.ProviderTypeOpenAICompat}, 0, false, nil, cfg, nil, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	if _, ok := reg.Get(advisor.ToolName); !ok {
		t.Fatalf("advisor tool not found in registry; got %v", reg.Names())
	}
	if _, ok := base.Get(advisor.ToolName); ok {
		t.Fatalf("advisor tool leaked into base registry; got %v", base.Names())
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
		if !strings.Contains(err.Error(), `models.profiles["default"].sub_agents contains unknown agent type "unknown_agent"`) {
			t.Fatalf("error = %v, want unknown agent type validation error", err)
		}
	})
}

func loadConfigWithSubAgentAgents(t *testing.T, agents map[string]string) (config.Config, error) {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "steiner.yaml")
	var b strings.Builder
	b.WriteString("models:\n  profiles:\n    default:\n      default_model: default\n      sub_agents:\n")
	for name, model := range agents {
		b.WriteString("        ")
		b.WriteString(name)
		b.WriteString(": ")
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

// failProvider is a provider stub that errors immediately on any call, so a
// sub-agent run triggered in tests exits without blocking.
type failProvider struct{}

func (failProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errors.New("fail")
}
func (failProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, errors.New("fail")
}
func (failProvider) SupportsUsageStats() bool { return false }

// TestBuildActiveRegistry_ModelResolverSetsReasoningEchoBack guards the fix for
// the sub-agent reasoning-echo regression: the modelResolver closure inside
// buildActiveRegistry must call ResolveWithDiscovery (not Resolve) so that
// ReasoningEchoBack is read from the models.dev cache. Without it,
// stripReasoningContent removes the field that interleaved-reasoning models
// (deepseek, kimi) require echoed back on every turn, causing a 400 on turn 2.
func TestBuildActiveRegistry_ModelResolverSetsReasoningEchoBack(t *testing.T) {
	// Write a minimal models.dev cache marking the test model as interleaved
	// reasoning (interleaved.field = "reasoning_content" → ReasoningEchoBack=true).
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	cacheDir := filepath.Join(cacheRoot, "steiner", "model-metadata")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cacheJSON := `{"testprov":{"models":{"reasoning-model":{"interleaved":{"field":"reasoning_content"}}}}}`
	if err := os.WriteFile(filepath.Join(cacheDir, "models.dev.json"), []byte(cacheJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(cache): %v", err)
	}
	// expires_at far in the future so LoadBestEffort skips the network refresh.
	metaJSON := `{"downloaded_at":"2026-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","url":"https://models.dev/api.json","schema_version":"1"}`
	if err := os.WriteFile(filepath.Join(cacheDir, "models.dev.meta.json"), []byte(metaJSON), 0o644); err != nil {
		t.Fatalf("WriteFile(meta): %v", err)
	}

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"testprov": {Type: config.ProviderTypeOpenAICompat, BaseURL: "http://localhost:11434/v1"},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"reasoning-alias": {Provider: "testprov", ID: "reasoning-model"},
			},
			Effective: config.EffectiveModelAssignments{SubAgents: map[string]string{
				string(delegation.AgentTypeExplore): "reasoning-alias",
			}},
		},
	}
	subAgentCfg := config.SubAgentConfig{
		Enabled: true,
	}

	// providerFactory captures the ResolvedModel passed by modelResolver and
	// returns a failProvider so the subsequent sub-agent run exits immediately.
	var capturedModel provider.ResolvedModel
	providerFactory := func(rm provider.ResolvedModel) (provider.Provider, error) {
		capturedModel = rm
		return failProvider{}, nil
	}

	reg, err := buildActiveRegistry(tool.NewRegistry(), subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, t.TempDir(), "", provider.ResolvedModel{}, 0, false, nil, cfg, providerFactory, nil, nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	toolDef, ok := reg.Get(string(delegation.AgentTypeExplore))
	if !ok {
		t.Fatalf("specialized tool %q not in registry", delegation.AgentTypeExplore)
	}

	// Invoke the handler. modelResolver (and providerFactory) runs before the
	// agent run starts, so the capture happens even though the run fails fast.
	toolDef.Handler(context.Background(), map[string]any{"task": "test"}) //nolint:errcheck

	if !capturedModel.ReasoningEchoBack {
		t.Error("modelResolver did not set ReasoningEchoBack: Resolve was used instead of ResolveWithDiscovery")
	}
}

func TestBuildActiveRegistry_ModelResolverUsesRuntimeHTTPClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":             "openrouter/reasoning-model",
					"context_length": 262144,
					"top_provider": map[string]any{
						"max_completion_tokens": 16384,
					},
				},
			},
		})
	}))
	defer srv.Close()

	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"openrouter": {Type: config.ProviderTypeOpenRouter, BaseURL: srv.URL},
		},
		Models: config.ModelsConfig{
			Definitions: map[string]config.ModelConfig{
				"reasoning-alias": {Provider: "openrouter", ID: "openrouter/reasoning-model"},
			},
			Effective: config.EffectiveModelAssignments{SubAgents: map[string]string{
				string(delegation.AgentTypeExplore): "reasoning-alias",
			}},
		},
	}
	subAgentCfg := config.SubAgentConfig{
		Enabled: true,
	}

	var capturedModel provider.ResolvedModel
	providerFactory := func(rm provider.ResolvedModel) (provider.Provider, error) {
		capturedModel = rm
		return failProvider{}, nil
	}

	reg, err := buildActiveRegistry(tool.NewRegistry(), subAgentCfg, config.AdvisorConfig{}, stubProvider{}, noopSink{}, t.TempDir(), "", provider.ResolvedModel{}, 0, false, nil, cfg, providerFactory, srv.Client(), nil)
	if err != nil {
		t.Fatalf("buildActiveRegistry() error = %v", err)
	}

	toolDef, ok := reg.Get(string(delegation.AgentTypeExplore))
	if !ok {
		t.Fatalf("specialized tool %q not in registry", delegation.AgentTypeExplore)
	}

	toolDef.Handler(context.Background(), map[string]any{"task": "test"}) //nolint:errcheck

	if got, want := capturedModel.EffectiveLimits.ContextWindow, 262144; got != want {
		t.Fatalf("captured context window = %d, want %d", got, want)
	}
	if got, want := capturedModel.EffectiveLimits.MaxOutputTokens, 16384; got != want {
		t.Fatalf("captured max output tokens = %d, want %d", got, want)
	}
}

func TestPromptAssemblyCarriesSandboxState(t *testing.T) {
	t.Run("sandbox active", func(t *testing.T) {
		cfg := config.Config{
			Sandbox: config.SandboxConfig{
				Enabled: true,
				HostMounts: []config.HostMount{
					{Path: "/host/ro", Mode: "ro"},
					{Path: "/host/rw", Mode: "rw"},
				},
			},
		}
		rt := cliRuntime{
			cfg:     cfg,
			sandbox: sandbox.New(cfg.Sandbox, config.PermissionsConfig{}, "/tmp", "/tmp", "/tmp", "/tmp/tmp"),
		}
		runner := cliRunner{runtime: rt}

		opts := runner.promptAssembly(nil, nil, prompt.ModelTokenBudget{}, config.ModelPrompts{})

		if !opts.SandboxEnabled {
			t.Error("AssemblyOptions.SandboxEnabled = false, want true when sandbox is active")
		}
		if got, want := opts.SandboxWritableMounts, []string{"/host/rw"}; !slices.Equal(got, want) {
			t.Errorf("AssemblyOptions.SandboxWritableMounts = %v, want %v", got, want)
		}
	})

	t.Run("sandbox bypassed", func(t *testing.T) {
		rt := cliRuntime{cfg: config.Config{Sandbox: config.SandboxConfig{Enabled: false}}}
		runner := cliRunner{runtime: rt}

		opts := runner.promptAssembly(nil, nil, prompt.ModelTokenBudget{}, config.ModelPrompts{})

		if opts.SandboxEnabled {
			t.Error("AssemblyOptions.SandboxEnabled = true, want false when sandbox is bypassed")
		}
		if len(opts.SandboxWritableMounts) != 0 {
			t.Errorf("AssemblyOptions.SandboxWritableMounts = %v, want empty when sandbox is bypassed", opts.SandboxWritableMounts)
		}
	})
}

// TestRunnerDelegateDepsCarryRuntimeSandboxState proves the production
// delegation-deps construction inside cliRunner.run threads the runtime sandbox
// state into delegated children. It drives a real cliRunner run that spawns an
// explore sub-agent, then inspects the child session the delegation handler
// saves. buildChildPrompt derives AssemblyOptions.SandboxEnabled and
// SandboxWritableMounts exclusively from DelegateDeps.SandboxEnabled and
// DelegateDeps.SandboxWritableMounts (via SubAgentHandlerDeps), so removing
// either wiring line in runner.go flips these
// fields and fails this test.
//
// The bypassed case is the real production shape where runtime.sandbox is a
// nil *sandbox.Sandbox (sandbox disabled via config/--unsafe, or
// unavailable): SandboxEnabled derives from
// `runtime.sandbox != nil && runtime.sandbox.Enabled()`, which is false for a
// nil pointer without any wrapper normalization.
func TestRunnerDelegateDepsCarryRuntimeSandboxState(t *testing.T) {
	tests := []struct {
		name        string
		sandboxCfg  config.SandboxConfig
		sb          *sandbox.Sandbox
		wantEnabled bool
		wantMounts  []string
	}{
		{
			name: "active sandbox",
			sandboxCfg: config.SandboxConfig{
				Enabled: true,
				HostMounts: []config.HostMount{
					{Path: "/host/ro", Mode: "ro"},
					{Path: "/host/rw1", Mode: "rw"},
					{Path: "/host/rw2", Mode: "rw"},
				},
			},
			sb:          sandbox.New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, "/tmp", "/tmp", "/tmp", "/tmp/tmp"),
			wantEnabled: true,
			wantMounts:  []string{"/host/rw1", "/host/rw2"},
		},
		{
			name:        "sandbox nil pointer (bypassed)",
			sandboxCfg:  config.SandboxConfig{Enabled: false},
			sb:          nil,
			wantEnabled: false,
			wantMounts:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var agentID string
			events := output.SinkFunc(func(event output.Event) {
				if event.Type != output.EventTypeDelegationStarted {
					return
				}
				if payload, ok := event.Payload.(output.DelegationStartedEvent); ok {
					agentID = payload.AgentID
				}
			})

			cfg := testRuntimeConfig("test-model")
			def := cfg.Models.Definitions["test-model"]
			def.Advanced.Limits.ContextWindow = 32768
			cfg.Models.Definitions["test-model"] = def
			cfg.Sandbox = tt.sandboxCfg
			cfg.SubAgent = config.SubAgentConfig{Enabled: true}
			sessions := delegation.NewSessionStore()
			runner := cliRunner{
				runtime: cliRuntime{
					cfg:                    cfg,
					provider:               &fakeProvider{responses: exploreDelegationResponses()},
					registry:               tool.NewRegistry(),
					workDir:                t.TempDir(),
					homeDir:                t.TempDir(),
					events:                 events,
					sandbox:                tt.sb,
					delegationSessionStore: sessions,
				},
				maxTurns: 4,
			}

			if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "delegate a task"}}, nil, nil); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if agentID == "" {
				t.Fatal("no delegation started event captured; explore sub-agent did not run")
			}
			session, ok := sessions.Get(agentID)
			if !ok {
				t.Fatalf("child session for agent %q not saved", agentID)
			}
			if got := session.Request.Prompt.SandboxEnabled; got != tt.wantEnabled {
				t.Errorf("child Prompt.SandboxEnabled = %v, want %v", got, tt.wantEnabled)
			}
			if got := session.Request.Prompt.SandboxWritableMounts; !slices.Equal(got, tt.wantMounts) {
				t.Errorf("child Prompt.SandboxWritableMounts = %v, want %v", got, tt.wantMounts)
			}
		})
	}
}

// exploreDelegationResponses returns the fake provider responses for a parent
// run that spawns one explore sub-agent: parent tool call, child answer, child
// summary turn, then the parent's final answer.
func exploreDelegationResponses() []provider.ChatResponse {
	return []provider.ChatResponse{
		{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{ID: "call_1", Name: "explore", Arguments: map[string]any{"task": "analyze"}},
				},
			},
			FinishReason: "tool_calls",
		},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "child answer"}, FinishReason: "stop"},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "child summary"}, FinishReason: "stop"},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "parent answer"}, FinishReason: "stop"},
	}
}

// TestRunnerDelegateDepsCarrySandboxTmpDir verifies the composition root feeds
// the runtime sandbox tmp dir into DelegateDeps so child executors (here the
// code sub-agent) can rewrite /tmp paths for mutate. With an active sandbox a
// child mutate on /tmp lands under the sandbox tmp dir; in --unsafe mode (nil
// sandbox) the same call is denied for lack of an approver.
func TestRunnerDelegateDepsCarrySandboxTmpDir(t *testing.T) {
	workDir, _ := setupTestRepo(t)
	sandboxTmpDir := filepath.Join(workDir, "sandbox-tmp")
	mustMkdirAll(t, sandboxTmpDir)

	pp := tool.NewPathPolicy(workDir, config.PathsConfig{})
	baseRegistry := tool.NewRegistry(builtin.NewMutateTool(builtin.Env{WorkDir: workDir, PathPolicy: &pp}))

	tests := []struct {
		name       string
		sandboxCfg config.SandboxConfig
		sb         *sandbox.Sandbox
		wantLanded bool
	}{
		{
			name:       "active sandbox passes tmp dir to child executor",
			sandboxCfg: config.SandboxConfig{Enabled: true},
			sb:         sandbox.New(config.SandboxConfig{Enabled: true}, config.PermissionsConfig{}, workDir, workDir, t.TempDir(), sandboxTmpDir),
			wantLanded: true,
		},
		{
			name:       "unsafe mode passes no tmp dir",
			sandboxCfg: config.SandboxConfig{Enabled: false},
			sb:         nil,
			wantLanded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var agentID string
			events := output.SinkFunc(func(event output.Event) {
				if event.Type != output.EventTypeDelegationStarted {
					return
				}
				if payload, ok := event.Payload.(output.DelegationStartedEvent); ok {
					agentID = payload.AgentID
				}
			})

			cfg := testRuntimeConfig("test-model")
			// The code sub-agent's system prompt plus sandbox preamble exceeds the
			// 4096-token default test context; widen it so the child run fits.
			def := cfg.Models.Definitions["test-model"]
			def.Advanced.Limits.ContextWindow = 32768
			cfg.Models.Definitions["test-model"] = def
			cfg.Sandbox = tt.sandboxCfg
			cfg.SubAgent = config.SubAgentConfig{Enabled: true}
			sessions := delegation.NewSessionStore()
			runner := cliRunner{
				runtime: cliRuntime{
					cfg:                    cfg,
					provider:               &fakeProvider{responses: codeDelegationResponses()},
					registry:               baseRegistry,
					workDir:                workDir,
					homeDir:                t.TempDir(),
					events:                 events,
					sandbox:                tt.sb,
					delegationSessionStore: sessions,
				},
				maxTurns: 4,
			}

			if _, err := runner.Run(context.Background(), []agent.Message{{Role: agent.MessageRoleUser, Content: "delegate a coding task"}}, nil, nil); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if agentID == "" {
				t.Fatal("no delegation started event captured; code sub-agent did not run")
			}
			session, ok := sessions.Get(agentID)
			if !ok {
				t.Fatalf("child session for agent %q not saved", agentID)
			}

			_, err := session.Request.Executor.Execute(context.Background(), "mutate", "", map[string]any{
				"operations": []any{
					map[string]any{"type": "create", "path": "/tmp/test-file", "content": "hello\n"},
				},
			})
			if tt.wantLanded {
				if err != nil {
					t.Fatalf("Execute(mutate /tmp) error = %v", err)
				}
				got, err := os.ReadFile(filepath.Join(sandboxTmpDir, "test-file"))
				if err != nil {
					t.Fatalf("read under sandbox tmp dir: %v", err)
				}
				if string(got) != "hello\n" {
					t.Errorf("file content = %q, want %q", string(got), "hello\n")
				}
				return
			}
			var execErr *tool.ToolExecutionError
			if !errors.As(err, &execErr) {
				t.Fatalf("Execute(mutate /tmp) error = %v, want *tool.ToolExecutionError policy_denied", err)
			}
			if execErr.Kind != "policy_denied" {
				t.Fatalf("Execute(mutate /tmp) error kind = %q, want %q", execErr.Kind, "policy_denied")
			}
		})
	}
}

// codeDelegationResponses returns the fake provider responses for a parent run
// that spawns one code sub-agent: parent tool call, child answer, child summary
// turn, then the parent's final answer.
func codeDelegationResponses() []provider.ChatResponse {
	return []provider.ChatResponse{
		{
			Message: provider.Message{
				Role: provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{
					{ID: "call_1", Name: "code", Arguments: map[string]any{"task": "write a file"}},
				},
			},
			FinishReason: "tool_calls",
		},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "child answer"}, FinishReason: "stop"},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "child summary"}, FinishReason: "stop"},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "parent answer"}, FinishReason: "stop"},
	}
}
