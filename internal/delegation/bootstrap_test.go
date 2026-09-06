package delegation

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
	"github.com/luispabon/steiner/internal/usagestats"
)

func TestWithAgentScopeAddsAgentType(t *testing.T) {
	var got output.Event
	sink := withAgentScope("child-1", AgentTypeCode, output.SinkFunc(func(event output.Event) {
		got = event
	}))
	sink.Emit(output.Event{Type: output.EventTypeAPIRequest})
	if got.Scope.AgentID != "child-1" || got.Scope.AgentType != string(AgentTypeCode) {
		t.Fatalf("scope = %#v, want agent ID child-1 and type %s", got.Scope, AgentTypeCode)
	}
}

func assertSharedChildSystemPrompt(t *testing.T, content string) {
	t.Helper()

	for _, want := range []string{
		"You are steiner, a lean coding agent.",
		"Core rules:",
		"Prefer smallest correct change.",
		"### While editing",
		"### Verification",
		"## Final response",
		"## Delegated task",
		"This delegated brief authorizes the work.",
		"Do not ask the user for approval, confirmation, or feedback.",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("shared child system prompt missing %q in %q", want, content)
		}
	}
	for _, forbidden := range []string{
		"not the default implementation worker",
		"## Your sub-agents",
	} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("shared child system prompt unexpectedly contains delegation instructions: %q", content)
		}
	}
	if strings.Contains(content, "Ask the user for confirmation before editing.") {
		t.Fatalf("shared child system prompt unexpectedly contains parent approval guidance: %q", content)
	}
}

func testChildOverride(deps SubAgentHandlerDeps) ChildBootstrapOverrides {
	var names []string
	if deps.ParentReg != nil {
		for _, def := range deps.ParentReg.Definitions() {
			names = append(names, def.Name)
		}
	}
	var allowedTools []string
	switch {
	case slices.Contains(names, "probe"):
		allowedTools = []string{"probe"}
	case slices.Contains(names, "mutate"):
		allowedTools = []string{"mutate"}
	case slices.Contains(names, "bash") && !slices.Contains(names, "read"):
		allowedTools = []string{"bash"}
	case slices.Contains(names, "write"):
		allowedTools = []string{"read", "write"}
	default:
		allowedTools = []string{"read"}
	}
	return ChildBootstrapOverrides{
		AllowedTools:  allowedTools,
		AgentType:     AgentTypeCode,
		Provider:      deps.Provider,
		ResolvedModel: deps.ResolvedModel,
	}
}

func TestChildContextSkips(t *testing.T) {
	tests := []struct {
		agentType          AgentType
		skipProjectContext bool
		skipAgents         bool
	}{
		{AgentTypeExplore, true, false},
		{AgentTypeResearch, true, false},
		{AgentTypeCode, false, false},
		{AgentTypeEvaluate, false, false},
		{AgentTypeSanityCheck, true, false},
		{AgentTypeReview, false, false},
		{AgentTypeVision, true, true},
	}
	for _, tt := range tests {
		t.Run(string(tt.agentType), func(t *testing.T) {
			gotProjectContext, gotAgents := childContextSkips(tt.agentType)
			if gotProjectContext != tt.skipProjectContext || gotAgents != tt.skipAgents {
				t.Errorf("childContextSkips(%q) = (%v, %v), want (%v, %v)", tt.agentType, gotProjectContext, gotAgents, tt.skipProjectContext, tt.skipAgents)
			}
		})
	}
}

func TestChildWorkflowMode(t *testing.T) {
	tests := []struct {
		agentType AgentType
		wantMode  prompt.WorkflowMode
	}{
		{AgentTypeCode, prompt.DelegatedChildWorkflowMode()},
		{AgentTypeExplore, prompt.DelegatedNonCodeChildWorkflowMode()},
		{AgentTypeResearch, prompt.DelegatedNonCodeChildWorkflowMode()},
		{AgentTypeEvaluate, prompt.DelegatedNonCodeChildWorkflowMode()},
		{AgentTypeSanityCheck, prompt.DelegatedNonCodeChildWorkflowMode()},
		{AgentTypeReview, prompt.DelegatedNonCodeChildWorkflowMode()},
		{AgentTypeVision, prompt.DelegatedNonCodeChildWorkflowMode()},
	}
	for _, tt := range tests {
		t.Run(string(tt.agentType), func(t *testing.T) {
			if got := childWorkflowMode(tt.agentType); got != tt.wantMode {
				t.Errorf("childWorkflowMode(%q) = %q, want %q", tt.agentType, got, tt.wantMode)
			}
		})
	}
}

func TestBuildChildRunUsesOverrideProviderAndModel(t *testing.T) {
	rawProvider := stubProvider{name: "raw"}
	resolvedProvider := stubProvider{name: "resolved"}
	deps := SubAgentHandlerDeps{
		Provider:      rawProvider,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "raw-model"},
		ParentReg:     tool.NewRegistry(tool.ToolDef{Name: "read"}),
	}
	override := ChildBootstrapOverrides{
		Provider:      resolvedProvider,
		ResolvedModel: provider.ResolvedModel{BackendModelID: "resolved-model"},
		AllowedTools:  []string{"read"},
	}

	req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "t", AgentID: "a", Limits: Limits{MaxTurns: 1}})
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.ResolvedModel.BackendModelID != "resolved-model" {
		t.Errorf("req.ResolvedModel.BackendModelID = %q, want %q", req.ResolvedModel.BackendModelID, "resolved-model")
	}
	gotProvider, ok := req.Provider.(stubProvider)
	if !ok || gotProvider.name != "resolved" {
		t.Errorf("req.Provider = %#v, want resolved provider", req.Provider)
	}
}

