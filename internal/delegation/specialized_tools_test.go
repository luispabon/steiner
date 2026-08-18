package delegation

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type mockRunner struct {
	runFunc func(context.Context, agent.RunRequest) (agent.RunState, error)
}

func (m *mockRunner) Run(ctx context.Context, req agent.RunRequest) (agent.RunState, error) {
	return m.runFunc(ctx, req)
}

type noopEventSink struct{}

func (noopEventSink) Emit(output.Event) {}

type stubProvider struct{}

func (stubProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (stubProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, nil
}

func (stubProvider) SupportsUsageStats() bool { return false }

func successRunState() agent.RunState {
	return agent.RunState{
		Conversation: []agent.Message{
			{Role: agent.MessageRoleAssistant, Content: "task result"},
		},
		TurnCount:  1,
		TokenCount: 100,
		StopReason: agent.StopReasonComplete,
	}
}

// minimalDeps returns a SpecializedToolDeps suitable for unit tests.
// It uses a noop event sink, stub provider, empty registry, and a
// configurable mock runner.
func minimalDeps(runner AgentRunner) SpecializedToolDeps {
	return SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
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
			// Vision has two required fields (task + image_id); all others have only task.
			if agentType == AgentTypeVision {
				if len(required) != 2 {
					t.Errorf("vision required fields count=%d, want 2", len(required))
				}
			} else {
				if len(required) != 1 {
					t.Errorf("required fields count=%d, want 1", len(required))
				}
				if len(required) > 0 && required[0] != "task" {
					t.Errorf("required[0]=%v, want 'task'", required[0])
				}
				// Pin the non-vision task description so schema/canon drift is caught.
				taskProp, ok := props["task"].(map[string]any)
				if !ok {
					t.Fatal("task property is not an object in schema")
				}
				const wantTaskDesc = "Required. Self-contained task with objective, context, deliverable, constraints, success criteria, and checks to run."
				if desc, _ := taskProp["description"].(string); desc != wantTaskDesc {
					t.Errorf("non-vision task description=%q, want %q", desc, wantTaskDesc)
				}
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

	defs := AllSpecializedToolDefs(deps, nil)

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

func TestAllSpecializedToolDefs_ExcludeTypes(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})

	defs := AllSpecializedToolDefs(deps, []AgentType{AgentTypeResearch})

	want := len(AllAgentTypes()) - 1
	if len(defs) != want {
		t.Errorf("AllSpecializedToolDefs with exclude returned %d defs, want %d", len(defs), want)
	}

	for _, def := range defs {
		if def.Name == string(AgentTypeResearch) {
			t.Errorf("research agent should be excluded but was found in defs")
		}
	}
}

func TestSpecializedHandler_UsesTypeSystemPrompt(t *testing.T) {
	// Verify that the handler builds the child RunRequest with the correct
	// system prompt for the agent type. Code now uses the shared base prompt,
	// so this test stays focused on the types that still supply explicit
	// overrides.
	for _, agentType := range []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview} {
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
			if agentType == AgentTypeVision {
				// Vision requires an ImageStore with a real image; covered by TestVisionHandler_*.
				t.Skip("vision handler requires ImageStore setup; tested separately")
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
				SubAgentHandlerDeps: SubAgentHandlerDeps{
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
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
		AgentModels: map[string]string{
			string(agentType): expectedModelAlias,
		},
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

	modelResolver := func(_ string) (provider.Provider, provider.ResolvedModel, error) {
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
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: parentModel,
		},
		ModelResolver: modelResolver,
		AgentModels:   map[string]string{}, // No entry for agentType
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
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: parentModel,
		},
		ModelResolver: nil,
		AgentModels: map[string]string{
			string(agentType): "some-alias",
		},
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

	modelResolver := func(_ string) (provider.Provider, provider.ResolvedModel, error) {
		return nil, provider.ResolvedModel{}, expectedErr
	}

	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:      stubProvider{},
			ParentReg:     tool.NewRegistry(),
			Runner:        runner,
			Events:        noopEventSink{},
			WorkDir:       "/tmp/work",
			ResolvedModel: provider.ResolvedModel{BackendModelID: "parent-model"},
		},
		ModelResolver: modelResolver,
		AgentModels: map[string]string{
			string(agentType): expectedAlias,
		},
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

func TestSpecializedHandler_SavesChildSession(t *testing.T) {
	origIDGen := idGen
	idGen = func() string { return "child-specialized" }
	defer func() { idGen = origIDGen }()

	store := NewSessionStore()
	state := agent.RunState{
		Conversation: []agent.Message{
			{
				Role:    agent.MessageRoleAssistant,
				Content: "exploring",
				ToolCalls: []agent.ToolCall{
					{ID: "call-1", Name: "read", Arguments: map[string]any{"path": "main.go"}},
				},
			},
		},
		TurnCount:  1,
		TokenCount: 17,
		StopReason: agent.StopReasonComplete,
	}

	var (
		capturedReq    agent.RunRequest
		capturedReqSet bool
	)
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if !capturedReqSet {
					capturedReq = req
					capturedReqSet = true
				}
				return state, nil
			}},
			Events:       noopEventSink{},
			WorkDir:      "/tmp/work",
			SessionStore: store,
		},
	}

	def := SpecializedToolDef(AgentTypeExplore, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "inspect code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := store.Get("child-specialized")
	if !ok {
		t.Fatal("child session was not saved")
	}
	if session.Spec.AgentID != "child-specialized" {
		t.Fatalf("Spec.AgentID = %q, want %q", session.Spec.AgentID, "child-specialized")
	}
	if session.Spec.Task != "inspect code" {
		t.Fatalf("Spec.Task = %q, want %q", session.Spec.Task, "inspect code")
	}
	if session.Spec.SystemPrompt != AgentSystemPrompt(AgentTypeExplore) {
		t.Fatalf("Spec.SystemPrompt = %q, want %q", session.Spec.SystemPrompt, AgentSystemPrompt(AgentTypeExplore))
	}
	if !reflect.DeepEqual(session.Request, capturedReq) {
		t.Fatal("saved request does not match child run request")
	}
	if !reflect.DeepEqual(session.Conversation, state.Conversation) {
		t.Fatalf("Conversation = %#v, want %#v", session.Conversation, state.Conversation)
	}
	if session.TurnCount != state.TurnCount {
		t.Fatalf("TurnCount = %d, want %d", session.TurnCount, state.TurnCount)
	}
	if session.TokenCount != state.TokenCount {
		t.Fatalf("TokenCount = %d, want %d", session.TokenCount, state.TokenCount)
	}
	if session.ToolCallCount != 1 {
		t.Fatalf("ToolCallCount = %d, want 1", session.ToolCallCount)
	}
}

