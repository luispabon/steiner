package delegation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

func TestDeriveChildLimits(t *testing.T) {
	tests := []struct {
		name        string
		cfg         config.SubAgentConfig
		overrides   DelegationLimits
		wantTurns   int
		wantTokens  int
		wantTimeout time.Duration
	}{
		{
			name:        "empty config and no overrides uses defaults",
			cfg:         config.SubAgentConfig{},
			overrides:   DelegationLimits{},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "config defaults used when no overrides",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   DelegationLimits{},
			wantTurns:   20,
			wantTokens:  200000,
			wantTimeout: 0,
		},
		{
			name:        "override tightens max turns",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   DelegationLimits{MaxTurns: 5},
			wantTurns:   5,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "looser override is ignored",
			cfg:         config.SubAgentConfig{MaxTurns: 15},
			overrides:   DelegationLimits{MaxTurns: 30},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 0,
		},
		{
			name:        "timeout override applied",
			cfg:         config.SubAgentConfig{},
			overrides:   DelegationLimits{Timeout: 30 * time.Second},
			wantTurns:   15,
			wantTokens:  100000,
			wantTimeout: 30 * time.Second,
		},
		{
			name:        "tokens override tightens",
			cfg:         config.SubAgentConfig{MaxTokens: 100000},
			overrides:   DelegationLimits{OutputLimitTokens: 50000},
			wantTurns:   15,
			wantTokens:  50000,
			wantTimeout: 0,
		},
		{
			name:        "all fields override together",
			cfg:         config.SubAgentConfig{MaxTurns: 20, MaxTokens: 200000},
			overrides:   DelegationLimits{MaxTurns: 10, OutputLimitTokens: 50000, Timeout: time.Minute},
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
		spec          DelegationSpec
		wantFirstRole provider.MessageRole
		wantFirstText string
		wantSystem    string
		wantLen       int
	}{
		{
			name: "default system prompt with task only",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-1",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something",
			wantSystem:    "You are a sub-agent. Complete the task given to you.\n\nUse the scratchpad tool to record your findings as you go. Update it after each significant discovery — do not wait until the end to synthesize. Your work may be interrupted at any time; only findings recorded in scratchpad are guaranteed to survive.",
			wantLen:       1,
		},
		{
			name: "custom system prompt",
			spec: DelegationSpec{
				Task:         "do something",
				SystemPrompt: "Custom prompt",
				AgentID:      "test-2",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something",
			wantSystem:    "Custom prompt",
			wantLen:       1,
		},
		{
			name: "task with context formats correctly",
			spec: DelegationSpec{
				Task:    "do something",
				Context: "relevant info",
				AgentID: "test-3",
			},
			wantFirstRole: provider.MessageRoleUser,
			wantFirstText: "do something\n\nAdditional context:\nrelevant info",
			wantSystem:    "You are a sub-agent. Complete the task given to you.\n\nUse the scratchpad tool to record your findings as you go. Update it after each significant discovery — do not wait until the end to synthesize. Your work may be interrupted at any time; only findings recorded in scratchpad are guaranteed to survive.",
			wantLen:       1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			promptOpts := buildChildPrompt(tt.spec, "/tmp/work", "", config.ProjectContextConfig{}, false)
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
			}
		})
	}
}

func TestBuildChildPromptAssemblesSingleSystemMessage(t *testing.T) {
	t.Parallel()

	promptOpts := buildChildPrompt(DelegationSpec{
		Task:         "do something",
		SystemPrompt: "Custom prompt",
		AgentID:      "test-single-system",
	}, "/tmp/work", "", config.ProjectContextConfig{}, false)

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

func TestBuildChildRegistries(t *testing.T) {
	t.Run("excludes delegate from both registries", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "read"},
			tool.ToolDef{Name: "write"},
			tool.ToolDef{Name: "delegate"},
			tool.ToolDef{Name: "follow_up"},
			tool.ToolDef{Name: "grep"},
		)

		visible, exec := buildChildRegistries(parent, []string{"read", "write", "grep", "follow_up"})

		if visible == nil || exec == nil {
			t.Fatal("registries should not be nil")
		}

		visibleNames := visible.Names()
		if len(visibleNames) != 3 {
			t.Errorf("visible has %d tools, want 3: %v", len(visibleNames), visibleNames)
		}
		for _, name := range visibleNames {
			if name == "delegate" || name == "follow_up" {
				t.Errorf("visible registry should not contain delegation control tool %q", name)
			}
		}

		execNames := exec.Names()
		if len(execNames) != 3 {
			t.Errorf("exec has %d tools, want 3: %v", len(execNames), execNames)
		}
		for _, name := range execNames {
			if name == "delegate" || name == "follow_up" {
				t.Errorf("exec registry should not contain delegation control tool %q", name)
			}
		}
	})

	t.Run("exec registry has auto approval", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "bash", Approval: config.ApprovalModePrompt},
		)

		_, exec := buildChildRegistries(parent, []string{"bash"})

		defs := exec.Definitions()
		if len(defs) != 1 {
			t.Fatalf("expected 1 tool definition, got %d", len(defs))
		}
		if defs[0].Approval != config.ApprovalModeAuto {
			t.Errorf("exec approval = %v, want %v", defs[0].Approval, config.ApprovalModeAuto)
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
			name:         "delegate in allow-list is still excluded",
			allowedTools: []string{"read", "delegate"},
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
			for _, name := range visibleNames {
				if name == "delegate" {
					t.Error("visible registry must not contain delegate")
				}
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

func TestBuildChildRegistriesAutoApproval(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Approval: config.ApprovalModePrompt},
		tool.ToolDef{Name: "bash", Approval: config.ApprovalModePrompt},
	)

	_, exec := buildChildRegistries(parent, []string{"read", "bash"})

	defs := exec.Definitions()
	if len(defs) != 2 {
		t.Fatalf("exec has %d tools, want 2", len(defs))
	}
	for _, def := range defs {
		if def.Approval != config.ApprovalModeAuto {
			t.Errorf("tool %q exec approval = %v, want %v", def.Name, def.Approval, config.ApprovalModeAuto)
		}
	}
}