func TestDeriveChildLimits(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.SubAgentConfig
		overrides   Limits
		wantTurns   int
		wantTokens  int
		wantTimeout time.Duration
	}{
		{
			name:        "empty config and no overrides uses defaults",
			cfg:         config.SubAgentConfig{},
			overrides:   Limits{},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "config defaults used when no overrides",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   Limits{},
			wantTurns:   20,
			wantTokens:  200000,
			wantTimeout: 0,
		},
		{
			name:        "override tightens max turns",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   Limits{MaxTurns: 5},
			wantTurns:   5,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "looser override is ignored",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   Limits{MaxTurns: 30},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "timeout override applied",
			cfg:         config.SubAgentConfig{},
			overrides:   Limits{Timeout: 30 * time.Second},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "tokens override tightens",
			cfg:         config.SubAgentConfig{MaxTokens: 100000},
			overrides:   Limits{OutputLimitTokens: 50000},
			wantTurns:   15,
			wantTokens:  50000,
			wantTimeout: 0,
		},
		{
			name:        "all fields override together",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   Limits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: time.Minute},
			wantTurns:   10,
			wantTokens:  50000,
			wantTimeout: time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveChildLimits(tt.cfg, tt.overrides)
			if got.MaxTurns != tt.wantTurns {
				t.Errorf("MaxTurns=%d, want %d", got.MaxTurns, tt.wantTurns)
			}
			if got.OutputLimitTokens != tt.wantTokens {
				t.Errorf("OutputLimitTokens=%d, want %d", got.OutputLimitTokens, tt.wantTokens)
			}
			if got.Timeout != tt.wantTimeout {
				t.Errorf("Timeout=%v, want %v", got.Timeout, tt.wantTimeout)
			}
		})
	}
}

func TestBuildChildPrompt(t *testing.T) {
	tests := []struct {
		name          string
		spec          Spec
		wantFirstRole provider.MessageRole
		wantFirstText string
		wantSystem    string
		wantLen       int
		wantImages    []provider.ImageBlock
	}{
		{
			name: "default system prompt with task only",
			spec: Spec{
				Task:    "do something",
				AgentID: "test-1",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something",
			wantSystem:    defaultChildSystemPrompt,
			wantLen:       1,
			wantImages:    nil,
		},
		{
			name: "custom system prompt",
			spec: Spec{
				Task:         "do something",
				SystemPrompt: "Custom prompt",
				AgentID:      "test-2",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something",
			wantSystem:    "Custom prompt",
			wantLen:       1,
			wantImages:    nil,
		},
		{
			name: "task with context formats correctly",
			spec: Spec{
				Task:    "do something",
				Context: "relevant info",
				AgentID: "test-3",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something\n\nAdditional context:\nrelevant info",
			wantSystem:    defaultChildSystemPrompt,
			wantLen:       1,
			wantImages:    nil,
		},
		{
			name: "task with images included in first message",
			spec: Spec{
				Task:    "analyze this image",
				AgentID: "test-4",
				Images: []provider.ImageBlock{
					{MediaType: "image/jpeg", Data: "test_data"},
				},
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "analyze this image",
			wantSystem:    defaultChildSystemPrompt,
			wantLen:       1,
			wantImages: []provider.ImageBlock{
				{MediaType: "image/jpeg", Data: "test_data"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			promptOpts := buildChildPrompt(childPromptParams{
				spec:      tt.spec,
				workDir:   "/tmp/work",
				homeDir:   "",
				caveHuman: false,
			})
			if len(promptOpts.Conversation) != tt.wantLen {
				t.Errorf("Conversation length = %d, want %d", len(promptOpts.Conversation), tt.wantLen)
			}
			if promptOpts.PromptOverrides.System != tt.wantSystem {
				t.Errorf("PromptOverrides.System = %q, want %q", promptOpts.PromptOverrides.System, tt.wantSystem)
			}
			if len(promptOpts.Conversation) > 0 {
				first := promptOpts.Conversation[0]
				if first.Role != tt.wantFirstRole {
					t.Errorf("Conversation[0].Role = %q, want %q", first.Role, tt.wantFirstRole)
				}
				if first.Content != tt.wantFirstText {
					t.Errorf("Conversation[0].Content = %q, want %q", first.Content, tt.wantFirstText)
				}
				if len(tt.wantImages) > 0 {
					if len(first.Images) != len(tt.wantImages) {
						t.Errorf("Conversation[0].Images length = %d, want %d", len(first.Images), len(tt.wantImages))
					}
					for i, img := range first.Images {
						if i < len(tt.wantImages) {
							if img.MediaType != tt.wantImages[i].MediaType {
								t.Errorf("Conversation[0].Images[%d].MediaType = %q, want %q", i, img.MediaType, tt.wantImages[i].MediaType)
							}
							if img.Data != tt.wantImages[i].Data {
								t.Errorf("Conversation[0].Images[%d].Data = %q, want %q", i, img.Data, tt.wantImages[i].Data)
							}
						}
					}
				} else if len(first.Images) != 0 {
					t.Errorf("Conversation[0].Images = %v, want empty/nil", first.Images)
				}
			}
		})
	}
}

func TestBuildChildPromptAssemblesSingleSystemMessage(t *testing.T) {
	t.Parallel()

	promptOpts := buildChildPrompt(childPromptParams{
		spec: Spec{
			Task:         "do something",
			SystemPrompt: "Custom prompt",
			AgentID:      "test-single-system",
		},
		workDir:   "/tmp/work",
		homeDir:   "",
		caveHuman: false,
	})

	assembly, err := prompt.Assemble(context.Background(), promptOpts)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}

	var systemMessages int
	for _, message := range assembly.Messages {
		if message.Role == provider.MessageRoleSystem {
			systemMessages++
			if !strings.Contains(message.Content, "Custom prompt") {
				t.Fatalf("system message missing child override: %q", message.Content)
			}
		}
	}
	if systemMessages != 1 {
		t.Fatalf("system message count = %d, want 1; messages = %#v", systemMessages, assembly.Messages)
	}
	if len(assembly.Messages) != 2 {
		t.Fatalf("message count = %d, want 2", len(assembly.Messages))
	}
	if assembly.Messages[1].Role != provider.MessageRoleUser {
		t.Fatalf("message[1].Role = %q, want user", assembly.Messages[1].Role)
	}
	if assembly.Messages[1].Content != "do something" {
		t.Fatalf("message[1].Content = %q, want task", assembly.Messages[1].Content)
	}
}

func TestBuildChildPromptUsesSharedSystemPreambleWhenOverrideEmpty(t *testing.T) {
	t.Parallel()

	promptOpts := buildChildPrompt(childPromptParams{
		spec: Spec{
			Task:    "do something",
			AgentID: "test-shared-system",
		},
		workDir:   t.TempDir(),
		homeDir:   "",
		caveHuman: false,
	})

	if promptOpts.PromptOverrides.System != defaultChildSystemPrompt {
		t.Fatalf("PromptOverrides.System = %q, want empty shared base", promptOpts.PromptOverrides.System)
	}

	assembly, err := prompt.Assemble(context.Background(), promptOpts)
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(assembly.Messages) == 0 {
		t.Fatal("assembled prompt has no messages")
	}
	if assembly.Messages[0].Role != provider.MessageRoleSystem {
		t.Fatalf("message[0].Role = %q, want system", assembly.Messages[0].Role)
	}

	assertSharedChildSystemPrompt(t, assembly.Messages[0].Content)
}

func TestBuildChildRegistries(t *testing.T) {
	t.Run("excludes delegate from both registries", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "read"},
			tool.ToolDef{Name: "write"},
			tool.ToolDef{Name: "delegate"},
			tool.ToolDef{Name: "follow_up"},
			tool.ToolDef{Name: "workflow_handoff"},
			tool.ToolDef{Name: "grep"},
		)

		visible, exec := buildChildRegistries(parent, []string{"read", "write", "grep", "follow_up", "workflow_handoff"})

		if visible == nil || exec == nil {
			t.Fatal("registries should not be nil")
		}

		visibleNames := visible.Names()
		if len(visibleNames) != 3 {
			t.Errorf("visible has %d tools, want 3: %v", len(visibleNames), visibleNames)
		}
		for _, name := range visibleNames {
			if name == "delegate" || name == "follow_up" || name == "workflow_handoff" {
				t.Errorf("visible registry should not contain delegation control tool %q", name)
			}
		}

		execNames := exec.Names()
		if len(execNames) != 3 {
			t.Errorf("exec has %d tools, want 3: %v", len(execNames), execNames)
		}
		for _, name := range execNames {
			if name == "delegate" || name == "follow_up" || name == "workflow_handoff" {
				t.Errorf("exec registry should not contain delegation control tool %q", name)
			}
		}
	})

	t.Run("exec registry contains allowed tools", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "bash"},
		)

		_, exec := buildChildRegistries(parent, []string{"bash"})

		defs := exec.Definitions()
		if len(defs) != 1 {
			t.Fatalf("expected 1 tool definition, got %d", len(defs))
		}
		if defs[0].Name != "bash" {
			t.Errorf("exec tool name = %q, want %q", defs[0].Name, "bash")
		}
	})

	t.Run("nil parent returns empty registries", func(t *testing.T) {
		visible, exec := buildChildRegistries(nil, []string{"read"})

		if len(visible.Names()) != 0 {
			t.Errorf("visible has %d tools, want 0", len(visible.Names()))
		}
		if len(exec.Names()) != 0 {
			t.Errorf("exec has %d tools, want 0", len(exec.Names()))
		}
	})
}