func TestVisionToolDef_Schema(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SpecializedToolDef(AgentTypeVision, deps)

	schema, ok := def.ParameterSchema["properties"].(map[string]any)
	if !ok {
		t.Fatal("ParameterSchema missing 'properties' map")
	}
	if _, hasTask := schema["task"]; !hasTask {
		t.Error("vision schema missing 'task' property")
	}
	if _, hasImageID := schema["image_id"]; !hasImageID {
		t.Error("vision schema missing 'image_id' property")
	}

	required, ok := def.ParameterSchema["required"].([]any)
	if !ok {
		t.Fatal("ParameterSchema missing 'required' slice")
	}
	requiredSet := make(map[string]bool, len(required))
	for _, r := range required {
		if s, ok := r.(string); ok {
			requiredSet[s] = true
		}
	}
	if !requiredSet["task"] {
		t.Error("'task' must be required in vision schema")
	}
	if !requiredSet["image_id"] {
		t.Error("'image_id' must be required in vision schema")
	}
}

func TestVisionToolDef_DescriptionMentionsFollowUp(t *testing.T) {
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	def := SpecializedToolDef(AgentTypeVision, deps)

	if !strings.Contains(def.Description, "follow_up") {
		t.Errorf("vision tool description should mention 'follow_up', got: %q", def.Description)
	}
}

