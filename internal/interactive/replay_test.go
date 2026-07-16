package interactive

import (
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/delegation"
	"github.com/luispabon/steiner/internal/output"
)

func TestIsDelegateToolCall(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		want     bool
	}{
		{name: "delegate is no longer a delegate tool", toolName: "delegate", want: false},
		{name: "explore is delegate tool", toolName: "explore", want: true},
		{name: "research is delegate tool", toolName: "research", want: true},
		{name: "code is delegate tool", toolName: "code", want: true},
		{name: "evaluate is delegate tool", toolName: "evaluate", want: true},
		{name: "sanity_check is delegate tool", toolName: "sanity_check", want: true},
		{name: "follow_up is delegate tool", toolName: "follow_up", want: true},
		{name: "read is not delegate tool", toolName: "read", want: false},
		{name: "mutate is not delegate tool", toolName: "mutate", want: false},
		{name: "bash is not delegate tool", toolName: "bash", want: false},
		{name: "delegate uppercase is no longer a delegate tool", toolName: "DELEGATE", want: false},
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

func TestReplaySessionMessagesSummaryRole(t *testing.T) {
	t.Parallel()
	var events []output.Event
	s := testNewSession(t, Dependencies{
		BaseEvents: output.SinkFunc(func(e output.Event) { events = append(events, e) }),
	})

	msgs := []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "compaction summary text"},
		{Role: agent.MessageRoleUser, Content: "hello"},
		{Role: agent.MessageRoleAssistant, Content: "hi"},
	}
	s.replaySessionMessages(msgs)

	var foundSummary bool
	for _, e := range events {
		if e.Type != output.EventTypeContextDiagnostics {
			continue
		}
		if compaction, ok := output.AsContextCompactionEvent(e.Payload); ok {
			if compaction.SummaryText == "compaction summary text" {
				foundSummary = true
			}
		}
	}
	if !foundSummary {
		t.Fatal("summary message not replayed as compaction diagnostics event")
	}
}

func TestIsDelegateToolCallCaseInsensitive(t *testing.T) {
	testCases := delegation.AllSpecializedDelegateTools()

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
