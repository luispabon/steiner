package agent

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestIsLookupTool(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "read", want: true},
		{name: "grep", want: true},
		{name: "glob", want: true},
		{name: "lsp_hover", want: true},
		{name: "bash"},
		{name: "mutate"},
		{name: "mcp__srv__x"},
		{name: "sub_agent"},
	} {
		if got := isLookupTool(test.name); got != test.want {
			t.Errorf("isLookupTool(%q) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestWithLookupNote(t *testing.T) {
	encoded, err := json.Marshal(lookupStreakNote)
	if err != nil {
		t.Fatalf("marshal note: %v", err)
	}
	field := `,"harness_note":` + string(encoded)
	trailing := "\n\n" + lookupStreakNote
	for _, test := range []struct {
		name       string
		content    string
		want       string
		wantObject bool
	}{
		{name: "non-empty object", content: `{"a":1}`, want: `{"a":1` + field + `}`, wantObject: true},
		{name: "empty object", content: `{}`, want: `{"harness_note":` + string(encoded) + `}`, wantObject: true},
		{name: "object with trailing newline", content: "{\"a\":1}\n", want: "{\"a\":1" + field + "}\n", wantObject: true},
		{name: "object value containing a brace", content: `{"output":"}"}`, want: `{"output":"}"` + field + `}`, wantObject: true},
		{name: "plain text", content: "plain text", want: "plain text" + trailing},
		{name: "json array", content: `[1,2]`, want: `[1,2]` + trailing},
		{name: "malformed object", content: `{"a":`, want: `{"a":` + trailing},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := withLookupNote(test.content)
			if got != test.want {
				t.Fatalf("withLookupNote(%q) = %q, want %q", test.content, got, test.want)
			}
			if test.wantObject && !json.Valid([]byte(got)) {
				t.Fatalf("withLookupNote(%q) produced invalid JSON: %q", test.content, got)
			}
		})
	}
}

func TestWithLookupNoteKeepsReadResult(t *testing.T) {
	original := builtin.ReadResult{Path: "internal/agent/x.go", StartLine: 3, EndLine: 4, TotalLines: 9, FileHash: "abc", NextOffset: 4, Output: "line\n"}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal read result: %v", err)
	}
	nudged := withLookupNote(string(data))
	if !strings.Contains(nudged, `"`+lookupNoteField+`":`) {
		t.Fatalf("note field missing from %q", nudged)
	}
	var decoded builtin.ReadResult
	if err := json.Unmarshal([]byte(nudged), &decoded); err != nil {
		t.Fatalf("unmarshal nudged read result: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("read result = %+v, want %+v", decoded, original)
	}
	if _, ok := parseReadResult(nudged); !ok {
		t.Fatalf("parseReadResult rejected nudged content %q", nudged)
	}
}

func lookupStreakClassifier(name string) ParallelClass {
	if name == "sub_agent" {
		return ParallelClassDelegation
	}
	return ParallelClassTool
}