func TestBuildChildRegistries_AllowedTools(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read"},
		tool.ToolDef{Name: "write"},
		tool.ToolDef{Name: "grep"},
		tool.ToolDef{Name: "bash"},
		tool.ToolDef{Name: "delegate"},
	)

	tests := []struct {
		name         string
		allowedTools []string
		wantNames    []string
		wantCount    int
	}{
		{
			name:         "non-empty allow-list: only listed tools visible",
			allowedTools: []string{"read", "grep"},
			wantNames:    []string{"grep", "read"},
			wantCount:    2,
		},
		{
			name:         "delegate is no longer specially excluded",
			allowedTools: []string{"read", "delegate"},
			wantNames:    []string{"delegate", "read"},
			wantCount:    2,
		},
		{
			name:         "workflow handoff in allow-list is still excluded",
			allowedTools: []string{"read", "workflow_handoff"},
			wantNames:    []string{"read"},
			wantCount:    1,
		},
		{
			name:         "empty allow-list produces 0 tools",
			allowedTools: []string{},
			wantNames:    []string{},
			wantCount:    0,
		},
		{
			name:         "allow-list with unknown name: only valid name appears",
			allowedTools: []string{"read", "nonexistent"},
			wantNames:    []string{"read"},
			wantCount:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			visible, exec := buildChildRegistries(parent, tt.allowedTools)

			visibleNames := visible.Names()
			if len(visibleNames) != tt.wantCount {
				t.Errorf("visible has %d tools, want %d: %v", len(visibleNames), tt.wantCount, visibleNames)
			}
			execNames := exec.Names()
			if len(execNames) != tt.wantCount {
				t.Errorf("exec has %d tools, want %d: %v", len(execNames), tt.wantCount, execNames)
			}
			for i, want := range tt.wantNames {
				if i >= len(visibleNames) || visibleNames[i] != want {
					t.Errorf("visible[%d] = %q, want %q", i, func() string {
						if i < len(visibleNames) {
							return visibleNames[i]
						}
						return "<missing>"
					}(), want)
				}
			}
		})
	}
}

func TestBuildChildRegistriesContainsAllowedTools(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read"},
		tool.ToolDef{Name: "bash"},
	)

	_, exec := buildChildRegistries(parent, []string{"read", "bash"})

	defs := exec.Definitions()
	if len(defs) != 2 {
		t.Fatalf("exec has %d tools, want 2", len(defs))
	}
	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Name] = true
	}
	for _, want := range []string{"read", "bash"} {
		if !names[want] {
			t.Errorf("exec registry missing tool %q", want)
		}
	}
}

func TestBuildChildPromptDefaultSystemPrompt(t *testing.T) {
	spec := Spec{Task: "do something"}
	opts := buildChildPrompt(childPromptParams{
		spec:      spec,
		workDir:   "/tmp/work",
		homeDir:   "",
		caveHuman: false,
	})
	if opts.PromptOverrides.System != defaultChildSystemPrompt {
		t.Errorf("default system prompt = %q, want %q", opts.PromptOverrides.System, defaultChildSystemPrompt)
	}
}

