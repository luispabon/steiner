package prompt

import "testing"

func TestContextSourceIsSystemZone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  ContextSource
		want bool
	}{
		{name: "preamble", src: ContextSourcePreamble, want: true},
		{name: "global agents", src: ContextSourceGlobalAgentsMD, want: true},
		{name: "project agents", src: ContextSourceProjectAgentsMD, want: true},
		{name: "conversation summary", src: ContextSourceConversationSummary, want: true},
		{name: "project context", src: ContextSourceProjectContext, want: false},
		{name: "durable context", src: ContextSourceDurableContext, want: false},
		{name: "tool result", src: ContextSourceToolResult, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.src.IsSystemZone(); got != tc.want {
				t.Fatalf("IsSystemZone() = %t, want %t", got, tc.want)
			}
		})
	}
}