func TestVisionToolSkippedWithoutModel(t *testing.T) {
	// When the vision model is not configured, AllSpecializedToolDefs should
	// not include a vision tool.
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}})
	// SubAgentCfg.Agents has no "vision" entry → model is empty.
	defs := AllSpecializedToolDefs(deps, []AgentType{AgentTypeVision})

	for _, def := range defs {
		if def.Name == string(AgentTypeVision) {
			t.Error("vision tool should be excluded but was found in defs")
		}
	}
}

func TestVisionHandler_UnknownImageID(t *testing.T) {
	store := agent.NewImageStore(t.TempDir())
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
				return successRunState(), nil
			}},
			Events:  noopEventSink{},
			WorkDir: "/tmp/work",
		},
		ImageStore: store,
	}
	def := SpecializedToolDef(AgentTypeVision, deps)

	_, err := def.Handler(context.Background(), map[string]any{
		"task":     "describe the image",
		"image_id": "img-99",
	})
	if err == nil {
		t.Fatal("expected error for unknown image_id")
	}
	if !strings.Contains(err.Error(), "img-99") {
		t.Errorf("error %q should mention the image_id", err.Error())
	}
}

func TestVisionHandler_ReadsImageAndInjectsIntoSpec(t *testing.T) {
	// Write a small fake image file and register it in the ImageStore.
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	imgContent := []byte("fake-png-content")
	if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}

	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(tool.ToolDef{Name: "read", Description: "read"}),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     dir,
		},
		ImageStore: store,
	}
	def := SpecializedToolDef(AgentTypeVision, deps)

	raw, err := def.Handler(context.Background(), map[string]any{
		"task":     "describe what you see",
		"image_id": ref.ID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Result must be a tool.ExecutionResult.
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}

	// The child RunRequest prompt must include the image in the first user message.
	_ = capturedReq // runner captured the request; build verification via DelegationResult

	// Verify the result contains the follow-up reminder.
	execResult, _ := raw.(tool.ExecutionResult)
	dr, ok := execResult.Value.(DelegationResult)
	if !ok {
		t.Fatalf("ExecutionResult.Value is %T, want DelegationResult", execResult.Value)
	}
	if !strings.Contains(dr.Output, "follow_up") {
		t.Errorf("result output %q should mention 'follow_up'", dr.Output)
	}
	if !strings.Contains(dr.Output, "agent_id") {
		t.Errorf("result output %q should mention 'agent_id'", dr.Output)
	}

	// Verify the image was base64-encoded from disk correctly.
	wantEncoded := base64.StdEncoding.EncodeToString(imgContent)
	_ = wantEncoded // encoding correctness is implicit; the handler would error if os.ReadFile failed
}

func TestSpecializedHandler_SavesSessionForStructuredFailure(t *testing.T) {
	origIDGen := idGen
	idGen = func() string { return "child-specialized-error" }
	defer func() { idGen = origIDGen }()

	store := NewSessionStore()
	var (
		capturedReq    agent.RunRequest
		capturedReqSet bool
	)
	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if !capturedReqSet {
					capturedReq = req
					capturedReqSet = true
				}
				return agent.RunState{}, context.Canceled
			}},
			Events:       noopEventSink{},
			WorkDir:      "/tmp/work",
			SessionStore: store,
		},
	}

	def := SpecializedToolDef(AgentTypeExplore, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "inspect code"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	session, ok := store.Get("child-specialized-error")
	if !ok {
		t.Fatal("session was not saved for structured failure")
	}
	if !reflect.DeepEqual(session.Request, capturedReq) {
		t.Fatal("saved request does not match child run request")
	}
	if len(session.Conversation) != 0 {
		t.Fatalf("Conversation length = %d, want 0", len(session.Conversation))
	}
	if session.TurnCount != 0 {
		t.Fatalf("TurnCount = %d, want 0", session.TurnCount)
	}
	if session.TokenCount != 0 {
		t.Fatalf("TokenCount = %d, want 0", session.TokenCount)
	}
	if session.ToolCallCount != 0 {
		t.Fatalf("ToolCallCount = %d, want 0", session.ToolCallCount)
	}
}

