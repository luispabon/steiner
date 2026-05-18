package delegation

import (
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