func TestApplyLookupStreakNudge(t *testing.T) {
	const content = `{"path":"a.go","output":"x"}`
	running := func() []PendingSubAgent {
		return []PendingSubAgent{{AgentID: "agent", State: SubAgentRunning}}
	}
	queued := func() []PendingSubAgent {
		return []PendingSubAgent{{AgentID: "agent", State: SubAgentQueued}}
	}
	finished := func() []PendingSubAgent {
		return []PendingSubAgent{{AgentID: "agent", State: SubAgentFinished}}
	}
	for _, test := range []struct {
		name       string
		async      bool
		agentID    string
		pending    func() []PendingSubAgent
		calls      []string
		wantNudged []bool
		wantStreak int
	}{
		{
			name:       "nudges on the third lookup only",
			async:      true,
			pending:    running,
			calls:      []string{"read", "grep", "glob", "read"},
			wantNudged: []bool{false, false, true, false},
			wantStreak: 4,
		},
		{
			name:       "lsp lookups count",
			async:      true,
			pending:    running,
			calls:      []string{"read", "lsp_hover", "lsp_definition"},
			wantNudged: []bool{false, false, true},
			wantStreak: 3,
		},
		{
			name:       "queued sub-agent nudges",
			async:      true,
			pending:    queued,
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, true},
			wantStreak: 3,
		},
		{
			name:       "non-lookup call resets the streak",
			async:      true,
			pending:    running,
			calls:      []string{"read", "read", "mutate", "read", "read", "read"},
			wantNudged: []bool{false, false, false, false, false, true},
			wantStreak: 3,
		},
		{
			name:       "bash resets the streak",
			async:      true,
			pending:    running,
			calls:      []string{"read", "read", "bash", "read", "read", "read"},
			wantNudged: []bool{false, false, false, false, false, true},
			wantStreak: 3,
		},
		{
			name:       "mcp tool resets the streak",
			async:      true,
			pending:    running,
			calls:      []string{"read", "read", "mcp__srv__x", "read", "read", "read"},
			wantNudged: []bool{false, false, false, false, false, true},
			wantStreak: 3,
		},
		{
			name:       "delegation call resets the streak",
			async:      true,
			pending:    running,
			calls:      []string{"read", "read", "sub_agent", "read", "read", "read"},
			wantNudged: []bool{false, false, false, false, false, true},
			wantStreak: 3,
		},
		{
			name:       "finished sub-agents do not nudge",
			async:      true,
			pending:    finished,
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, false},
			wantStreak: 3,
		},
		{
			name:       "empty pending list does not nudge",
			async:      true,
			pending:    func() []PendingSubAgent { return nil },
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, false},
			wantStreak: 3,
		},
		{
			name:       "nil pending source does not nudge",
			async:      true,
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, false},
		},
		{
			name:       "blocking session does not nudge",
			pending:    running,
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, false},
		},
		{
			name:       "child run does not nudge",
			async:      true,
			agentID:    "child",
			pending:    running,
			calls:      []string{"read", "grep", "glob"},
			wantNudged: []bool{false, false, false},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newTurnProgressor(RunRequest{
				ParallelClassOf:  lookupStreakClassifier,
				AgentID:          test.agentID,
				PendingSubAgents: test.pending,
				Prompt:           prompt.AssemblyOptions{AsyncSubAgents: test.async},
			}, prompt.AssemblyOptions{}, nil)
			for i, name := range test.calls {
				got := p.applyLookupStreakNudge(name, content)
				nudged := strings.Contains(got, lookupNoteField)
				if nudged != test.wantNudged[i] {
					t.Fatalf("call %d (%s) nudged = %v, want %v; content = %q", i+1, name, nudged, test.wantNudged[i], got)
				}
				if !nudged && got != content {
					t.Fatalf("call %d (%s) content = %q, want %q", i+1, name, got, content)
				}
			}
			if p.lookupStreak != test.wantStreak {
				t.Fatalf("lookupStreak = %d, want %d", p.lookupStreak, test.wantStreak)
			}
		})
	}
}

func lookupNudgeToolSpecs() []provider.ToolSpec {
	return []provider.ToolSpec{{Type: "function", Function: provider.ToolFunctionSpec{Name: "read", Description: "Read files", Parameters: map[string]any{"type": "object"}}}}
}

func lookupNudgeReadCall(id string) provider.ToolCall {
	return provider.ToolCall{ID: id, Name: "read", Arguments: map[string]any{"path": "a.go"}}
}

func lookupNudgeToolCallResponse(calls ...provider.ToolCall) provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: calls},
		FinishReason: "tool_calls",
		Usage:        &provider.UsageStats{TotalTokens: 3, CompletionTokens: 3},
	}
}

func lookupNudgeStopResponse() provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "done"},
		FinishReason: "stop",
		Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
	}
}

func lookupNudgeExecutor() *fakeExecutor {
	return &fakeExecutor{execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
		return tool.ExecutionResult{Value: builtin.ReadResult{Path: "a.go", StartLine: 1, EndLine: 1, TotalLines: 1, FileHash: "hash", Output: "package a"}}, nil
	}}
}