func TestBuildChildPromptSkipProjectContext(t *testing.T) {
	tests := []struct {
		name               string
		skipProjectContext bool
		skipAgents         bool
		wantSkip           bool
		wantSkipAgents     bool
	}{
		{
			name:               "skip=true sets SkipProjectContext on AssemblyOptions",
			skipProjectContext: true,
			wantSkip:           true,
		},
		{
			name:               "skip=false leaves SkipProjectContext unset",
			skipProjectContext: false,
			wantSkip:           false,
		},
		{
			name:           "skipAgents=true sets SkipAgents on AssemblyOptions",
			skipAgents:     true,
			wantSkipAgents: true,
		},
		{
			name:           "skipAgents=false leaves SkipAgents unset",
			skipAgents:     false,
			wantSkipAgents: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := buildChildPrompt(childPromptParams{
				spec: Spec{
					Task:    "do something",
					AgentID: "test-skip",
				},
				workDir:            "/tmp/work",
				homeDir:            "",
				projectContextCfg:  config.ProjectContextConfig{MaxBytes: 4000},
				caveHuman:          false,
				skipProjectContext: tt.skipProjectContext,
				skipAgents:         tt.skipAgents,
			})

			if opts.SkipProjectContext != tt.wantSkip {
				t.Errorf("SkipProjectContext = %v, want %v", opts.SkipProjectContext, tt.wantSkip)
			}
			if opts.SkipAgents != tt.wantSkipAgents {
				t.Errorf("SkipAgents = %v, want %v", opts.SkipAgents, tt.wantSkipAgents)
			}
			if opts.ProjectContextBudgetBytes != 4000 {
				t.Errorf("ProjectContextBudgetBytes = %d, want 4000", opts.ProjectContextBudgetBytes)
			}
		})
	}
}

func TestBuildChildRunDoesNotInheritActiveSkills(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := Spec{
		Task:    "investigate this file",
		AgentID: "child-no-skills",
		Limits:  Limits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if len(req.Prompt.SkillNames) != 0 {
		t.Fatalf("Prompt.SkillNames = %v, want empty", req.Prompt.SkillNames)
	}
}

func TestBuildChildRunAllowedTools(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "write", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "bash", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "delegate"},
	)

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := Spec{
		Task:    "test allowed tools",
		AgentID: "test-allowed",
		Limits:  Limits{MaxTurns: 5},
	}

	override := ChildBootstrapOverrides{AgentType: AgentTypeCode, AllowedTools: []string{"read"}, Provider: deps.Provider, ResolvedModel: deps.ResolvedModel}
	req, _, err := BuildChildRun(context.Background(), deps, override, spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("visible tools = %d, want 1: %v", len(req.Tools), func() []string {
			names := make([]string, len(req.Tools))
			for i, ts := range req.Tools {
				names[i] = ts.Function.Name
			}
			return names
		}())
	}
	if req.Tools[0].Function.Name != "read" {
		t.Errorf("visible tool = %q, want %q", req.Tools[0].Function.Name, "read")
	}
}

func TestBuildChildRunResultToolSurface(t *testing.T) {
	// Verify that BuildChildRun produces correct tool registries through
	// the full bootstrap path.
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "delegate"},
		tool.ToolDef{Name: "write", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
	}

	spec := Spec{
		Task:    "test",
		AgentID: "test-bootstrap",
		Limits:  Limits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	// Visible provider specs should not include delegate
	for _, ts := range req.Tools {
		if ts.Function.Name == "delegate" {
			t.Error("visible provider specs contain delegate tool")
		}
	}

	// Visible tools should include non-delegate tools
	found := map[string]bool{}
	for _, ts := range req.Tools {
		found[ts.Function.Name] = true
	}
	if !found["read"] {
		t.Error("visible provider specs missing 'read'")
	}
	if !found["write"] {
		t.Error("visible provider specs missing 'write'")
	}
	if len(req.Tools) != 2 {
		t.Errorf("visible tools = %d, want 2", len(req.Tools))
	}

	// Executor registry behavior is verified through the integration tests
	// that exercise req.Executor.Execute directly.
}

func TestBuildChildRunUsesProvidedWorkDir(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := Spec{
		Task:    "test",
		AgentID: "test-workdir",
		Limits:  Limits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if req.Executor == nil {
		t.Fatal("BuildChildRun() produced a nil Executor - executor should be non-nil")
	}
}

func TestBuildChildRunThreadsMaxParallelTools(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", ParallelSafe: true, Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	deps := SubAgentHandlerDeps{
		ParentReg:        parent,
		SubAgentCfg:      config.SubAgentConfig{MaxFollowUps: 100},
		Events:           output.NoopSink{},
		WorkDir:          "/tmp/work",
		Provider:         stubProvider{},
		MaxParallelTools: 7,
	}

	spec := Spec{
		Task:    "test",
		AgentID: "test-parallel",
		Limits:  Limits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.ParallelClassOf == nil {
		t.Fatal("ParallelClassOf is nil, want set")
	}
	if req.MaxParallelTools != 7 {
		t.Fatalf("MaxParallelTools = %d, want 7", req.MaxParallelTools)
	}
	if got := req.ParallelClassOf("read"); got != agent.ParallelClassTool {
		t.Fatalf("ParallelClassOf(read) = %v, want ParallelClassTool", got)
	}
}

// writeExecutableScript writes a shell script to dir that prints a minimal
// valid JSON envelope to stdout and n 'x' bytes to stderr, and returns its
// path. Output truncation is exercised via stderr so stdout stays valid JSON
// regardless of the configured output byte cap.
func writeExecutableScript(t *testing.T, dir string, n int) string {
	t.Helper()
	path := filepath.Join(dir, "probe.sh")
	script := "#!/bin/sh\nprintf '{\"ok\":true}'\nprintf '%0" + strconv.Itoa(n) + "d' 0 | tr '0' 'x' 1>&2\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestBuildChildRunThreadsToolOutputMaxBytes(t *testing.T) {
	scriptPath := writeExecutableScript(t, t.TempDir(), 2000)
	parent := tool.NewRegistry(tool.ToolDef{Name: "probe", ExecPath: scriptPath})

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
		Sandbox:     tool.Unsandboxed{},
		Limits:      config.LimitsConfig{ToolOutputMaxBytes: 48},
	}
	spec := Spec{Task: "test", AgentID: "test-output-limit", Limits: Limits{MaxTurns: 5}}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	result, err := req.Executor.Execute(context.Background(), "probe", "", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if !execResult.Metadata.Stderr.Truncated {
		t.Fatal("stderr truncated = false, want true — configured tool_output_max_bytes did not reach the child executor")
	}
}

func TestBuildChildRunDefaultToolOutputMaxBytesWhenUnset(t *testing.T) {
	scriptPath := writeExecutableScript(t, t.TempDir(), 2000)
	parent := tool.NewRegistry(tool.ToolDef{Name: "probe", ExecPath: scriptPath})

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
		Sandbox:     tool.Unsandboxed{},
	}
	spec := Spec{Task: "test", AgentID: "test-output-limit-default", Limits: Limits{MaxTurns: 5}}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	result, err := req.Executor.Execute(context.Background(), "probe", "", nil)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	execResult, ok := result.(tool.ExecutionResult)
	if !ok {
		t.Fatalf("result type = %T, want tool.ExecutionResult", result)
	}
	if execResult.Metadata.Stderr.Truncated {
		t.Fatal("stderr truncated = true, want false at the 65536 default for a 2000-byte output")
	}
}

