package delegation

import (
	"slices"
	"strings"
	"testing"
)

func TestValidAgentType(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "explore is valid", input: "explore", want: true},
		{name: "research is valid", input: "research", want: true},
		{name: "code is valid", input: "code", want: true},
		{name: "evaluate is valid", input: "evaluate", want: true},
		{name: "sanity_check is valid", input: "sanity_check", want: true},
		{name: "review is valid", input: "review", want: true},
		{name: "vision is valid", input: "vision", want: true},
		{name: "empty string is invalid", input: "", want: false},
		{name: "unknown type is invalid", input: "debug", want: false},
		{name: "uppercase is invalid", input: "Explore", want: false},
		{name: "partial match is invalid", input: "explor", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidAgentType(tt.input)
			if got != tt.want {
				t.Fatalf("ValidAgentType(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestAllAgentTypes(t *testing.T) {
	types := AllAgentTypes()
	if len(types) != 7 {
		t.Fatalf("AllAgentTypes() returned %d types, want 7", len(types))
	}
	for i, at := range types {
		if at == "" {
			t.Fatalf("AllAgentTypes()[%d] is empty", i)
		}
	}
}

func TestAllSpecializedDelegateTools(t *testing.T) {
	tools := AllSpecializedDelegateTools()
	want := []string{"explore", "research", "code", "evaluate", "sanity_check", "review", "vision", "follow_up"}

	if !slices.Equal(tools, want) {
		t.Fatalf("AllSpecializedDelegateTools() = %v, want %v", tools, want)
	}

	tools[0] = "mutated"
	if got := AllSpecializedDelegateTools(); !slices.Equal(got, want) {
		t.Fatalf("AllSpecializedDelegateTools() returned shared backing storage: %v", got)
	}
}

func TestIsDelegationTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: FollowUpToolName, want: true},
		{name: "read", want: false},
		{name: "mutate", want: false},
		{name: "bash", want: false},
		{name: "glob", want: false},
		{name: "grep", want: false},
		{name: "ls", want: false},
		{name: "fetch_url", want: false},
		{name: "display_file", want: false},
		{name: "advisor", want: false},
		{name: "workflow_handoff", want: false},
		{name: "mcp__server__tool", want: false},
	}
	for _, agentType := range AllAgentTypes() {
		tests = append(tests, struct {
			name string
			want bool
		}{name: string(agentType), want: true})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDelegationTool(tt.name); got != tt.want {
				t.Fatalf("IsDelegationTool(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestIsDelegationToolCoversAllAgentTypes(t *testing.T) {
	for _, agentType := range AllAgentTypes() {
		if !IsDelegationTool(string(agentType)) {
			t.Errorf("IsDelegationTool(%q) = false, want true", agentType)
		}
	}
}

func TestAgentSystemPrompt(t *testing.T) {
	for _, at := range AllAgentTypes() {
		t.Run(string(at), func(t *testing.T) {
			p := AgentSystemPrompt(at)
			switch at {
			case AgentTypeCode:
				if p != "" {
					t.Fatalf("AgentSystemPrompt(%q) = %q, want empty shared-base prompt", at, p)
				}
			default:
				if p == "" {
					t.Fatalf("AgentSystemPrompt(%q) returned empty string", at)
				}
			}
		})
	}

	t.Run("unknown type returns empty", func(t *testing.T) {
		if got := AgentSystemPrompt("unknown"); got != "" {
			t.Fatalf("AgentSystemPrompt(unknown) = %q, want empty", got)
		}
	})
}

func TestAgentSystemSuffix(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		contains  []string
		wantEmpty bool
	}{
		{
			name:      "code agent suffix",
			agentType: AgentTypeCode,
			contains: []string{
				"git status --porcelain",
				"Do not merge, rebase, or push",
				"Do not discard, reset, restore, stash, switch branches",
			},
		},
		{name: "explore agent has no suffix", agentType: AgentTypeExplore, wantEmpty: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentSystemSuffix(tt.agentType)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("AgentSystemSuffix(%q) = %q, want empty", tt.agentType, got)
				}
				return
			}
			if got == "" {
				t.Fatal("AgentSystemSuffix(code) returned empty string")
			}
			for _, phrase := range tt.contains {
				if !strings.Contains(got, phrase) {
					t.Errorf("AgentSystemSuffix(code) does not contain %q", phrase)
				}
			}
		})
	}
}

