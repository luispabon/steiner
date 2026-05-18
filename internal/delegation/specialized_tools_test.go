package delegation

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// minimalDeps returns a SpecializedToolDeps suitable for unit tests.
// It uses a noop event sink, stub provider, empty registry, and a
// configurable mock runner.
func minimalDeps(runner AgentRunner) SpecializedToolDeps {
	return SpecializedToolDeps{
		DelegateHandlerDeps: DelegateHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     "/tmp/work",
		},
		ModelResolver: nil,
	}
}

func TestSpecializedToolDef(t *testing.T) {
	agentTypes := AllAgentTypes()
	if len(agentTypes) == 0 {
		t.Fatal("AllAgentTypes returned empty slice")
	}

	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	for _, agentType := range agentTypes {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			def := SpecializedToolDef(agentType, deps)

			if def.Name != string(agentType) {
				t.Errorf("Name=%q, want %q", def.Name, string(agentType))
			}
			if def.Description == "" {
				t.Error("Description is empty")
			}
			if def.Handler == nil {
				t.Fatal("Handler is nil")
			}
			if def.Approval != config.ApprovalModeAuto {
				t.Errorf("Approval=%v, want %v", def.Approval, config.ApprovalModeAuto)
			}

			schemaType, ok := def.ParameterSchema["type"].(string)
			if !ok || schemaType != "object" {
				t.Errorf("schema type=%v, want 'object'", def.ParameterSchema["type"])
			}
			props, ok := def.ParameterSchema["properties"].(map[string]any)
			if !ok {
				t.Fatal("properties missing from schema")
			}
			if _, hasTask := props["task"]; !hasTask {
				t.Error("schema properties missing 'task'")
			}
			// Specialized tools expose only "task", not context/system_prompt/max_turns/timeout.
			if _, hasCtx := props["context"]; hasCtx {
				t.Error("schema properties should not expose 'context'")
			}
			required, ok := def.ParameterSchema["required"].([]any)
			if !ok {
				t.Fatal("required missing from schema")
			}
			if len(required) != 1 {
				t.Errorf("required fields count=%d, want 1", len(required))
			}
			if len(required) > 0 && required[0] != "task" {
				t.Errorf("required[0]=%v, want 'task'", required[0])
			}
		})
	}
}

func TestSpecializedHandler_EmptyTask(t *testing.T) {
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return agent.RunState{}, nil
	}}

	for _, agentType := range AllAgentTypes() {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			def := SpecializedToolDef(agentType, minimalDeps(runner))

			_, err := def.Handler(context.Background(), map[string]any{})
			if err == nil {
				t.Error("expected error for missing task")
			}

			_, err = def.Handler(context.Background(), map[string]any{"task": ""})
			if err == nil {
				t.Error("expected error for empty task string")
			}

			if !strings.Contains(err.Error(), string(agentType)) {
				t.Errorf("error %q should mention agent type %q", err.Error(), agentType)
			}
		})
	}
}

func TestAllSpecializedToolDefs_Count(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	defs := AllSpecializedToolDefs(deps)

	want := len(AllAgentTypes())
	if len(defs) != want {
		t.Errorf("AllSpecializedToolDefs returned %d defs, want %d", len(defs), want)
	}

	// All names must be distinct.
	seen := make(map[string]bool, len(defs))
	for _, def := range defs {
		if seen[def.Name] {
			t.Errorf("duplicate tool name %q", def.Name)
		}
		seen[def.Name] = true
	}

	// Each name must match a known agent type.
	for _, def := range defs {
		if !ValidAgentType(def.Name) {
			t.Errorf("tool name %q is not a valid agent type", def.Name)
		}
	}
}

func TestSpecializedHandler_UsesTypeSystemPrompt(t *testing.T) {
	// Verify that the handler builds the child RunRequest with the correct
	// system prompt for the agent type. We capture the RunRequest via the
	// mock runner and inspect the prompt.
	for _, agentType := range AllAgentTypes() {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			var capturedReq agent.RunRequest
			runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				capturedReq = req
				return successRunState(), nil
			}}

			deps := minimalDeps(runner)
			def := SpecializedToolDef(agentType, deps)

			_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// The system prompt from AgentSystemPrompt should appear in the
			// assembled prompt. It is set as a PromptOverride, so it appears
			// in PromptOverrides.System.
			expectedPrompt := AgentSystemPrompt(agentType)
			got := capturedReq.Prompt.PromptOverrides.System
			if got != expectedPrompt {
				t.Errorf("PromptOverrides.System=%q, want prompt for agent type %q", got, agentType)
			}
		})
	}
}

func TestSpecializedHandler_UsesTypeAllowedTools(t *testing.T) {
	// Verify that the handler restricts child registries to the per-type allowlist.
	// We register all allowed tools in the parent registry and confirm that
	// only the expected tools reach the child.
	for _, agentType := range AllAgentTypes() {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			allowedTools := AgentAllowedTools(agentType)
			if len(allowedTools) == 0 {
				t.Skip("agent type has empty allowlist; skipping")
			}

			// Build parent registry with all allowed tools plus an extra one.
			allDefs := make([]tool.ToolDef, 0, len(allowedTools)+1)
			for _, name := range allowedTools {
				allDefs = append(allDefs, tool.ToolDef{Name: name, Description: name})
			}
			allDefs = append(allDefs, tool.ToolDef{Name: "not_allowed", Description: "not allowed"})

			var capturedReq agent.RunRequest
			runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				capturedReq = req
				return successRunState(), nil
			}}

			deps := SpecializedToolDeps{
				DelegateHandlerDeps: DelegateHandlerDeps{
					SubAgentCfg: config.SubAgentConfig{},
					Provider:    stubProvider{},
					ParentReg:   tool.NewRegistry(allDefs...),
					Runner:      runner,
					Events:      noopEventSink{},
					WorkDir:     "/tmp/work",
				},
				ModelResolver: nil,
			}
			def := SpecializedToolDef(agentType, deps)

			_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// "not_allowed" must not appear in the child's visible tools.
			for _, ts := range capturedReq.Tools {
				if ts.Function.Name == "not_allowed" {
					t.Error("child tools contain 'not_allowed' tool")
				}
			}
			// "delegate" must never appear in child tools.
			for _, ts := range capturedReq.Tools {
				if ts.Function.Name == DelegateToolName {
					t.Error("child tools contain delegate tool")
				}
			}
		})
	}
}