func TestBuildChildRunThreadsPathsBlockedPaths(t *testing.T) {
	parent := tool.NewRegistry(tool.ToolDef{
		Name:    "read",
		Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
	})
	workDir := t.TempDir()

	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     workDir,
		Provider:    stubProvider{},
		Paths:       config.PathsConfig{BlockedPaths: []string{workDir + "/secrets"}},
	}
	spec := Spec{Task: "test", AgentID: "test-blocked-paths", Limits: Limits{MaxTurns: 5}}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	_, err = req.Executor.Execute(context.Background(), "read", "", map[string]any{"path": workDir + "/secrets/key.txt"})
	if err == nil {
		t.Fatal("Execute() error = nil, want policy_denied — configured paths.blocked_paths did not reach the child executor")
	}
	var toolErr *tool.ToolExecutionError
	if !errors.As(err, &toolErr) {
		t.Fatalf("error type = %T, want *tool.ToolExecutionError", err)
	}
	if toolErr.Kind != "policy_denied" {
		t.Fatalf("error kind = %q, want policy_denied", toolErr.Kind)
	}
}

func TestBuildChildRunThreadsContextManagement(t *testing.T) {
	parent := tool.NewRegistry(tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }})

	deps := SubAgentHandlerDeps{
		ParentReg:         parent,
		SubAgentCfg:       config.SubAgentConfig{MaxFollowUps: 100},
		Events:            output.NoopSink{},
		WorkDir:           "/tmp/work",
		Provider:          stubProvider{},
		ContextManagement: config.ContextManagementConfig{ReadAnnotations: false},
	}
	spec := Spec{Task: "test", AgentID: "test-context-management", Limits: Limits{MaxTurns: 5}}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.ContextManager == nil {
		t.Fatal("ContextManager is nil, want a manager configured from deps.ContextManagement")
	}
}

func TestBuildChildRunIncludesModel(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:     parent,
		SubAgentCfg:   config.SubAgentConfig{MaxFollowUps: 100},
		Events:        output.NoopSink{},
		WorkDir:       "/tmp/work",
		Provider:      stubProvider{},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
	}
	spec := Spec{Task: "task", AgentID: "m1", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.ResolvedModel.BackendModelID != "test-model" {
		t.Errorf("BackendModelID=%q, want %q", req.ResolvedModel.BackendModelID, "test-model")
	}
}

func TestBuildChildRunIncludesMaxTokens(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	mt := 42000
	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
		MaxTokens:   &mt,
	}
	spec := Spec{Task: "task", AgentID: "m2", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.MaxTokens == nil {
		t.Fatal("MaxTokens is nil, want non-nil")
	}
	if *req.MaxTokens != mt {
		t.Errorf("MaxTokens=%d, want %d", *req.MaxTokens, mt)
	}
}

func TestBuildChildRunIncludesModelBudget(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
		ResolvedModel: provider.ResolvedModel{
			EffectiveLimits: provider.EffectiveLimits{
				ContextWindow:             128000,
				MaxOutputTokens:           8192,
				CompactionThreshold:       0.70,
				EstimatorPadTokens:        1280,
				NormalSummaryMaxTokens:    8192,
				EmergencySummaryMaxTokens: 5120,
			},
		},
	}
	wantBudget := prompt.ModelBudgetFromEffectiveLimits(deps.ResolvedModel.EffectiveLimits)
	spec := Spec{Task: "task", AgentID: "m3", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.ModelBudget != wantBudget {
		t.Errorf("ModelBudget=%+v, want %+v", req.ModelBudget, wantBudget)
	}
}

func TestBuildChildRunIncludesStreamingPreferred(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:          parent,
		SubAgentCfg:        config.SubAgentConfig{},
		Events:             output.NoopSink{},
		WorkDir:            "/tmp/work",
		Provider:           stubProvider{},
		StreamingPreferred: true,
	}
	spec := Spec{Task: "task", AgentID: "m4", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if !req.StreamingPreferred {
		t.Error("StreamingPreferred=false, want true")
	}
}

func TestBuildChildRunIncludesTurnTimeout(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}
	spec := Spec{Task: "task", AgentID: "m5", Limits: Limits{MaxTurns: 1, Timeout: 42 * time.Second}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if got, want := req.Limits.TurnTimeout, 42*time.Second; got != want {
		t.Errorf("TurnTimeout=%v, want %v", got, want)
	}
}