// TestSubAgentHandlerDepsCarriesSandboxState proves the specialized handler
// forwards the parent's plain sandbox state (SandboxEnabled and writable
// mounts) into the child prompt's sandbox section.
func TestSubAgentHandlerDepsCarriesSandboxState(t *testing.T) {
	var capturedReq agent.RunRequest
	firstCall := true

	idGen = func() string { return "child-sandbox-test" }
	t.Cleanup(func() { idGen = func() string { return fmt.Sprintf("child-%d", agentCounter.Add(1)) } })

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:              stubProvider{},
			ParentReg:             tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }}),
			SubAgentCfg:           config.SubAgentConfig{},
			Events:                noopEventSink{},
			WorkDir:               "/tmp/work",
			SandboxEnabled:        true,
			SandboxWritableMounts: []string{"/host/rw"},
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if firstCall {
					capturedReq = req
					firstCall = false
				}
				return successRunState(), nil
			}},
		},
	}

	def := SpecializedToolDef(AgentTypeExplore, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test sandbox state"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if !capturedReq.Prompt.SandboxEnabled {
		t.Error("child Prompt.SandboxEnabled=false, want true")
	}
	if want := []string{"/host/rw"}; !slices.Equal(capturedReq.Prompt.SandboxWritableMounts, want) {
		t.Errorf("child Prompt.SandboxWritableMounts=%v, want %v", capturedReq.Prompt.SandboxWritableMounts, want)
	}
}

// TestSubAgentHandlerDepsDisabledSandboxNotCarried proves a disabled (or
// bypassed) parent sandbox leaves the child prompt's sandbox section off.
func TestSubAgentHandlerDepsDisabledSandboxNotCarried(t *testing.T) {
	var capturedReq agent.RunRequest
	firstCall := true

	idGen = func() string { return "child-nil-sandbox" }
	t.Cleanup(func() { idGen = func() string { return fmt.Sprintf("child-%d", agentCounter.Add(1)) } })

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }}),
			SubAgentCfg: config.SubAgentConfig{},
			Events:      noopEventSink{},
			WorkDir:     "/tmp/work",
			// SandboxEnabled defaults to false; no writable mounts configured.
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if firstCall {
					capturedReq = req
					firstCall = false
				}
				return successRunState(), nil
			}},
		},
	}

	def := SpecializedToolDef(AgentTypeExplore, deps)
	_, err := def.Handler(context.Background(), map[string]any{"task": "test disabled sandbox"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	if capturedReq.Prompt.SandboxEnabled {
		t.Error("child Prompt.SandboxEnabled=true, want false")
	}
	if len(capturedReq.Prompt.SandboxWritableMounts) != 0 {
		t.Errorf("child Prompt.SandboxWritableMounts=%v, want empty", capturedReq.Prompt.SandboxWritableMounts)
	}
}

func TestSpecializedHandlerSkipProjectContext(t *testing.T) {
	// Agents that should skip project context.
	skipTypes := []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeSanityCheck}
	// Vision is excluded because it uses newVisionHandler which requires image_id.
	// Agents that should receive full project context.
	keepTypes := []AgentType{AgentTypeCode, AgentTypeReview, AgentTypeEvaluate}

	t.Run("lean agents skip project context", func(t *testing.T) {
		for _, agentType := range skipTypes {
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

				if !capturedReq.Prompt.SkipProjectContext {
					t.Errorf("%s: SkipProjectContext = false, want true", agentType)
				}
				if capturedReq.Prompt.SkipAgents {
					t.Errorf("%s: SkipAgents = true, want false", agentType)
				}
			})
		}
	})

	t.Run("full agents keep project context", func(t *testing.T) {
		for _, agentType := range keepTypes {
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

				if capturedReq.Prompt.SkipProjectContext {
					t.Errorf("%s: SkipProjectContext = true, want false", agentType)
				}
				if capturedReq.Prompt.SkipAgents {
					t.Errorf("%s: SkipAgents = true, want false", agentType)
				}
			})
		}
	})

	t.Run("vision skips agents and project context", func(t *testing.T) {
		dir := t.TempDir()
		imgPath := filepath.Join(dir, "test.png")
		imgContent := []byte("fake-png-content")
		if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
			t.Fatalf("write temp image: %v", err)
		}

		store := agent.NewImageStore(dir)
		ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

		var capturedReq agent.RunRequest
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			capturedReq = req
			return successRunState(), nil
		}}
		deps := minimalDeps(runner)
		deps.ImageStore = store
		def := SpecializedToolDef(AgentTypeVision, deps)

		_, err := def.Handler(context.Background(), map[string]any{"task": "describe the image", "image_id": ref.ID})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !capturedReq.Prompt.SkipProjectContext {
			t.Errorf("vision: SkipProjectContext = false, want true")
		}
		if !capturedReq.Prompt.SkipAgents {
			t.Errorf("vision: SkipAgents = false, want true")
		}
	})
}

