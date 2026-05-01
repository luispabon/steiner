package delegation

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
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
		name           string
		spec           DelegationSpec
		wantFirstRole  provider.MessageRole
		wantFirstText  string
		wantSecondText string
		wantLen        int
	}{
		{
			name: "default system prompt with task only",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-1",
			},
			wantFirstRole:  provider.MessageRoleSystem,
			wantFirstText:  "You are a sub-agent. Complete the task given to you.",
			wantSecondText: "do something",
			wantLen:        2,
		},
		{
			name: "custom system prompt",
			spec: DelegationSpec{
				Task:         "do something",
				SystemPrompt: "Custom prompt",
				AgentID:      "test-2",
			},
			wantFirstRole:  provider.MessageRoleSystem,
			wantFirstText:  "Custom prompt",
			wantSecondText: "do something",
			wantLen:        2,
		},
		{
			name: "task with context formats correctly",
			spec: DelegationSpec{
				Task:    "do something",
				Context: "relevant info",
				AgentID: "test-3",
			},
			wantFirstRole:  provider.MessageRoleSystem,
			wantFirstText:  "You are a sub-agent. Complete the task given to you.",
			wantSecondText: "do something\n\nAdditional context:\nrelevant info",
			wantLen:        2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			promptOpts, err := buildChildPrompt(tt.spec)
			if err != nil {
				t.Fatalf("buildChildPrompt() error = %v", err)
			}
			if len(promptOpts.Conversation) != tt.wantLen {
				t.Errorf("Conversation length = %d, want %d", len(promptOpts.Conversation), tt.wantLen)
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
			if tt.wantSecondText != "" && len(promptOpts.Conversation) > 1 {
				second := promptOpts.Conversation[1]
				if second.Content != tt.wantSecondText {
					t.Errorf("Conversation[1].Content = %q, want %q", second.Content, tt.wantSecondText)
				}
			}
		})
	}
}

func TestBuildChildRegistries(t *testing.T) {
	t.Run("excludes delegate from both registries", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "read"},
			tool.ToolDef{Name: "write"},
			tool.ToolDef{Name: "delegate"},
			tool.ToolDef{Name: "grep"},
		)

		visible, exec := buildChildRegistries(parent, "delegate")

		if visible == nil || exec == nil {
			t.Fatal("registries should not be nil")
		}

		visibleNames := visible.Names()
		if len(visibleNames) != 3 {
			t.Errorf("visible has %d tools, want 3: %v", len(visibleNames), visibleNames)
		}
		for _, name := range visibleNames {
			if name == "delegate" {
				t.Error("visible registry should not contain delegate tool")
			}
		}

		execNames := exec.Names()
		if len(execNames) != 3 {
			t.Errorf("exec has %d tools, want 3: %v", len(execNames), execNames)
		}
		for _, name := range execNames {
			if name == "delegate" {
				t.Error("exec registry should not contain delegate tool")
			}
		}
	})

	t.Run("exec registry has auto approval", func(t *testing.T) {
		parent := tool.NewRegistry(
			tool.ToolDef{Name: "bash", Approval: config.ApprovalModePrompt},
		)

		_, exec := buildChildRegistries(parent, "delegate")

		defs := exec.Definitions()
		if len(defs) != 1 {
			t.Fatalf("expected 1 tool definition, got %d", len(defs))
		}
		if defs[0].Approval != config.ApprovalModeAuto {
			t.Errorf("exec approval = %v, want %v", defs[0].Approval, config.ApprovalModeAuto)
		}
	})

	t.Run("nil parent returns empty registries", func(t *testing.T) {
		visible, exec := buildChildRegistries(nil, "delegate")

		if len(visible.Names()) != 0 {
			t.Errorf("visible has %d tools, want 0", len(visible.Names()))
		}
		if len(exec.Names()) != 0 {
			t.Errorf("exec has %d tools, want 0", len(exec.Names()))
		}
	})
}