func TestBuildChildRun(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "delegate"},
		tool.ToolDef{Name: "write", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	tests := []struct {
		name string
		spec Spec
		want func(t *testing.T, req agent.RunRequest)
	}{
		{
			name: "default limits and prompt",
			spec: Spec{
				Task:    "do something",
				AgentID: "test-1",
				Limits:  Limits{},
			},
			want: func(t *testing.T, req agent.RunRequest) {
				if req.Limits.MaxTurns != 15 {
					t.Errorf("MaxTurns=%d, want 15", req.Limits.MaxTurns)
				}
				if req.Limits.MaxTokens != 100000 {
					t.Errorf("MaxTokens=%d, want 100000", req.Limits.MaxTokens)
				}
				if req.Prompt.PromptOverrides.System != "" {
					t.Errorf("PromptOverrides.System=%q, want empty shared base", req.Prompt.PromptOverrides.System)
				}
				if len(req.Prompt.Conversation) != 1 {
					t.Fatalf("Conversation length=%d, want 1", len(req.Prompt.Conversation))
				}
				assembly, err := prompt.Assemble(context.Background(), req.Prompt)
				if err != nil {
					t.Fatalf("Assemble() error = %v", err)
				}
				if len(assembly.Messages) == 0 {
					t.Fatal("assembled prompt has no messages")
				}
				if assembly.Messages[0].Role != provider.MessageRoleSystem {
					t.Fatalf("message[0].Role = %q, want system", assembly.Messages[0].Role)
				}
				assertSharedChildSystemPrompt(t, assembly.Messages[0].Content)
				if len(assembly.Messages) < 2 {
					t.Fatalf("assembled prompt message count=%d, want at least 2", len(assembly.Messages))
				}
				if req.Prompt.Conversation[0].Role != provider.MessageRoleUser {
					t.Errorf("Conversation[0].Role=%q, want %q", req.Prompt.Conversation[0].Role, provider.MessageRoleUser)
				}
				if req.Prompt.Conversation[0].Content != "do something" {
					t.Errorf("Conversation[0].Content=%q, want 'do something'", req.Prompt.Conversation[0].Content)
				}
				for _, ts := range req.Tools {
					if ts.Function.Name == "delegate" {
						t.Error("visible provider specs contain delegate tool")
					}
				}
				if len(req.Tools) != 2 {
					t.Errorf("visible tools=%d, want 2", len(req.Tools))
				}
				if req.Executor == nil {
					t.Error("Executor is nil")
				}
				if req.ResolvedModel.BackendModelID != "" {
					t.Errorf("BackendModelID=%q, want empty (no model set in deps)", req.ResolvedModel.BackendModelID)
				}
			},
		},
		{
			name: "with overrides",
			spec: Spec{
				Task:    "do something",
				AgentID: "test-2",
				Limits:  Limits{MaxTurns: 5, OutputLimitTokens: 50000},
			},
			want: func(t *testing.T, req agent.RunRequest) {
				if req.Limits.MaxTurns != 5 {
					t.Errorf("MaxTurns=%d, want 5", req.Limits.MaxTurns)
				}
				if req.Limits.MaxTokens != 50000 {
					t.Errorf("MaxTokens=%d, want 50000", req.Limits.MaxTokens)
				}
			},
		},
		{
			name: "with system prompt override",
			spec: Spec{
				Task:         "do something",
				AgentID:      "test-3",
				SystemPrompt: "custom",
				Limits:       Limits{},
			},
			want: func(t *testing.T, req agent.RunRequest) {
				if len(req.Prompt.Conversation) < 1 {
					t.Fatal("Conversation empty")
				}
				if req.Prompt.PromptOverrides.System != "custom" {
					t.Errorf("PromptOverrides.System=%q, want 'custom'", req.Prompt.PromptOverrides.System)
				}
			},
		},
		{
			name: "with context",
			spec: Spec{
				Task:    "do something",
				AgentID: "test-4",
				Context: "extra",
				Limits:  Limits{},
			},
			want: func(t *testing.T, req agent.RunRequest) {
				if len(req.Prompt.Conversation) != 1 {
					t.Fatalf("Conversation length=%d, want 1", len(req.Prompt.Conversation))
				}
				want := "do something\n\nAdditional context:\nextra"
				if req.Prompt.Conversation[0].Content != want {
					t.Errorf("Conversation[0].Content=%q, want %q", req.Prompt.Conversation[0].Content, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := SubAgentHandlerDeps{
				ParentReg:   parent,
				SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
				Events:      output.NoopSink{},
				WorkDir:     "/tmp/work",
				Provider:    stubProvider{},
			}
			req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), tt.spec)
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			tt.want(t, req)
		})
	}
}

func TestBuildChildRunRecorderPropagation(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	spec := Spec{Task: "task", AgentID: "rec-test", Limits: Limits{MaxTurns: 1}}

	t.Run("recorder set when non-nil", func(t *testing.T) {
		rec := usagestats.New(nil)
		deps := SubAgentHandlerDeps{
			ParentReg:     parent,
			SubAgentCfg:   config.SubAgentConfig{MaxFollowUps: 100},
			Events:        output.NoopSink{},
			WorkDir:       "/tmp/work",
			Provider:      stubProvider{},
			UsageRecorder: rec,
		}
		req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
		if err != nil {
			t.Fatalf("BuildChildRun() error = %v", err)
		}
		if req.UsageRecorder == nil {
			t.Error("UsageRecorder is nil, want non-nil (typed-nil guard must not wrap a non-nil pointer in a nil interface)")
		}
	})

	t.Run("recorder stays nil when not provided", func(t *testing.T) {
		deps := SubAgentHandlerDeps{
			ParentReg:     parent,
			SubAgentCfg:   config.SubAgentConfig{MaxFollowUps: 100},
			Events:        output.NoopSink{},
			WorkDir:       "/tmp/work",
			Provider:      stubProvider{},
			UsageRecorder: nil,
		}
		req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
		if err != nil {
			t.Fatalf("BuildChildRun() error = %v", err)
		}
		if req.UsageRecorder != nil {
			t.Error("UsageRecorder is non-nil, want nil (typed-nil guard must leave field unset)")
		}
	})
}

// TestBuildChildRunSandboxDisabled proves the child prompt's sandbox section
// follows the plain SandboxEnabled value on SubAgentHandlerDeps: when the parent
// sandbox is disabled (or bypassed), the child preamble renders no sandbox
// section and carries no writable mounts.
func TestBuildChildRunSandboxDisabled(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:      parent,
		SubAgentCfg:    config.SubAgentConfig{MaxFollowUps: 100},
		Events:         output.NoopSink{},
		WorkDir:        "/tmp/work",
		Provider:       stubProvider{},
		SandboxEnabled: false, // no sandbox
	}
	spec := Spec{Task: "task", AgentID: "sandbox-disabled", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if req.Prompt.SandboxEnabled {
		t.Error("Prompt.SandboxEnabled=true, want false when sandbox is disabled")
	}
	if len(req.Prompt.SandboxWritableMounts) != 0 {
		t.Errorf("Prompt.SandboxWritableMounts=%v, want empty when sandbox is disabled", req.Prompt.SandboxWritableMounts)
	}
}

// TestBuildChildRunSandboxEnabled proves the child prompt's sandbox section
// follows the plain SandboxEnabled and SandboxWritableMounts values on
// SubAgentHandlerDeps: when the parent sandbox is active, the child preamble renders
// the same sandbox section with the writable mount paths.
func TestBuildChildRunSandboxEnabled(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := SubAgentHandlerDeps{
		ParentReg:             parent,
		SubAgentCfg:           config.SubAgentConfig{MaxFollowUps: 100},
		Events:                output.NoopSink{},
		WorkDir:               "/tmp/work",
		Provider:              stubProvider{},
		SandboxEnabled:        true,
		SandboxWritableMounts: []string{"/var/log", "/srv/data"},
	}
	spec := Spec{Task: "task", AgentID: "sandbox-enabled", Limits: Limits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}
	if !req.Prompt.SandboxEnabled {
		t.Error("Prompt.SandboxEnabled=false, want true when sandbox is enabled")
	}
	wantMounts := []string{"/var/log", "/srv/data"}
	if !slices.Equal(req.Prompt.SandboxWritableMounts, wantMounts) {
		t.Errorf("Prompt.SandboxWritableMounts=%v, want %v", req.Prompt.SandboxWritableMounts, wantMounts)
	}
}