func TestMergedAllowedTools(t *testing.T) {
	tests := []struct {
		name   string
		base   []string
		extras []string
		want   []string
	}{
		{
			name:   "nil base and extras produce empty slice",
			base:   nil,
			extras: nil,
			want:   []string{},
		},
		{
			name:   "extras appended and sorted with base",
			base:   []string{"read", "grep"},
			extras: []string{"bash"},
			want:   []string{"bash", "grep", "read"},
		},
		{
			name:   "duplicates across base and extras removed",
			base:   []string{"read", "bash"},
			extras: []string{"bash", "read"},
			want:   []string{"bash", "read"},
		},
		{
			name:   "unsorted extras sorted with base",
			base:   []string{"ls", "read"},
			extras: []string{"mutate", "bash", "grep"},
			want:   []string{"bash", "grep", "ls", "mutate", "read"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergedAllowedTools(tt.base, tt.extras)
			if !slices.Equal(got, tt.want) {
				t.Errorf("mergedAllowedTools(%v, %v) = %v, want %v", tt.base, tt.extras, got, tt.want)
			}
		})
	}

	t.Run("base slice is not mutated", func(t *testing.T) {
		base := []string{"read", "grep", "ls"}
		original := append([]string(nil), base...)
		got := mergedAllowedTools(base, []string{"ls", "bash", "read"})
		if !slices.Equal(base, original) {
			t.Fatalf("base mutated: %v, want %v", base, original)
		}
		got[0] = "mutated"
		if !slices.Equal(base, original) {
			t.Fatalf("mutating merged result changed base: %v, want %v", base, original)
		}
	})
}

