package session

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/prompt"
)

// activation renders a skill activation envelope for tests. Test bodies are
// far below the cap, so the truncation flag is ignored.
func activation(name, body string) string {
	block, _ := prompt.RenderSkillActivation(name, body)
	return block
}

// subAgentEnvelope renders a completed sub-agent result envelope for tests.
func subAgentEnvelope() string {
	return agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{
		AgentID:          "a1",
		AgentType:        "explore",
		Status:           "completed",
		ParentCallID:     "s1",
		ObjectivePreview: "Find other retry call sites",
		TurnCount:        3,
		TokenCount:       900,
		Duration:         12 * time.Second,
		Body:             "Found two more call sites.",
	})
}

func TestRenderHistory(t *testing.T) {
	tests := []struct {
		name   string
		source []agent.Message
		want   string
	}{
		{
			name: "empty input renders nothing",
			want: "",
		},
		{
			name: "user and assistant text",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 1, Content: "hi"},
				{Role: agent.MessageRoleAssistant, Turn: 1, Content: "hello"},
			},
			want: "### user · turn 1\n\nhi\n\n### assistant · turn 1\n\nhello",
		},
		{
			name: "turn 0 omits the turn number",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Content: "hi"},
			},
			want: "### user\n\nhi",
		},
		{
			name: "message with no body is skipped",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1},
				{Role: agent.MessageRoleUser, Turn: 1, Content: "hi"},
			},
			want: "### user · turn 1\n\nhi",
		},
		{
			name: "context block renders text under its role",
			source: []agent.Message{
				{Role: agent.MessageRoleContextBlock, Turn: 1, Content: "Project rules."},
			},
			want: "### context-block · turn 1\n\nProject rules.",
		},
		{
			name: "reasoning is dropped",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, Content: "answer", ReasoningContent: "secret reasoning"},
			},
			want: "### assistant · turn 1\n\nanswer",
		},
		{
			name: "summary becomes divider and text without archive note",
			source: []agent.Message{
				{Role: agent.MessageRoleSummary, Content: "Summary line.\n" + agent.HistoryNoteMarker + " Archived."},
			},
			want: "── compacted here ──\n\nSummary line.",
		},
		{
			name: "summary with only archive note renders divider",
			source: []agent.Message{
				{Role: agent.MessageRoleSummary, Content: agent.HistoryNoteMarker + " Archived."},
			},
			want: "── compacted here ──",
		},
		{
			name: "ok tool result marks call with check",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "bash", Arguments: map[string]any{"command": "go test ./..."}},
				}},
				{Role: agent.MessageRoleTool, Turn: 1, ToolCallID: "c1", Content: `{"ok":true,"result":{}}`},
			},
			want: "### assistant · turn 1\n\n- **bash** go test ./... ✓",
		},
		{
			name: "plain JSON tool result counts as ok",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "glob", Arguments: map[string]any{"pattern": "*.go"}},
				}},
				{Role: agent.MessageRoleTool, Turn: 1, ToolCallID: "c1", Content: `{"matches":[]}`},
			},
			want: "### assistant · turn 1\n\n- **glob** *.go ✓",
		},
		{
			name: "failed tool result marks call as error",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "bash", Arguments: map[string]any{"command": "go test ./..."}},
				}},
				{Role: agent.MessageRoleTool, Turn: 1, ToolCallID: "c1", Content: `{"ok":false,"error":{"kind":"tool","message":"exit status 1"}}`},
			},
			want: "### assistant · turn 1\n\n- **bash** go test ./... error",
		},
		{
			name: "unpaired tool call has no status",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "bash", Arguments: map[string]any{"command": "go test ./..."}},
				}},
			},
			want: "### assistant · turn 1\n\n- **bash** go test ./...",
		},
		{
			name: "tool call without arguments shows name only",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "glob"},
				}},
			},
			want: "### assistant · turn 1\n\n- **glob**",
		},
		{
			name: "mutate call shows first operation and count",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "c1", Name: "mutate", Arguments: map[string]any{"operations": []any{
						map[string]any{"type": "replace", "path": "a.go"},
						map[string]any{"type": "replace", "path": "b.go"},
					}}},
				}},
			},
			want: "### assistant · turn 1\n\n- **mutate** replace a.go (+1 more)",
		},
		{
			name: "sub_agent call shows objective",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "s1", Name: "sub_agent", Arguments: map[string]any{"type": "explore", "objective": "Find retry sites"}},
				}},
			},
			want: "### assistant · turn 1\n\n- **sub_agent** Find retry sites",
		},
		{
			name: "follow_up call shows message",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "f1", Name: "follow_up", Arguments: map[string]any{"message": "Check jitter"}},
				}},
			},
			want: "### assistant · turn 1\n\n- **follow_up** Check jitter",
		},
		{
			name: "display_file call is skipped",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, Content: "Shown.", ToolCalls: []agent.ToolCall{
					{ID: "d1", Name: "display_file", Arguments: map[string]any{"path": "a.go"}},
				}},
				{Role: agent.MessageRoleTool, Turn: 1, ToolCallID: "d1", Content: `{"ok":true}`},
			},
			want: "### assistant · turn 1\n\nShown.",
		},
		{
			name: "message with only display_file call is dropped",
			source: []agent.Message{
				{Role: agent.MessageRoleAssistant, Turn: 1, ToolCalls: []agent.ToolCall{
					{ID: "d1", Name: "display_file", Arguments: map[string]any{"path": "a.go"}},
				}},
			},
			want: "",
		},
		{
			name: "user image shows placeholder after text",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 1, Content: "Screenshot.", Images: []agent.ImageBlock{{ID: "img-1"}}},
			},
			want: "### user · turn 1\n\nScreenshot.\n\n[image: img-1]",
		},
		{
			name: "tool image renders placeholder without heading",
			source: []agent.Message{
				{Role: agent.MessageRoleTool, Turn: 1, ToolCallID: "c1", Content: `{"ok":true}`, Images: []agent.ImageBlock{{ID: "img-2"}}},
			},
			want: "[image: img-2]",
		},
		{
			name: "active skill becomes marker and keeps user text",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 1, Content: activation("review", "Body.") + "\n\nUse the review checklist."},
			},
			want: "### user · turn 1\n\n[skill: review]\n\nUse the review checklist.",
		},
		{
			name: "inactive skill renders nothing",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 1, Content: prompt.RenderSkillDeactivation("review")},
			},
			want: "",
		},
		{
			name: "sub-agent result batch is replaced",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 5, Source: agent.MessageSourceSubAgentResult, Content: subAgentEnvelope()},
			},
			want: "### user · turn 5\n\n[sub-agent result omitted]",
		},
		{
			name: "sub-agent envelope without source tag is replaced",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 5, Content: subAgentEnvelope()},
			},
			want: "### user · turn 5\n\n[sub-agent result omitted]",
		},
		{
			name: "text after sub-agent envelope is kept",
			source: []agent.Message{
				{Role: agent.MessageRoleUser, Turn: 5, Content: subAgentEnvelope() + "\n\nWhat next?"},
			},
			want: "### user · turn 5\n\n[sub-agent result omitted]\n\nWhat next?",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderHistory(tt.source); got != tt.want {
				t.Errorf("RenderHistory() = %q, want %q", got, tt.want)
			}
		})
	}
}