// recordingSandboxWrapper is a tool.SandboxWrapper fake that counts calls to
// WrapCommandMode and records the last readOnlyProject value it was called
// with, used to prove sandbox wrapping (and mode-aware read-only-ness)
// survives the child bootstrap chain.
type recordingSandboxWrapper struct {
	calls               atomic.Int32
	lastReadOnlyProject atomic.Bool
}

func (w *recordingSandboxWrapper) Enabled() bool { return true }

func (w *recordingSandboxWrapper) WrapCommandMode(cmd *exec.Cmd, readOnlyProject bool) *exec.Cmd {
	w.calls.Add(1)
	w.lastReadOnlyProject.Store(readOnlyProject)
	return cmd
}

// TestChildBashIsSandboxed proves the parent's sandbox wrapper survives the
// child bootstrap chain: SubAgentHandlerDeps.Sandbox -> child tool.Executor ->
// SandboxWrapperKey resolved per call -> bash handler. The sentinel wrapper
// must fire when the child runs bash. This is what #507 doubted.
func TestChildBashIsSandboxed(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	workDir := t.TempDir()
	pp := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := builtin.Env{PathPolicy: &pp}
	parent := tool.NewRegistry(builtin.NewBashTool(env))

	wrapper := &recordingSandboxWrapper{}
	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     workDir,
		Provider:    stubProvider{},
		Sandbox:     wrapper,
	}
	spec := Spec{
		Task:    "task",
		AgentID: "child-bash-wrapper",
		Limits:  Limits{MaxTurns: 1},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if _, err := req.Executor.Execute(context.Background(), "bash", "", map[string]any{"command": "echo hello"}); err != nil {
		t.Fatalf("Execute(bash) error = %v", err)
	}

	if wrapper.calls.Load() == 0 {
		t.Error("sandbox wrapper was not called: bash handler lost sandbox wrapping through the child bootstrap chain")
	}
}

// TestChildModeGetterAppliesReadOnlyProjectInPlanMode proves a non-explore
// child (which gets no readOnlyBash flag) still inherits the parent's live
// execution mode via SubAgentHandlerDeps.ModeGetter, so its own executor resolves
// readOnlyProject the same way the parent's would in plan mode.
func TestChildModeGetterAppliesReadOnlyProjectInPlanMode(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not available: %v", err)
	}

	workDir := t.TempDir()
	pp := tool.NewPathPolicy(workDir, config.PathsConfig{})
	env := builtin.Env{PathPolicy: &pp}
	parent := tool.NewRegistry(builtin.NewBashTool(env))

	wrapper := &recordingSandboxWrapper{}
	deps := SubAgentHandlerDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{MaxFollowUps: 100},
		Events:      output.NoopSink{},
		WorkDir:     workDir,
		Provider:    stubProvider{},
		Sandbox:     wrapper,
		ModeGetter:  func() config.ExecutionMode { return config.ExecutionModePlan },
	}
	spec := Spec{
		Task:    "task",
		AgentID: "child-plan-mode",
		Limits:  Limits{MaxTurns: 1},
	}

	req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if _, err := req.Executor.Execute(context.Background(), "bash", "", map[string]any{"command": "echo hello"}); err != nil {
		t.Fatalf("Execute(bash) error = %v", err)
	}

	if !wrapper.lastReadOnlyProject.Load() {
		t.Error("child bash readOnlyProject = false, want true when the parent is in plan mode, even for a non-explore child")
	}
}

func TestChildExploreBashContextIsReadOnly(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		want      bool
	}{
		{name: "explore with sandbox", agentType: AgentTypeExplore, want: true},
		{name: "code with sandbox", agentType: AgentTypeCode, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bool
			parent := tool.NewRegistry(tool.ToolDef{
				Name: "probe",
				Handler: func(ctx context.Context, _ map[string]any) (any, error) {
					got = ctx.Value(tool.BashReadOnlyProjectKey{}) == true
					return nil, nil
				},
			})
			deps := SubAgentHandlerDeps{
				ParentReg:      parent,
				SubAgentCfg:    config.SubAgentConfig{MaxFollowUps: 100},
				Events:         output.NoopSink{},
				WorkDir:        "/tmp/work",
				Provider:       stubProvider{},
				SandboxEnabled: true,
			}
			override := testChildOverride(deps)
			override.AgentType = tt.agentType
			req, _, err := BuildChildRun(context.Background(), deps, override, Spec{Task: "task", AgentID: tt.name, Limits: Limits{MaxTurns: 1}})
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			if _, err := req.Executor.Execute(context.Background(), "probe", "", nil); err != nil {
				t.Fatalf("Execute(probe) error = %v", err)
			}
			if got != tt.want {
				t.Errorf("BashReadOnlyProjectKey = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildChildRunRequestPromptCacheKeyFallsBackWhenStoreNil(t *testing.T) {
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:    "/tmp/work",
		AgentID:    "cache-key-nil-store",
		VisibleReg: tool.NewRegistry(),
		ExecReg:    tool.NewRegistry(),
	})
	if req.PromptCacheKey == "" {
		t.Fatal("PromptCacheKey is empty, want a freshly minted key when CacheKeyStore is nil")
	}
}

func TestBuildChildRunRequestEnablesParallelSafeToolsOnly(t *testing.T) {
	execReg := tool.NewRegistry(
		tool.ToolDef{Name: "read", ParallelSafe: true},
		tool.ToolDef{Name: "bash"},
	)
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:          "/tmp/work",
		AgentID:          "no-nesting",
		VisibleReg:       tool.NewRegistry(),
		ExecReg:          execReg,
		MaxParallelTools: 3,
	})
	if req.ParallelClassOf == nil {
		t.Fatal("child ParallelClassOf is nil, want set")
	}
	if got := req.ParallelClassOf("read"); got != agent.ParallelClassTool {
		t.Fatalf("child ParallelClassOf(read) = %v, want ParallelClassTool", got)
	}
	if got := req.ParallelClassOf("bash"); got == agent.ParallelClassTool {
		t.Fatal("child ParallelClassOf(bash) = ParallelClassTool, want ParallelClassNone")
	}
	if req.MaxParallelTools != 3 {
		t.Fatalf("child MaxParallelTools = %d, want 3", req.MaxParallelTools)
	}
}

