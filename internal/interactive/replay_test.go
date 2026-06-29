package interactive

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/delegation"
)

func TestIsDelegateToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{name: "delegate is delegate tool", toolName: "delegate", want: true},
		{name: "explore is delegate tool", toolName: "explore", want: true},
		{name: "research is delegate tool", toolName: "research", want: true},
		{name: "code is delegate tool", toolName: "code", want: true},
		{name: "plan is delegate tool", toolName: "plan", want: true},
		{name: "verify is delegate tool", toolName: "verify", want: true},
		{name: "follow_up is delegate tool", toolName: "follow_up", want: true},
		{name: "read is not delegate tool", toolName: "read", want: false},
		{name: "mutate is not delegate tool", toolName: "mutate", want: false},
		{name: "bash is not delegate tool", toolName: "bash", want: false},
		{name: "delegate uppercase", toolName: "DELEGATE", want: true},
		{name: "explore uppercase", toolName: "EXPLORE", want: true},
		{name: "follow_up uppercase", toolName: "FOLLOW_UP", want: true},
		{name: "empty string is not delegate tool", toolName: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDelegateToolCall(tt.toolName)
			if got != tt.want {
				t.Errorf("isDelegateToolCall(%q) = %v, want %v", tt.toolName, got, tt.want)
			}
		})
	}
}

func TestIsDelegateToolCallAllSpecializedTools(t *testing.T) {
	allSpecializedTools := delegation.AllSpecializedDelegateTools()
	for _, toolName := range allSpecializedTools {
		if !isDelegateToolCall(toolName) {
			t.Errorf("isDelegateToolCall(%q) = false, want true (from AllSpecializedDelegateTools)", toolName)
		}
	}
}

func TestIsDelegateToolCallDelegateToolName(t *testing.T) {
	if !isDelegateToolCall(delegation.DelegateToolName) {
		t.Errorf("isDelegateToolCall(%q) = false, want true", delegation.DelegateToolName)
	}
}

func TestIsDelegateToolCallCaseInsensitive(t *testing.T) {
	testCases := []string{
		delegation.DelegateToolName,
	}
	testCases = append(testCases, delegation.AllSpecializedDelegateTools()...)

	for _, toolName := range testCases {
		lower := strings.ToLower(toolName)
		upper := strings.ToUpper(toolName)
		mixed := strings.ToUpper(toolName[:1]) + strings.ToLower(toolName[1:])

		if !isDelegateToolCall(lower) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", lower)
		}
		if !isDelegateToolCall(upper) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", upper)
		}
		if !isDelegateToolCall(mixed) {
			t.Errorf("isDelegateToolCall(%q) = false, want true", mixed)
		}
	}
}