func lookupNudgeRequest(providerStub provider.Provider, events output.EventSink) RunRequest {
	return RunRequest{
		Provider:      providerStub,
		Executor:      lookupNudgeExecutor(),
		Tools:         lookupNudgeToolSpecs(),
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "check the area"}}, AsyncSubAgents: true},
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		MaxTokens:     intPtr(64),
		Limits:        Limits{MaxTurns: 6, MaxTokens: 100},
		PendingSubAgents: func() []PendingSubAgent {
			return []PendingSubAgent{{AgentID: "agent", State: SubAgentRunning}}
		},
		Events: events,
	}
}

func lookupNudgeToolMessages(conversation []Message) []Message {
	var messages []Message
	for _, message := range conversation {
		if message.Role == MessageRoleTool {
			messages = append(messages, message)
		}
	}
	return messages
}

func TestLookupStreakNudgePersistedButNotEmitted(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		lookupNudgeToolCallResponse(lookupNudgeReadCall("call_1"), lookupNudgeReadCall("call_2")),
		lookupNudgeToolCallResponse(lookupNudgeReadCall("call_3")),
		lookupNudgeStopResponse(),
	}}
	var events []output.Event
	state, err := NewRunner().Run(context.Background(), lookupNudgeRequest(providerStub, output.SinkFunc(func(event output.Event) { events = append(events, event) })))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	messages := lookupNudgeToolMessages(state.Conversation)
	if len(messages) != 3 {
		t.Fatalf("tool messages = %d, want 3", len(messages))
	}
	for i, message := range messages[:2] {
		if strings.Contains(message.Content, lookupNoteField) {
			t.Fatalf("tool message %d carries the note: %q", i+1, message.Content)
		}
	}
	third := messages[2]
	if !strings.Contains(third.Content, `"`+lookupNoteField+`":`) {
		t.Fatalf("third tool message missing the note: %q", third.Content)
	}
	var decoded builtin.ReadResult
	if err := json.Unmarshal([]byte(third.Content), &decoded); err != nil {
		t.Fatalf("unmarshal third tool message: %v", err)
	}
	if decoded.Path != "a.go" || decoded.FileHash != "hash" || decoded.TotalLines != 1 {
		t.Fatalf("read result = %+v, want the original path, hash and line count", decoded)
	}
	if _, ok := parseReadResult(third.Content); !ok {
		t.Fatalf("parseReadResult rejected %q", third.Content)
	}

	finished := 0
	for _, event := range events {
		if event.Type != output.EventTypeToolCallFinished {
			continue
		}
		finished++
		if result := event.Payload.(output.ToolCallFinishedEvent).Result; strings.Contains(result, lookupNoteField) {
			t.Fatalf("tool_call_finished result carries the note: %q", result)
		}
	}
	if finished != 3 {
		t.Fatalf("tool_call_finished events = %d, want 3", finished)
	}
}

func TestLookupStreakNudgeResetByDelivery(t *testing.T) {
	for _, test := range []struct {
		name     string
		deliver  bool
		wantNote int
	}{
		{name: "no delivery nudges on the third lookup", wantNote: 1},
		{name: "delivery resets the streak", deliver: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			providerStub := &fakeProvider{responses: []provider.ChatResponse{
				lookupNudgeToolCallResponse(lookupNudgeReadCall("call_1"), lookupNudgeReadCall("call_2")),
				lookupNudgeToolCallResponse(lookupNudgeReadCall("call_3")),
				lookupNudgeToolCallResponse(lookupNudgeReadCall("call_4")),
				lookupNudgeStopResponse(),
			}}
			delivered := false
			request := lookupNudgeRequest(providerStub, nil)
			request.DrainInbox = func() InboxDrain {
				if test.deliver && !delivered {
					delivered = true
					return InboxDrain{Message: &Message{Role: MessageRoleUser, Content: "a sub-agent reported in"}}
				}
				return InboxDrain{}
			}
			state, err := NewRunner().Run(context.Background(), request)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			nudged := 0
			for _, message := range lookupNudgeToolMessages(state.Conversation) {
				if strings.Contains(message.Content, lookupNoteField) {
					nudged++
				}
			}
			if nudged != test.wantNote {
				t.Fatalf("nudged tool messages = %d, want %d", nudged, test.wantNote)
			}
		})
	}
}
