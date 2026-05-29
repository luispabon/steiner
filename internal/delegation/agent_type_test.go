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
	if len(types) != 5 {
		t.Fatalf("AllAgentTypes() returned %d types, want 5", len(types))
	}
	for i, at := range types {
		if at == "" {
			t.Fatalf("AllAgentTypes()[%d] is empty", i)
		}
	}
}

func TestAgentSystemPrompt(t *testing.T) {
	for _, at := range AllAgentTypes() {
		t.Run(string(at), func(t *testing.T) {
			p := AgentSystemPrompt(at)
			if p == "" {
				t.Fatalf("AgentSystemPrompt(%q) returned empty string", at)
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
	for _, at := range AllAgentTypes() {
		t.Run(string(at), func(t *testing.T) {
			tools := AgentAllowedTools(at)
			if len(tools) == 0 {
				t.Fatalf("AgentAllowedTools(%q) returned empty slice", at)
			}
			if !slices.Contains(tools, "scratchpad") {
				t.Fatalf("AgentAllowedTools(%q) missing scratchpad", at)
			}
		})
	}

	t.Run("explore has no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypeExplore)
		for _, m := range []string{"bash", "mutate"} {
			if slices.Contains(tools, m) {
				t.Fatalf("AgentAllowedTools(explore) should not contain %q", m)
			}
		}
	})

	t.Run("plan has no mutation tools", func(t *testing.T) {
		tools := AgentAllowedTools(AgentTypePlan)
		for _, m := range []string{"bash", "mutate"} {
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
		if slices.Contains(tools, "mutate") {
			t.Fatal("AgentAllowedTools(verify) should not contain mutate")
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
		for _, m := range []string{"bash", "mutate"} {
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