func TestSpecializedHandler_ReturnsExecutionResult(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SpecializedToolDef(AgentTypeExplore, deps)

	raw, err := def.Handler(context.Background(), map[string]any{"task": "explore something"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Errorf("handler returned %T, want tool.ExecutionResult", raw)
	}
}

func TestSpecializedHandler_UsesPerTypeModel(t *testing.T) {
	// Configure a model alias for a specific agent type.
	// Provide a ModelResolver that records whether it was called and with what alias.
	// Verify that the handler uses the resolved model.
	agentType := AgentTypeExplore
	expectedModelAlias := "custom-model"
	resolverCalled := false
	var resolverCalledWith string
	var capturedReq agent.RunRequest

	testProvider := stubProvider{}
	testModel := provider.ResolvedModel{
		Alias:           expectedModelAlias,
		BackendModelID:  "backend-custom-model",
		EffectiveLimits: provider.EffectiveLimits{ContextWindow: 8000, MaxOutputTokens: 2000},
	}

	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalled = true
		resolverCalledWith = alias
		return testProvider, testModel, nil
	}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		DelegateHandlerDeps: DelegateHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{
				Agents: map[string]config.AgentConfig{
					string(agentType): {Model: expectedModelAlias},
				},
			},
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
	}

	def := SpecializedToolDef(agentType, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resolverCalled {
		t.Error("ModelResolver was not called")
	}
	if resolverCalledWith != expectedModelAlias {
		t.Errorf("ModelResolver called with %q, want %q", resolverCalledWith, expectedModelAlias)
	}
	if capturedReq.ResolvedModel.Alias != expectedModelAlias {
		t.Errorf("child RunRequest ResolvedModel.Alias=%q, want %q", capturedReq.ResolvedModel.Alias, expectedModelAlias)
	}
}

func TestSpecializedHandler_FallsBackWithoutModelConfig(t *testing.T) {
	// No Agents entry for the agent type.
	// ModelResolver is provided but should NOT be called.
	// Should fall back to parent model.
	agentType := AgentTypeExplore
	resolverCalled := false

	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		resolverCalled = true
		return nil, provider.ResolvedModel{}, fmt.Errorf("should not be called")
	}

	var capturedReq agent.RunRequest
	parentModel := provider.ResolvedModel{BackendModelID: "parent-model"}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		DelegateHandlerDeps: DelegateHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{
				Agents: map[string]config.AgentConfig{}, // No entry for agentType
			},
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: parentModel,
		},
		ModelResolver: modelResolver,
	}

	def := SpecializedToolDef(agentType, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resolverCalled {
		t.Error("ModelResolver should not have been called")
	}
	if capturedReq.ResolvedModel.BackendModelID != parentModel.BackendModelID {
		t.Errorf("child RunRequest BackendModelID=%q, want %q", capturedReq.ResolvedModel.BackendModelID, parentModel.BackendModelID)
	}
}

func TestSpecializedHandler_FallsBackWithNilResolver(t *testing.T) {
	// Agents entry exists with a model alias, but ModelResolver is nil.
	// Should fall back to parent model without error.
	agentType := AgentTypeExplore
	var capturedReq agent.RunRequest
	parentModel := provider.ResolvedModel{BackendModelID: "parent-model"}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		DelegateHandlerDeps: DelegateHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{
				Agents: map[string]config.AgentConfig{
					string(agentType): {Model: "some-alias"},
				},
			},
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: parentModel,
		},
		ModelResolver: nil,
	}

	def := SpecializedToolDef(agentType, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq.ResolvedModel.BackendModelID != parentModel.BackendModelID {
		t.Errorf("child RunRequest BackendModelID=%q, want %q", capturedReq.ResolvedModel.BackendModelID, parentModel.BackendModelID)
	}
}

func TestSpecializedHandler_ModelResolverError(t *testing.T) {
	// ModelResolver returns an error.
	// Handler should return that error.
	agentType := AgentTypeExplore
	expectedAlias := "bad-model"
	expectedErr := fmt.Errorf("model not found")

	modelResolver := func(alias string) (provider.Provider, provider.ResolvedModel, error) {
		return nil, provider.ResolvedModel{}, expectedErr
	}

	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		DelegateHandlerDeps: DelegateHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{
				Agents: map[string]config.AgentConfig{
					string(agentType): {Model: expectedAlias},
				},
			},
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
	}

	def := SpecializedToolDef(agentType, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test task"})
	if err == nil {
		t.Fatal("expected error from ModelResolver")
	}
	if !strings.Contains(err.Error(), string(agentType)) {
		t.Errorf("error %q should mention agent type %q", err.Error(), agentType)
	}
	if !strings.Contains(err.Error(), expectedAlias) {
		t.Errorf("error %q should mention model alias %q", err.Error(), expectedAlias)
	}
}