func TestBuildChildRunRequestNeverEnablesDelegationTools(t *testing.T) {
	// Children never have delegation tools in their exec registry (they
	// can't nest), so the child ParallelClassOf classifier must only consult the
	// registry, never delegation.IsDelegationTool.
	req := buildChildRunRequest(childRunRequestParams{
		WorkDir:    "/tmp/work",
		AgentID:    "no-nesting",
		VisibleReg: tool.NewRegistry(),
		ExecReg:    tool.NewRegistry(),
	})
	if got := req.ParallelClassOf("code"); got == agent.ParallelClassDelegation {
		t.Fatal("child ParallelClassOf(code) = ParallelClassDelegation, want ParallelClassNone: children cannot nest delegation")
	}
}

func TestBuildChildRunRequestPromptCacheKeyReusePerAgentType(t *testing.T) {
	store := NewCacheKeyStore()

	first := buildChildRunRequest(childRunRequestParams{
		WorkDir:       "/tmp/work",
		AgentID:       "cache-key-1",
		VisibleReg:    tool.NewRegistry(),
		ExecReg:       tool.NewRegistry(),
		AgentType:     AgentTypeCode,
		CacheKeyStore: store,
	})
	second := buildChildRunRequest(childRunRequestParams{
		WorkDir:       "/tmp/work",
		AgentID:       "cache-key-2",
		VisibleReg:    tool.NewRegistry(),
		ExecReg:       tool.NewRegistry(),
		AgentType:     AgentTypeCode,
		CacheKeyStore: store,
	})
	if first.PromptCacheKey == "" {
		t.Fatal("PromptCacheKey is empty, want a minted key")
	}
	if first.PromptCacheKey != second.PromptCacheKey {
		t.Errorf("PromptCacheKey differs across same-AgentType calls: %q vs %q", first.PromptCacheKey, second.PromptCacheKey)
	}

	third := buildChildRunRequest(childRunRequestParams{
		WorkDir:       "/tmp/work",
		AgentID:       "cache-key-3",
		VisibleReg:    tool.NewRegistry(),
		ExecReg:       tool.NewRegistry(),
		AgentType:     AgentTypeReview,
		CacheKeyStore: store,
	})
	if third.PromptCacheKey == first.PromptCacheKey {
		t.Errorf("PromptCacheKey for a different AgentType matched: %q", third.PromptCacheKey)
	}
}

func TestMCPChildRegistryRetainsHandlersAndProvenance(t *testing.T) {
	handler := func(_ context.Context, _ map[string]any) (any, error) { return "ok", nil }
	prov := tool.MCPProvenance{Server: "notes", ToolName: "search"}
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Description: "read"},
		tool.ToolDef{Name: "mcp__notes__search", Description: "search notes", Handler: handler, MCP: prov},
	)

	visible, exec := buildChildRegistries(parent, []string{"read", "mcp__notes__search"})

	if !slices.Contains(visible.Names(), "mcp__notes__search") {
		t.Fatalf("visible registry missing MCP tool: %v", visible.Names())
	}
	got, ok := exec.Get("mcp__notes__search")
	if !ok {
		t.Fatal("exec registry missing MCP tool")
	}
	if got.Handler == nil {
		t.Error("MCP ToolDef lost its handler in child registry")
	}
	if reflect.ValueOf(got.Handler).Pointer() != reflect.ValueOf(handler).Pointer() {
		t.Error("child MCP ToolDef handler differs from parent handler")
	}
	if got.MCP != prov {
		t.Errorf("child MCP ToolDef provenance = %+v, want %+v", got.MCP, prov)
	}
}

// TestBuildChildRunSandboxTmpDir proves the child executor inherits the
// sandbox tmp dir so mutate can rewrite /tmp paths into it. With an empty
// SandboxTmpDir the same mutation is denied because /tmp is outside the
// project root and the child has no approver.
func TestBuildChildRunSandboxTmpDir(t *testing.T) {
	workDir := t.TempDir()
	sandboxTmpDir := filepath.Join(workDir, "sandbox-tmp")
	if err := os.MkdirAll(sandboxTmpDir, 0o755); err != nil {
		t.Fatalf("mkdir sandbox tmp dir: %v", err)
	}

	pp := tool.NewPathPolicy(workDir, config.PathsConfig{})
	parent := tool.NewRegistry(builtin.NewMutateTool(builtin.Env{WorkDir: workDir, PathPolicy: &pp}))

	spec := Spec{
		Task:    "task",
		AgentID: "child-sandbox-tmp",
		Limits:  Limits{MaxTurns: 1},
	}

	tests := []struct {
		name          string
		sandboxTmpDir string
		wantLanded    bool
	}{
		{name: "sandbox tmpdir set rewrites /tmp into it", sandboxTmpDir: sandboxTmpDir, wantLanded: true},
		{name: "empty sandbox tmpdir denies /tmp", sandboxTmpDir: "", wantLanded: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := SubAgentHandlerDeps{
				ParentReg:     parent,
				SubAgentCfg:   config.SubAgentConfig{MaxFollowUps: 100},
				Events:        output.NoopSink{},
				WorkDir:       workDir,
				Provider:      stubProvider{},
				SandboxTmpDir: tt.sandboxTmpDir,
			}
			req, _, err := BuildChildRun(context.Background(), deps, testChildOverride(deps), spec)
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}

			_, err = req.Executor.Execute(context.Background(), "mutate", "", map[string]any{
				"operations": []any{
					map[string]any{"type": "create", "path": "/tmp/test-file", "content": "hello\n"},
				},
			})
			if !tt.wantLanded {
				var execErr *tool.ToolExecutionError
				if !errors.As(err, &execErr) {
					t.Fatalf("Execute(mutate /tmp) error = %v, want *tool.ToolExecutionError policy_denied", err)
				}
				if execErr.Kind != "policy_denied" {
					t.Fatalf("Execute(mutate /tmp) error kind = %q, want %q", execErr.Kind, "policy_denied")
				}
				return
			}
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
		})
	}
}