func TestBuildChildPromptDefaultSystemPromptMentionsScratchpad(t *testing.T) {
	spec := DelegationSpec{Task: "do something"}
	opts := buildChildPrompt(spec, "/tmp/work", "", config.ProjectContextConfig{}, false)
	if !strings.Contains(opts.PromptOverrides.System, "scratchpad") {
		t.Errorf("default system prompt does not mention scratchpad: %q", opts.PromptOverrides.System)
	}
}

func TestBuildChildRunDoesNotInheritActiveSkills(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := DelegationSpec{
		Task:    "investigate this file",
		AgentID: "child-no-skills",
		Limits:  DelegationLimits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, spec)
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

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := DelegationSpec{
		Task:    "test allowed tools",
		AgentID: "test-allowed",
		Limits:  DelegationLimits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, spec)
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

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read", "write"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
	}

	spec := DelegationSpec{
		Task:    "test",
		AgentID: "test-bootstrap",
		Limits:  DelegationLimits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, spec)
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

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}

	spec := DelegationSpec{
		Task:    "test",
		AgentID: "test-workdir",
		Limits:  DelegationLimits{MaxTurns: 5},
	}

	req, _, err := BuildChildRun(context.Background(), deps, spec)
	if err != nil {
		t.Fatalf("BuildChildRun() error = %v", err)
	}

	if req.Executor == nil {
		t.Fatal("BuildChildRun() produced a nil Executor - executor should be non-nil")
	}
}

func TestBuildChildRunIncludesModel(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(_ context.Context, _ map[string]any) (any, error) { return nil, nil }},
	)
	deps := BootstrapDeps{
		ParentReg:     parent,
		SubAgentCfg:   config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:        output.NoopSink{},
		WorkDir:       "/tmp/work",
		Provider:      stubProvider{},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
	}
	spec := DelegationSpec{Task: "task", AgentID: "m1", Limits: DelegationLimits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, spec)
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
	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
		MaxTokens:   &mt,
	}
	spec := DelegationSpec{Task: "task", AgentID: "m2", Limits: DelegationLimits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, spec)
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
	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
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
	spec := DelegationSpec{Task: "task", AgentID: "m3", Limits: DelegationLimits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, spec)
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
	deps := BootstrapDeps{
		ParentReg:          parent,
		SubAgentCfg:        config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:             output.NoopSink{},
		WorkDir:            "/tmp/work",
		Provider:           stubProvider{},
		StreamingPreferred: true,
	}
	spec := DelegationSpec{Task: "task", AgentID: "m4", Limits: DelegationLimits{MaxTurns: 1}}
	req, _, err := BuildChildRun(context.Background(), deps, spec)
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
	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read"}},
		Events:      output.NoopSink{},
		WorkDir:     "/tmp/work",
		Provider:    stubProvider{},
	}
	spec := DelegationSpec{Task: "task", AgentID: "m5", Limits: DelegationLimits{MaxTurns: 1, Timeout: 42 * time.Second}}
	req, _, err := BuildChildRun(context.Background(), deps, spec)
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
		spec DelegationSpec
		want func(t *testing.T, req agent.RunRequest)
	}{
		{
			name: "default limits and prompt",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-1",
				Limits:  DelegationLimits{},
			},
			want: func(t *testing.T, req agent.RunRequest) {
				if req.Limits.MaxTurns != 15 {
					t.Errorf("MaxTurns=%d, want 15", req.Limits.MaxTurns)
				}
				if req.Limits.MaxTokens != 100000 {
					t.Errorf("MaxTokens=%d, want 100000", req.Limits.MaxTokens)
				}
				if len(req.Prompt.Conversation) != 1 {
					t.Fatalf("Conversation length=%d, want 1", len(req.Prompt.Conversation))
				}
				if !strings.HasPrefix(req.Prompt.PromptOverrides.System, "You are a sub-agent. Complete the task given to you.") {
					t.Errorf("PromptOverrides.System=%q, want to start with default system prompt", req.Prompt.PromptOverrides.System)
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
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-2",
				Limits:  DelegationLimits{MaxTurns: 5, OutputLimitTokens: 50000},
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
			spec: DelegationSpec{
				Task:         "do something",
				AgentID:      "test-3",
				SystemPrompt: "custom",
				Limits:       DelegationLimits{},
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
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-4",
				Context: "extra",
				Limits:  DelegationLimits{},
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
			deps := BootstrapDeps{
				ParentReg:   parent,
				SubAgentCfg: config.SubAgentConfig{AllowedTools: []string{"read", "write"}},
				Events:      output.NoopSink{},
				WorkDir:     "/tmp/work",
				Provider:    stubProvider{},
			}
			req, _, err := BuildChildRun(context.Background(), deps, tt.spec)
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			tt.want(t, req)
		})
	}
}
