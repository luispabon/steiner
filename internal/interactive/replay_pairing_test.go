package interactive

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestPairToolResults(t *testing.T) {
	t.Parallel()
	call := func(id, name string) agent.ToolCall { return agent.ToolCall{ID: id, Name: name} }
	assistant := func(calls ...agent.ToolCall) agent.Message {
		return agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: calls}
	}
	result := func(id, name string) agent.Message {
		return agent.Message{Role: agent.MessageRoleTool, ToolCallID: id, Name: name}
	}
	type pairWant struct {
		call   replayOccurrenceKey
		result int
		compat bool
	}
	tests := []struct {
		name           string
		msgs           []agent.Message
		want           []pairWant
		wantUnbalanced []string
	}{
		{
			name: "one to one",
			msgs: []agent.Message{assistant(call("a", "bash"), call("b", "read")), result("b", "read"), result("a", "bash")},
			want: []pairWant{{replayOccurrenceKey{0, 1}, 1, true}, {replayOccurrenceKey{0, 0}, 2, true}},
		},
		{
			name: "duplicate IDs pair first in first out",
			msgs: []agent.Message{assistant(call("a", "bash"), call("a", "bash")), result("a", "bash"), result("a", "bash")},
			want: []pairWant{{replayOccurrenceKey{0, 0}, 1, true}, {replayOccurrenceKey{0, 1}, 2, true}},
		},
		{
			name: "duplicate IDs across messages",
			msgs: []agent.Message{assistant(call("a", "bash")), result("a", "bash"), assistant(call("a", "bash")), result("a", "bash")},
			want: []pairWant{{replayOccurrenceKey{0, 0}, 1, true}, {replayOccurrenceKey{2, 0}, 3, true}},
		},
		{
			name:           "empty IDs never pair and never unbalance",
			msgs:           []agent.Message{assistant(call("", "bash")), result("", "bash")},
			wantUnbalanced: nil,
		},
		{
			name: "name mismatch pairs but is flagged",
			msgs: []agent.Message{assistant(call("a", "bash")), result("a", "read")},
			want: []pairWant{{replayOccurrenceKey{0, 0}, 1, false}},
		},
		{
			name: "empty result name is compatible",
			msgs: []agent.Message{assistant(call("a", "bash")), result("a", "")},
			want: []pairWant{{replayOccurrenceKey{0, 0}, 1, true}},
		},
		{
			name:           "more calls than results",
			msgs:           []agent.Message{assistant(call("a", "bash"), call("a", "bash")), result("a", "bash")},
			want:           []pairWant{{replayOccurrenceKey{0, 0}, 1, true}},
			wantUnbalanced: []string{"a"},
		},
		{
			name:           "more results than calls",
			msgs:           []agent.Message{assistant(call("a", "bash")), result("a", "bash"), result("a", "bash")},
			want:           []pairWant{{replayOccurrenceKey{0, 0}, 1, true}},
			wantUnbalanced: []string{"a"},
		},
		{
			name:           "result before its call does not pair backward",
			msgs:           []agent.Message{result("a", "bash"), assistant(call("a", "bash"))},
			wantUnbalanced: []string{"a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pairToolResults(tt.msgs)
			var pairs []pairWant
			for _, p := range got.pairs {
				pairs = append(pairs, pairWant{p.call, p.resultIndex, p.nameCompatible})
			}
			if !reflect.DeepEqual(pairs, tt.want) {
				t.Errorf("pairs = %v, want %v", pairs, tt.want)
			}
			var unbalanced []string
			for id := range got.unbalanced {
				unbalanced = append(unbalanced, id)
			}
			if !reflect.DeepEqual(unbalanced, tt.wantUnbalanced) {
				t.Errorf("unbalanced = %v, want %v", unbalanced, tt.wantUnbalanced)
			}
		})
	}
}