func TestBuildChildRunResultToolSurface(t *testing.T) {
	// Verify that BuildChildRun produces correct tool registries through
	// the full bootstrap path.
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "delegate"},
		tool.ToolDef{Name: "write", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }},
	)

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{},
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
		tool.ToolDef{Name: "read", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }},
	)

	deps := BootstrapDeps{
		ParentReg:   parent,
		SubAgentCfg: config.SubAgentConfig{},
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

func TestBuildChildRun(t *testing.T) {
	parent := tool.NewRegistry(
		tool.ToolDef{Name: "read", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }},
		tool.ToolDef{Name: "delegate"},
		tool.ToolDef{Name: "write", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }},
	)

	tests := []struct {
		name string
		spec DelegationSpec
		want func(t *testing.T, req agent.RunRequest, limits DelegationLimits)
	}{
		{
			name: "default limits and prompt",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-1",
				Limits:  DelegationLimits{},
			},
			want: func(t *testing.T, req agent.RunRequest, limits DelegationLimits) {
				if req.Limits.MaxTurns != 15 {
					t.Errorf("MaxTurns=%d, want 15", req.Limits.MaxTurns)
				}
				if req.Limits.MaxTokens != 100000 {
					t.Errorf("MaxTokens=%d, want 100000", req.Limits.MaxTokens)
				}
				if len(req.Prompt.Conversation) != 2 {
					t.Fatalf("Conversation length=%d, want 2", len(req.Prompt.Conversation))
				}
				if req.Prompt.Conversation[0].Role != provider.MessageRoleSystem {
					t.Errorf("Conversation[0].Role=%q, want %q", req.Prompt.Conversation[0].Role, provider.MessageRoleSystem)
				}
				if req.Prompt.Conversation[0].Content != "You are a sub-agent. Complete the task given to you." {
					t.Errorf("Conversation[0].Content=%q, want default system prompt", req.Prompt.Conversation[0].Content)
				}
				if req.Prompt.Conversation[1].Content != "do something" {
					t.Errorf("Conversation[1].Content=%q, want 'do something'", req.Prompt.Conversation[1].Content)
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
				if req.Model != "" {
					t.Errorf("Model=%q, want empty", req.Model)
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
			want: func(t *testing.T, req agent.RunRequest, limits DelegationLimits) {
				if req.Limits.MaxTurns != 5 {
					t.Errorf("MaxTurns=%d, want 5", req.Limits.MaxTurns)
				}
				if req.Limits.MaxTokens != 50000 {
					t.Errorf("MaxTokens=%d, want 50000", req.Limits.MaxTokens)
				}
			},
		},
		{
			name: "with model override",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-3",
				Model:   "gpt-4",
				Limits:  DelegationLimits{},
			},
			want: func(t *testing.T, req agent.RunRequest, limits DelegationLimits) {
				if req.Model != "gpt-4" {
					t.Errorf("Model=%q, want 'gpt-4'", req.Model)
				}
			},
		},
		{
			name: "with system prompt override",
			spec: DelegationSpec{
				Task:         "do something",
				AgentID:      "test-4",
				SystemPrompt: "custom",
				Limits:       DelegationLimits{},
			},
			want: func(t *testing.T, req agent.RunRequest, limits DelegationLimits) {
				if len(req.Prompt.Conversation) < 1 {
					t.Fatal("Conversation empty")
				}
				if req.Prompt.Conversation[0].Role != provider.MessageRoleSystem {
					t.Errorf("Conversation[0].Role=%q, want %q", req.Prompt.Conversation[0].Role, provider.MessageRoleSystem)
				}
				if req.Prompt.Conversation[0].Content != "custom" {
					t.Errorf("Conversation[0].Content=%q, want 'custom'", req.Prompt.Conversation[0].Content)
				}
			},
		},
		{
			name: "with context",
			spec: DelegationSpec{
				Task:    "do something",
				AgentID: "test-5",
				Context: "extra",
				Limits:  DelegationLimits{},
			},
			want: func(t *testing.T, req agent.RunRequest, limits DelegationLimits) {
				if len(req.Prompt.Conversation) < 2 {
					t.Fatal("Conversation length < 2")
				}
				want := "do something\n\nAdditional context:\nextra"
				if req.Prompt.Conversation[1].Content != want {
					t.Errorf("Conversation[1].Content=%q, want %q", req.Prompt.Conversation[1].Content, want)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := BootstrapDeps{
				ParentReg:   parent,
				SubAgentCfg: config.SubAgentConfig{},
				Events:      output.NoopSink{},
				WorkDir:     "/tmp/work",
				Provider:    stubProvider{},
			}
			req, _, err := BuildChildRun(context.Background(), deps, tt.spec)
			if err != nil {
				t.Fatalf("BuildChildRun() error = %v", err)
			}
			tt.want(t, req, DelegationLimits{})
		})
	}
}