// mixedHistorySource returns a conversation covering every rendering rule.
// Its rendered form is pinned by testdata/history_expected.md.
func mixedHistorySource() []agent.Message {
	return []agent.Message{
		{Role: agent.MessageRoleSummary, Content: "The user reported retry storms in the provider client.\nNo fix applied yet.\n" + agent.HistoryNoteMarker + " Conversation before this summary is archived in the session history file."},
		{Role: agent.MessageRoleUser, Turn: 3, Content: "Retry storms are back. Look at the provider client again."},
		{Role: agent.MessageRoleAssistant, Turn: 3, Content: "I'll check the retry loop.", ReasoningContent: "Hidden reasoning that must not appear.", ToolCalls: []agent.ToolCall{
			{ID: "c1", Name: "grep", Arguments: map[string]any{"pattern": "maxRetries", "path": "internal/provider"}},
			{ID: "c2", Name: "display_file", Arguments: map[string]any{"path": "internal/provider/retry.go"}},
		}},
		{Role: agent.MessageRoleTool, Turn: 3, ToolCallID: "c1", Name: "grep", Content: `{"ok":true,"result":{"matches":["internal/provider/retry.go:14"]}}`},
		{Role: agent.MessageRoleTool, Turn: 3, ToolCallID: "c2", Name: "display_file", Content: `{"ok":true}`},
		{Role: agent.MessageRoleAssistant, Turn: 3, ToolCalls: []agent.ToolCall{
			{ID: "c3", Name: "bash", Arguments: map[string]any{"command": "go test ./internal/provider -run Retry"}},
			{ID: "c4", Name: "mutate", Arguments: map[string]any{"operations": []any{map[string]any{"type": "replace", "path": "internal/provider/retry.go"}}}},
		}},
		{Role: agent.MessageRoleTool, Turn: 3, ToolCallID: "c3", Name: "bash", Content: `{"ok":false,"error":{"kind":"tool","message":"exit status 1"}}`},
		{Role: agent.MessageRoleUser, Turn: 4, Content: "Screenshot of the log.", Images: []agent.ImageBlock{{ID: "img-1", MediaType: "image/png"}}},
		{Role: agent.MessageRoleAssistant, Turn: 4, Content: "Backoff doubles without a cap.", ToolCalls: []agent.ToolCall{
			{ID: "s1", Name: "sub_agent", Arguments: map[string]any{"type": "explore", "objective": "Find other retry call sites"}},
		}},
		{Role: agent.MessageRoleTool, Turn: 4, ToolCallID: "s1", Name: "sub_agent", Content: `{"ok":true,"result":{"agent_id":"a1"}}`},
		{Role: agent.MessageRoleUser, Turn: 5, Source: agent.MessageSourceSubAgentResult, Content: subAgentEnvelope()},
		{Role: agent.MessageRoleUser, Turn: 5, Content: activation("review", "Body.") + "\n\nUse the review checklist."},
		{Role: agent.MessageRoleAssistant, Turn: 5, ToolCalls: []agent.ToolCall{
			{ID: "f1", Name: "follow_up", Arguments: map[string]any{"message": "Check the jitter path too."}},
		}},
		{Role: agent.MessageRoleTool, Turn: 5, ToolCallID: "f1", Name: "follow_up", Content: `{"ok":false,"error":{"kind":"tool","message":"agent is busy"}}`},
		{Role: agent.MessageRoleUser, Turn: 6, Content: "Thanks, that is all."},
	}
}

// TestRenderHistoryGolden renders the mixed conversation twice and compares
// both results with testdata/history_expected.md. The golden file ends with a
// newline that RenderHistory does not emit.
func TestRenderHistoryGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/history_expected.md")
	if err != nil {
		t.Fatalf("read golden fixture: %v", err)
	}
	want := strings.TrimSuffix(string(raw), "\n")
	source := mixedHistorySource()
	for i := range 2 {
		if got := RenderHistory(source); got != want {
			t.Errorf("render %d mismatch\n got:\n%s\nwant:\n%s", i, got, want)
		}
	}
}
