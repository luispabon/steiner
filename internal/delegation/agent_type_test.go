package delegation

import (
	"slices"
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
		{name: "plan is valid", input: "plan", want: true},
		{name: "verify is valid", input: "verify", want: true},
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
	if len(types) != 6 {
		t.Fatalf("AllAgentTypes() returned %d types, want 6", len(types))
	}
	for i, at := range types {
		if at == "" {
			t.Fatalf("AllAgentTypes()[%d] is empty", i)
		}
	}
}

func TestAllSpecializedDelegateTools(t *testing.T) {
	tools := AllSpecializedDelegateTools()
	want := []string{"explore", "research", "code", "plan", "verify", "vision", "follow_up"}

	if !slices.Equal(tools, want) {
		t.Fatalf("AllSpecializedDelegateTools() = %v, want %v", tools, want)
	}

	tools[0] = "mutated"
	if got := AllSpecializedDelegateTools(); !slices.Equal(got, want) {
		t.Fatalf("AllSpecializedDelegateTools() returned shared backing storage: %v", got)
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

	t.Run("explore has no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeExplore)
		for _, m := range append(legacyMutationTools, "bash", "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(explore) should not contain %q", m)
			}
		}
	})

	t.Run("plan has no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypePlan)
		for _, m := range append(legacyMutationTools, "bash", "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(plan) should not contain %q", m)
			}
		}
	})

	t.Run("verify has bash but not mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeVerify)
		if !slices.Contains(tools, "bash") {
			t.Fatal("AgentAllowedTools(verify) missing bash")
		}
		for _, m := range append(legacyMutationTools, "mutate") {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(verify) should not contain %q", m)
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
