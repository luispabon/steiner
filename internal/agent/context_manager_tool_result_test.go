package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObserveFreshToolResultPassesContentThrough(t *testing.T) {
	bash, err := json.Marshal(map[string]any{
		"exit_code": 1,
		"timed_out": true,
		"message":   "command timed out",
		"output":    "  \x1b[31mred\x1b[0m\rspin\r\n\n\ttail  \n" + strings.Repeat("x", 20000),
	})
	if err != nil {
		t.Fatalf("marshal bash result: %v", err)
	}
	tests := []struct {
		name    string
		tool    string
		content string
	}{
		{"bash", "bash", string(bash)},
		{"grep", "grep", `{"matches":1,"output":"\u001b[31mmatch\u001b[0m\r\n"}`},
		{"read without annotations", "read", `{"path":"README.md","output":"alpha\n\nbeta\n"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &ContextStateManager{}
			if got := s.observeFreshToolResult(1, tt.tool, nil, tt.content, nil); got != tt.content {
				t.Fatalf("observeFreshToolResult(%s) changed content:\ngot  %q\nwant %q", tt.tool, got, tt.content)
			}
		})
	}
}