func TestAgentAllowedTools(t *testing.T) {
	legacyMutationTools := []string{"write", "edit", "apply_patch"}

	for _, at := range AllAgentTypes() {
		t.Run(string(at), func(t *testing.T) {
			tools := AgentAllowedTools(at)
			if len(tools) == 0 {
				t.Fatalf("AgentAllowedTools(%q) returned empty slice", at)
			}
		})
	}

	t.Run("explore has bash but no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeExplore)
		if !slices.Contains(tools, "bash") {
			t.Fatal("AgentAllowedTools(explore) missing bash")
		}
		for _, m := range append(legacyMutationTools, "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(explore) should not contain %q", m)
			}
		}
	})

	t.Run("evaluate has no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeEvaluate)
		for _, m := range append(legacyMutationTools, "bash", "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(evaluate) should not contain %q", m)
			}
		}
	})

	t.Run("sanity_check has bash but not mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeSanityCheck)
		if !slices.Contains(tools, "bash") {
			t.Fatal("AgentAllowedTools(sanity_check) missing bash")
		}
		for _, m := range append(legacyMutationTools, "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(sanity_check) should not contain %q", m)
			}
		}
	})

	t.Run("review has bash but not mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeReview)
		if !slices.Contains(tools, "bash") {
			t.Fatal("AgentAllowedTools(review) missing bash")
		}
		for _, m := range append(legacyMutationTools, "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(review) should not contain %q", m)
			}
		}
	})

	t.Run("research has web_search and fetch_url but not mutation tools or bash", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeResearch)
		if !slices.Contains(tools, "web_search") {
			t.Fatal("AgentAllowedTools(research) missing web_search")
		}
		if !slices.Contains(tools, "fetch_url") {
			t.Fatal("AgentAllowedTools(research) missing fetch_url")
		}
		for _, m := range append(legacyMutationTools, "bash", "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(research) should not contain %q", m)
			}
		}
	})

	t.Run("code has mutate and bash", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeCode)
		for _, m := range []string{"mutate", "bash"} {
			if !slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(code) missing %q", m)
			}
		}
		for _, m := range legacyMutationTools {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(code) should not contain legacy mutation tool %q", m)
			}
		}
	})

	t.Run("unknown type returns nil", func(t *testing.T) {
		if got := AgentAllowedTools("unknown"); got != nil {
			t.Fatalf("AgentAllowedTools(unknown) = %v, want nil", got)
		}
	})

	t.Run("returns defensive copy", func(t *testing.T) {
		first := AgentAllowedTools(AgentTypeExplore)
		first[0] = "mutated"
		second := AgentAllowedTools(AgentTypeExplore)
		if second[0] == "mutated" {
			t.Fatal("AgentAllowedTools returned a reference to internal data")
		}
	})
}

func TestAgentTypeVision(t *testing.T) {
	t.Run("appears in AllAgentTypes", func(t *testing.T) {
		if !slices.Contains(AllAgentTypes(), AgentTypeVision) {
			t.Fatal("AgentTypeVision not found in AllAgentTypes()")
		}
	})

	t.Run("ValidAgentType returns true", func(t *testing.T) {
		if !ValidAgentType("vision") {
			t.Fatal("ValidAgentType(\"vision\") = false, want true")
		}
	})

	t.Run("AgentAllowedTools returns read", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeVision)
		if !slices.Contains(tools, "read") {
			t.Fatalf("AgentAllowedTools(vision) = %v, want to contain \"read\"", tools)
		}
	})

	t.Run("AgentSystemPrompt returns non-empty string", func(t *testing.T) {
		p := AgentSystemPrompt(AgentTypeVision)
		if p == "" {
			t.Fatal("AgentSystemPrompt(vision) returned empty string")
		}
	})
}

func TestAgentAllowedToolsUnchangedByMerge(t *testing.T) {
	// Merging extra tools must not mutate the built-in allowlists: the values
	// returned by AgentAllowedTools are identical before and after a merge, and
	// mutating the merged result does not leak into them.
	for _, at := range AllAgentTypes() {
		before := AgentAllowedTools(at)
		merged := mergedAllowedTools(before, []string{"mcp__srv__extra", before[0]})
		after := AgentAllowedTools(at)
		if !slices.Equal(before, after) {
			t.Errorf("AgentAllowedTools(%q) changed after merge: before %v, after %v", at, before, after)
		}
		merged[0] = "mutated"
		if !slices.Equal(before, after) {
			t.Errorf("mutating merged result changed AgentAllowedTools(%q): %v", at, after)
		}
	}
}

func TestTemplateLoading(t *testing.T) {
	// Verify that all agent prompts and the code suffix template load
	// successfully without errors. This ensures the embedded templates are
	// present and valid at runtime.
	for _, at := range AllAgentTypes() {
		t.Run(string(at), func(t *testing.T) {
			switch at {
			case AgentTypeCode:
				// Code agent has no prompt, only a suffix
				if p := AgentSystemPrompt(at); p != "" {
					t.Errorf("AgentSystemPrompt(%q) should be empty, got %q", at, p)
				}
			default:
				p := AgentSystemPrompt(at)
				if p == "" {
					t.Errorf("AgentSystemPrompt(%q) failed to load", at)
				}
			}
		})
	}

	// Verify code suffix loads
	suffix := AgentSystemSuffix(AgentTypeCode)
	if suffix == "" {
		t.Error("AgentSystemSuffix(code) failed to load")
	}

	// Verify other types have no suffix
	for _, at := range AllAgentTypes() {
		if at != AgentTypeCode {
			if suffix := AgentSystemSuffix(at); suffix != "" {
				t.Errorf("AgentSystemSuffix(%q) should be empty, got non-empty string", at)
			}
		}
	}
}
