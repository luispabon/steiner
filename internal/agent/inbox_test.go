package agent

import (
	"context"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func completeResponse(content string) provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: content},
		FinishReason: "stop",
		Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
	}
}

func inboxRunRequest(p provider.Provider, drain func() InboxDrain) RunRequest {
	return RunRequest{
		Provider: p,
		Executor: &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
			return map[string]any{"ok": true}, nil
		}},
		Prompt: prompt.AssemblyOptions{
			Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "go"}},
		},
		Limits:     Limits{MaxTurns: 6, MaxTokens: 1000},
		DrainInbox: drain,
	}
}

func TestRunnerInboxWakeControlsContinuation(t *testing.T) {
	tests := []struct {
		name      string
		wake      bool
		wantCalls int
	}{
		{"quiet delivery does not force a turn", false, 1},
		{"wake delivery forces another turn", true, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &fakeProvider{responses: []provider.ChatResponse{completeResponse("a"), completeResponse("b")}}
			drained := 0
			var events []output.Event
			req := inboxRunRequest(stub, func() InboxDrain {
				drained++
				if drained > 1 {
					return InboxDrain{}
				}
				msg := Message{Role: MessageRoleUser, Content: "delivered", Source: MessageSourceSubAgentResult}
				return InboxDrain{Message: &msg, Wake: tt.wake}
			})
			req.Events = output.SinkFunc(func(e output.Event) { events = append(events, e) })
			state, err := NewRunner().Run(context.Background(), req)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if len(stub.requests) != tt.wantCalls {
				t.Fatalf("provider calls = %d, want %d", len(stub.requests), tt.wantCalls)
			}
			found := false
			for _, m := range state.Conversation {
				found = found || m.Content == "delivered"
			}
			if !found {
				t.Fatal("delivered message missing from conversation")
			}
			for _, e := range events {
				if _, ok := e.Payload.(output.SteerReceivedEvent); ok {
					t.Fatalf("SteerReceived emitted for a drain without UserText")
				}
			}
		})
	}
}

func TestRunnerInboxSteerEventCarriesOnlyUserText(t *testing.T) {
	stub := &fakeProvider{responses: []provider.ChatResponse{completeResponse("a"), completeResponse("b")}}
	drained := 0
	var payloads []string
	req := inboxRunRequest(stub, func() InboxDrain {
		drained++
		if drained > 1 {
			return InboxDrain{}
		}
		msg := Message{Role: MessageRoleUser, Content: "notice\n\ntyped"}
		return InboxDrain{Message: &msg, Wake: true, UserText: "typed"}
	})
	req.Events = output.SinkFunc(func(e output.Event) {
		if p, ok := e.Payload.(output.SteerReceivedEvent); ok {
			payloads = append(payloads, p.Text)
		}
	})
	if _, err := NewRunner().Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(payloads) != 1 || payloads[0] != "typed" {
		t.Fatalf("SteerReceived payloads = %q, want [typed]", payloads)
	}
}

func TestRunnerToolBatchDone(t *testing.T) {
	stub := &fakeProvider{responses: []provider.ChatResponse{
		{
			Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{
				{ID: "call-1", Name: "read", Arguments: map[string]any{}},
				{ID: "call-2", Name: "read", Arguments: map[string]any{}},
			}},
			FinishReason: "tool_calls",
			Usage:        &provider.UsageStats{TotalTokens: 2, CompletionTokens: 2},
		},
		completeResponse("done"),
	}}
	var seen []string
	req := inboxRunRequest(stub, nil)
	req.Executor = &fakeExecutor{execute: func(ctx context.Context, _ string, _ map[string]any) (any, error) {
		seen = append(seen, ToolBatchIDFrom(ctx))
		return map[string]any{"ok": true}, nil
	}}
	var done []string
	req.OnToolBatchDone = func(id string) { done = append(done, id) }
	if _, err := NewRunner().Run(context.Background(), req); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(done) != 1 || done[0] != "call-1" {
		t.Fatalf("OnToolBatchDone ids = %v, want [call-1]", done)
	}
	if len(seen) != 2 || seen[0] != "call-1" || seen[1] != "call-1" {
		t.Fatalf("handler batch ids = %v, want [call-1 call-1]", seen)
	}
}

func TestIsStrictlyEmptyAssistant(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want bool
	}{
		{"empty", Message{Role: MessageRoleAssistant}, true},
		{"content", Message{Role: MessageRoleAssistant, Content: "x"}, false},
		{"reasoning", Message{Role: MessageRoleAssistant, ReasoningContent: "r"}, false},
		{"metadata", Message{Role: MessageRoleAssistant, ProviderMetadata: &MessageProviderMetadata{}}, false},
		{"tool call", Message{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "c"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStrictlyEmptyAssistant(tt.msg); got != tt.want {
				t.Fatalf("isStrictlyEmptyAssistant = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunnerDropsStrictlyEmptyAssistant(t *testing.T) {
	stub := &fakeProvider{responses: []provider.ChatResponse{{
		Message:      provider.Message{Role: provider.MessageRoleAssistant},
		FinishReason: "stop",
		Usage:        &provider.UsageStats{TotalTokens: 1, CompletionTokens: 1},
	}}}
	state, err := NewRunner().Run(context.Background(), inboxRunRequest(stub, nil))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, m := range state.Conversation {
		if m.Role == MessageRoleAssistant {
			t.Fatalf("empty assistant message was appended: %+v", m)
		}
	}
}

func TestAppendPendingSubAgentsLine(t *testing.T) {
	pending := []PendingSubAgent{{AgentID: "a1", AgentType: "explore", State: SubAgentRunning}}
	tests := []struct {
		name    string
		fn      func() []PendingSubAgent
		wantMsg bool
	}{
		{"nil hook", nil, false},
		{"empty list", func() []PendingSubAgent { return nil }, false},
		{"pending", func() []PendingSubAgent { return pending }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := []Message{{Role: MessageRoleUser, Content: "u"}}
			state := RunState{Conversation: base, Lineage: newConversationLineage(base)}
			appendPendingSubAgentsLine(RunRequest{PendingSubAgents: tt.fn}, &state)
			if tt.wantMsg != (len(state.Conversation) == 2) {
				t.Fatalf("conversation len = %d, wantMsg %v", len(state.Conversation), tt.wantMsg)
			}
			if !tt.wantMsg {
				return
			}
			last := state.Conversation[1]
			if last.Source != MessageSourceSubAgentResult || IsRealUserMessage(last) {
				t.Fatalf("appended message source = %q, want sub_agent_result", last.Source)
			}
			if got := state.Lineage.FullMessages(); len(got) != 2 {
				t.Fatalf("lineage len = %d, want 2", len(got))
			}
		})
	}
}