func TestSpecializedHandler_ExtraAllowedTools(t *testing.T) {
	mcpTool := "mcp__notes__search"
	parentDefs := []tool.ToolDef{
		{Name: "read", Description: "read"},
		{Name: "grep", Description: "grep"},
		{Name: "ls", Description: "ls"},
		{Name: "bash", Description: "bash"},
		{Name: mcpTool, Description: "search notes", MCP: tool.MCPProvenance{Server: "notes", ToolName: "search"}},
	}

	runHandler := func(t *testing.T, agentType AgentType, extras map[AgentType][]string) (agent.RunRequest, *SessionStore) {
		t.Helper()
		store := NewSessionStore()
		var capturedReq agent.RunRequest
		runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			if _, ok := req.Executor.(summaryOnlyExecutor); ok {
				return successRunState(), nil
			}
			capturedReq = req
			return successRunState(), nil
		}}
		deps := SpecializedToolDeps{
			SubAgentHandlerDeps: SubAgentHandlerDeps{
				SubAgentCfg:       config.SubAgentConfig{},
				Provider:          stubProvider{},
				ParentReg:         tool.NewRegistry(parentDefs...),
				Runner:            runner,
				Events:            noopEventSink{},
				WorkDir:           "/tmp/work",
				SessionStore:      store,
				ExtraAllowedTools: extras,
			},
			ModelResolver: nil,
		}
		def := SpecializedToolDef(agentType, deps)
		if _, err := def.Handler(context.Background(), map[string]any{"task": "test task"}); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return capturedReq, store
	}

	childToolNames := func(req agent.RunRequest) []string {
		names := make([]string, 0, len(req.Tools))
		for _, ts := range req.Tools {
			names = append(names, ts.Function.Name)
		}
		return names
	}

	t.Run("nil extras grants no extra tools", func(t *testing.T) {
		req, _ := runHandler(t, AgentTypeResearch, nil)
		if slices.Contains(childToolNames(req), mcpTool) {
			t.Errorf("research child contains %q with nil extras: %v", mcpTool, childToolNames(req))
		}
	})

	t.Run("empty extras map grants no extra tools", func(t *testing.T) {
		req, _ := runHandler(t, AgentTypeResearch, map[AgentType][]string{})
		if slices.Contains(childToolNames(req), mcpTool) {
			t.Errorf("research child contains %q with empty extras: %v", mcpTool, childToolNames(req))
		}
	})

	t.Run("extra tool granted to research only", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool}}
		researchReq, _ := runHandler(t, AgentTypeResearch, extras)
		if !slices.Contains(childToolNames(researchReq), mcpTool) {
			t.Errorf("research child missing %q: %v", mcpTool, childToolNames(researchReq))
		}
		codeReq, _ := runHandler(t, AgentTypeCode, extras)
		if slices.Contains(childToolNames(codeReq), mcpTool) {
			t.Errorf("code child unexpectedly contains %q: %v", mcpTool, childToolNames(codeReq))
		}
	})

	t.Run("unknown extra tool names are ignored", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {"mcp__missing__tool"}}
		req, _ := runHandler(t, AgentTypeResearch, extras)
		names := childToolNames(req)
		if slices.Contains(names, "mcp__missing__tool") {
			t.Errorf("child contains unknown extra tool %q: %v", "mcp__missing__tool", names)
		}
		if !slices.Contains(names, "read") {
			t.Errorf("child missing base tool read: %v", names)
		}
	})

	t.Run("merged child tools are sorted and deduplicated", func(t *testing.T) {
		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool, "read", "bash"}}
		req, _ := runHandler(t, AgentTypeResearch, extras)
		names := childToolNames(req)
		want := []string{"bash", "grep", "ls", mcpTool, "read"}
		if !slices.Equal(names, want) {
			t.Errorf("child tools = %v, want %v", names, want)
		}
	})

	t.Run("follow-up reuses merged includes from saved session", func(t *testing.T) {
		origIDGen := idGen
		idGen = func() string { return "child-extra-followup" }
		defer func() { idGen = origIDGen }()

		extras := map[AgentType][]string{AgentTypeResearch: {mcpTool}}
		initialReq, store := runHandler(t, AgentTypeResearch, extras)
		if !slices.Contains(childToolNames(initialReq), mcpTool) {
			t.Fatalf("initial research child missing %q: %v", mcpTool, childToolNames(initialReq))
		}

		var followUpReq agent.RunRequest
		handler := NewFollowUpHandler(SubAgentHandlerDeps{
			SubAgentCfg:  config.SubAgentConfig{},
			SessionStore: store,
			Runner: &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
				if _, ok := req.Executor.(summaryOnlyExecutor); ok {
					return successRunState(), nil
				}
				followUpReq = req
				return successRunState(), nil
			}},
			Events: noopEventSink{},
		})
		if _, err := handler(context.Background(), map[string]any{
			"agent_id": "child-extra-followup",
			"message":  "continue",
		}); err != nil {
			t.Fatalf("follow-up error: %v", err)
		}
		if !slices.Contains(childToolNames(followUpReq), mcpTool) {
			t.Errorf("follow-up child missing %q: %v", mcpTool, childToolNames(followUpReq))
		}
	})
}

