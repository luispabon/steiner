package delegation

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// minimalDeps returns a SpecializedToolDeps suitable for unit tests.
// It uses a noop event sink, stub provider, empty registry, and a
// configurable mock runner.
func minimalDeps(runner AgentRunner) SpecializedToolDeps {
	return SpecializedToolDeps{
		SubAgentCfg: config.SubAgentConfig{},
		Provider:    stubProvider{},
		ParentReg:   tool.NewRegistry(),
		Runner:      runner,
		Events:      noopEventSink{},
		WorkDir:     "/tmp/work",
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
				SubAgentCfg: config.SubAgentConfig{},
				Provider:    stubProvider{},
				ParentReg:   tool.NewRegistry(allDefs...),
				Runner:      runner,
				Events:      noopEventSink{},
				WorkDir:     "/tmp/work",
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