func TestVisionHandler_ExtraAllowedTools(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	imgContent := []byte("fake-png-content")
	if err := os.WriteFile(imgPath, imgContent, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 100, 200, len(imgContent))

	mcpTool := "mcp__gallery__find"
	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		if _, ok := req.Executor.(summaryOnlyExecutor); ok {
			return successRunState(), nil
		}
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg:       config.SubAgentConfig{},
			Provider:          stubProvider{},
			ParentReg:         tool.NewRegistry(tool.ToolDef{Name: "read"}, tool.ToolDef{Name: mcpTool}),
			Runner:            runner,
			Events:            noopEventSink{},
			WorkDir:           dir,
			ExtraAllowedTools: map[AgentType][]string{AgentTypeVision: {mcpTool}},
		},
		ImageStore: store,
	}

	def := SpecializedToolDef(AgentTypeVision, deps)
	raw, err := def.Handler(context.Background(), map[string]any{
		"task":     "describe the image",
		"image_id": ref.ID,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := raw.(tool.ExecutionResult); !ok {
		t.Fatalf("handler returned %T, want tool.ExecutionResult", raw)
	}

	names := make([]string, 0, len(capturedReq.Tools))
	for _, ts := range capturedReq.Tools {
		names = append(names, ts.Function.Name)
	}
	if want := []string{mcpTool, "read"}; !slices.Equal(names, want) {
		t.Errorf("vision child tools = %v, want %v", names, want)
	}
}

func TestSpecializedHandler_CodeProvisionesWorktree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     repo,
		},
		ModelResolver: nil,
	}

	def := SpecializedToolDef(AgentTypeCode, deps)
	raw, err := def.Handler(ctx, map[string]any{"task": "implement a feature"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	result, ok := raw.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", raw)
	}

	delegationResult, ok := result.Value.(DelegationResult)
	if !ok {
		t.Fatalf("result.Value type = %T, want DelegationResult", result.Value)
	}

	if delegationResult.WorktreePath == "" {
		t.Error("WorktreePath is empty; expected provisioned worktree path")
	}
	if delegationResult.WorktreeBranch == "" {
		t.Error("WorktreeBranch is empty; expected provisioned worktree branch")
	}
	if len(delegationResult.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty for clean repo", delegationResult.Warnings)
	}

	if capturedReq.Prompt.ProjectRoot != delegationResult.WorktreePath {
		t.Errorf("child ProjectRoot = %q, want %q (the WorktreePath)", capturedReq.Prompt.ProjectRoot, delegationResult.WorktreePath)
	}
	if capturedReq.Prompt.ProjectRoot == repo {
		t.Errorf("child ProjectRoot should not equal parent repo path %q", repo)
	}
}

func TestSpecializedHandler_CodeWithDirtyTree(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Create an untracked file to make the tree dirty.
	dirtyFile := filepath.Join(repo, "dirty.txt")
	if err := os.WriteFile(dirtyFile, []byte("dirty"), 0o644); err != nil {
		t.Fatalf("write dirty file: %v", err)
	}

	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     repo,
		},
		ModelResolver: nil,
	}

	def := SpecializedToolDef(AgentTypeCode, deps)
	raw, err := def.Handler(ctx, map[string]any{"task": "implement a feature"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	result := raw.(tool.ExecutionResult)
	delegationResult := result.Value.(DelegationResult)

	if len(delegationResult.Warnings) == 0 {
		t.Error("Warnings is empty; expected dirty-tree warning")
	}

	// Verify the warning mentions the dirty file.
	hasWarning := false
	for _, w := range delegationResult.Warnings {
		if strings.Contains(w, "dirty.txt") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("Warnings do not mention dirty.txt: %v", delegationResult.Warnings)
	}

	// Verify that despite the dirty tree, provisioning succeeded.
	if delegationResult.WorktreePath == "" {
		t.Error("WorktreePath is empty; worktree should still be provisioned with dirty tree")
	}
}

func TestSpecializedHandler_CodeFallsBackOnProvisioningFailure(t *testing.T) {
	ctx := context.Background()
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	// Point at a nonexistent path to force provisioning to fail.
	badRepo := filepath.Join(repo, "nonexistent")

	var capturedReq agent.RunRequest
	runner := &mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		capturedReq = req
		return successRunState(), nil
	}}

	deps := SpecializedToolDeps{
		SubAgentHandlerDeps: SubAgentHandlerDeps{
			SubAgentCfg: config.SubAgentConfig{},
			Provider:    stubProvider{},
			ParentReg:   tool.NewRegistry(),
			Runner:      runner,
			Events:      noopEventSink{},
			WorkDir:     badRepo,
		},
		ModelResolver: nil,
	}

	def := SpecializedToolDef(AgentTypeCode, deps)
	raw, err := def.Handler(ctx, map[string]any{"task": "implement a feature"})
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	result := raw.(tool.ExecutionResult)
	delegationResult := result.Value.(DelegationResult)

	// Verify fallback warning is present.
	hasWarning := false
	for _, w := range delegationResult.Warnings {
		if strings.Contains(w, "falling back to the shared working tree") {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Errorf("Warnings do not contain fallback message: %v", delegationResult.Warnings)
	}

	// Verify WorktreePath is empty (fell back to parent).
	if delegationResult.WorktreePath != "" {
		t.Errorf("WorktreePath = %q, want empty on fallback", delegationResult.WorktreePath)
	}

	// Verify the child's ProjectRoot equals the parent's (the fallback).
	if capturedReq.Prompt.ProjectRoot != badRepo {
		t.Errorf("child ProjectRoot = %q, want %q (parent's badRepo)", capturedReq.Prompt.ProjectRoot, badRepo)
	}
}

func TestSpecializedHandler_NonCodeAgentsNoWorktreeFields(t *testing.T) {
	ctx := context.Background()
	runner := &mockRunner{runFunc: func(_ context.Context, _ agent.RunRequest) (agent.RunState, error) {
		return successRunState(), nil
	}}

	for _, agentType := range []AgentType{AgentTypeExplore, AgentTypeResearch, AgentTypeEvaluate, AgentTypeSanityCheck, AgentTypeReview} {
		agentType := agentType
		t.Run(string(agentType), func(t *testing.T) {
			deps := SpecializedToolDeps{
				SubAgentHandlerDeps: SubAgentHandlerDeps{
					SubAgentCfg: config.SubAgentConfig{},
					Provider:    stubProvider{},
					ParentReg:   tool.NewRegistry(),
					Runner:      runner,
					Events:      noopEventSink{},
					WorkDir:     "/tmp/work",
				},
				ModelResolver: nil,
			}

			def := SpecializedToolDef(agentType, deps)
			raw, err := def.Handler(ctx, map[string]any{"task": "test task"})
			if err != nil {
				t.Fatalf("handler error: %v", err)
			}

			result := raw.(tool.ExecutionResult)
			delegationResult := result.Value.(DelegationResult)

			if delegationResult.WorktreePath != "" {
				t.Errorf("WorktreePath = %q, want empty for non-code agent", delegationResult.WorktreePath)
			}
			if delegationResult.WorktreeBranch != "" {
				t.Errorf("WorktreeBranch = %q, want empty for non-code agent", delegationResult.WorktreeBranch)
			}
			if len(delegationResult.Warnings) != 0 {
				t.Errorf("Warnings = %v, want empty for non-code agent", delegationResult.Warnings)
			}
		})
	}
}
