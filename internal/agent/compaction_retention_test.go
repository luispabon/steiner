package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestSelectRetention pins the step-down choice on the retention fixture: three
// turns of normal tail, one tool-heavy oldest turn, budgets set to the fit points
// measured for that fixture.
func TestSelectRetention(t *testing.T) {
	messages := retentionFixtureMessages()
	tests := []struct {
		name         string
		messages     []Message
		contextSize  int
		maxUnits     int
		wantOK       bool
		wantUnits    int
		wantRetained int
	}{
		{
			name:         "largest fitting tail is three units",
			messages:     messages,
			contextSize:  6000,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    3,
			wantRetained: 7,
		},
		{
			name:         "steps down to two units when three overflow",
			messages:     messages,
			contextSize:  5060,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    2,
			wantRetained: 4,
		},
		{
			name:         "steps down to one unit when two overflow",
			messages:     messages,
			contextSize:  2100,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    1,
			wantRetained: 2,
		},
		{
			name:         "no tail fits",
			messages:     messages,
			contextSize:  1500,
			maxUnits:     3,
			wantOK:       false,
			wantUnits:    0,
			wantRetained: 2,
		},
		{
			name:         "fewer units than maxUnits keeps all of them",
			messages:     messages[5:],
			contextSize:  6000,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    2,
			wantRetained: 4,
		},
		{
			name:         "unknown window accepts maxUnits",
			messages:     messages,
			contextSize:  0,
			maxUnits:     3,
			wantOK:       true,
			wantUnits:    3,
			wantRetained: 7,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := &fakeProvider{}
			req := retentionFixtureRequest(tt.contextSize, providerStub)
			state := RunState{Lineage: newConversationLineage(tt.messages)}
			source, retained, units, ok, err := selectRetention(context.Background(), req, state, tt.messages, tt.messages, prompt.CompactionModeNormal, tt.maxUnits)
			if err != nil {
				t.Fatalf("selectRetention() error = %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if units != tt.wantUnits {
				t.Fatalf("units = %d, want %d", units, tt.wantUnits)
			}
			if len(retained) != tt.wantRetained {
				t.Fatalf("retained = %d messages, want %d", len(retained), tt.wantRetained)
			}
			if got, want := len(source)+len(retained), len(tt.messages); got != want {
				t.Fatalf("source+retained = %d messages, want %d (a partition of the conversation)", got, want)
			}
			if got := len(providerStub.requests); got != 0 {
				t.Fatalf("model calls = %d, want 0 (selection is computation only)", got)
			}
		})
	}
}

// parallelToolOutput is the body of each parallel tool result in parallelToolMessages.
var parallelToolOutput = strings.Repeat("parallel output line 0123456789 ", 40)

// parallelToolMessages returns four turns. The second turn's assistant issues five
// tool calls at once, so its unit holds one assistant and five results.
func parallelToolMessages() []Message {
	const calls = 5
	toolCalls := make([]ToolCall, 0, calls)
	for i := range calls {
		toolCalls = append(toolCalls, ToolCall{ID: fmt.Sprintf("call-%d", i), Name: "read"})
	}
	messages := []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: toolCalls},
	}
	for _, call := range toolCalls {
		messages = append(messages, Message{Role: MessageRoleTool, Name: "read", ToolCallID: call.ID, Content: parallelToolOutput})
	}
	return append(messages,
		Message{Role: MessageRoleUser, Content: "turn 3 user"},
		Message{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
		Message{Role: MessageRoleUser, Content: "turn 4 user"},
		Message{Role: MessageRoleAssistant, Content: "turn 4 assistant"},
	)
}

// subAgentPairMessages returns three turns. The second turn's assistant calls a
// read and a sub-agent; both results follow it, and the sub-agent completion is
// its own user-role message, as delivery writes it.
func subAgentPairMessages() []Message {
	return []Message{
		{Role: MessageRoleUser, Content: "turn 1 user"},
		{Role: MessageRoleAssistant, Content: "turn 1 assistant"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 assistant", ToolCalls: []ToolCall{{ID: "call-read", Name: "read"}, {ID: "call-agent", Name: "sub_agent"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-read", Content: parallelToolOutput},
		{Role: MessageRoleTool, Name: "sub_agent", ToolCallID: "call-agent", Content: "sub-agent started"},
		{Role: MessageRoleUser, Content: "sub-agent result for call-agent", Source: MessageSourceSubAgentResult},
		{Role: MessageRoleUser, Content: "turn 3 user"},
		{Role: MessageRoleAssistant, Content: "turn 3 assistant"},
	}
}

// summaryProvider returns a fakeProvider whose first summary is first and whose
// later summaries are the emergency handoff text.
func summaryProvider(first string) *fakeProvider {
	calls := 0
	return &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		calls++
		content := "emergency handoff text"
		if calls == 1 {
			content = first
		}
		return provider.ChatResponse{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: content},
			FinishReason: "stop",
		}, nil
	}}
}

// compactState runs the summarize compactor on state with req and fails the test
// on an error.
func compactState(t *testing.T, req RunRequest, state RunState) CompactionOutcome {
	t.Helper()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate, "")
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	return outcome
}

// assertCallResultPairs checks that each assistant tool call has its result right
// after the assistant, in call order.
func assertCallResultPairs(t *testing.T, messages []Message) {
	t.Helper()
	for i, message := range messages {
		if message.Role != MessageRoleAssistant {
			continue
		}
		for j, call := range message.ToolCalls {
			at := i + 1 + j
			if at >= len(messages) || messages[at].Role != MessageRoleTool || messages[at].ToolCallID != call.ID {
				t.Errorf("message %d tool call %q has no result at message %d", i, call.ID, at)
			}
		}
	}
}

// assertReplayKeepsMessages checks that replay normalization drops no message and
// clears no assistant tool call.
func assertReplayKeepsMessages(t *testing.T, messages []Message) {
	t.Helper()
	if got, want := len(ToReplaySafeProviderMessages(messages)), len(messages); got != want {
		t.Fatalf("replay messages = %d, want %d (nothing dropped)", got, want)
	}
	replay := ReplaySafeConversation(messages)
	for i, message := range messages {
		if got, want := len(replay[i].ToolCalls), len(message.ToolCalls); got != want {
			t.Errorf("message %d replay tool calls = %d, want %d", i, got, want)
		}
	}
}

// countToolResults returns the number of tool result messages in messages.
func countToolResults(messages []Message) int {
	count := 0
	for _, message := range messages {
		if message.Role == MessageRoleTool {
			count++
		}
	}
	return count
}

// TestCompactionHugeSummaryEscalatesFromSmallTail checks emergency escalation on
// the retention fixture at 4930. A normal summary that fits keeps the two-unit
// normal tail. A huge one escalates to the one-unit emergency tail.
func TestCompactionHugeSummaryEscalatesFromSmallTail(t *testing.T) {
	tests := []struct {
		name         string
		summary      string
		wantMode     prompt.CompactionMode
		wantStages   int
		wantCalls    int
		wantRetained int
		wantFirst    string
	}{
		{
			name:         "fitting normal summary keeps the normal tail",
			summary:      "summary handoff text",
			wantMode:     prompt.CompactionModeNormal,
			wantStages:   1,
			wantCalls:    1,
			wantRetained: 4,
			wantFirst:    "turn 3 user",
		},
		{
			name:         "huge normal summary escalates to the emergency tail",
			summary:      strings.Repeat("summary detail ", 600),
			wantMode:     prompt.CompactionModeEmergency,
			wantStages:   2,
			wantCalls:    2,
			wantRetained: 2,
			wantFirst:    "turn 4 user",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := summaryProvider(tt.summary)
			messages := retentionFixtureMessages()
			outcome := compactState(t, retentionFixtureRequest(4930, providerStub), RunState{Conversation: messages, Lineage: newConversationLineage(messages)})
			if !outcome.Applied {
				t.Fatal("Applied = false, want true")
			}
			if outcome.Mode != tt.wantMode || outcome.StageCount != tt.wantStages {
				t.Fatalf("Mode = %q, StageCount = %d, want %q, %d", outcome.Mode, outcome.StageCount, tt.wantMode, tt.wantStages)
			}
			if got := len(providerStub.requests); got != tt.wantCalls {
				t.Fatalf("summary calls = %d, want %d", got, tt.wantCalls)
			}
			if got := len(outcome.RetainedMessages); got != tt.wantRetained {
				t.Fatalf("retained messages = %d, want %d", got, tt.wantRetained)
			}
			if got := outcome.RetainedMessages[0].Content; got != tt.wantFirst {
				t.Errorf("first retained message = %q, want %q", got, tt.wantFirst)
			}
		})
	}
}

// TestCompactionParallelToolResultsKeepPairsUnderStepDown checks that a unit with
// five parallel tool calls moves as one piece. The normal tail keeps all five
// results. The emergency tail that cuts the unit leaves no orphan result.
func TestCompactionParallelToolResultsKeepPairsUnderStepDown(t *testing.T) {
	tests := []struct {
		name            string
		contextSize     int
		wantMode        prompt.CompactionMode
		wantStages      int
		wantRetained    int
		wantToolResults int
	}{
		{
			name:            "normal tail keeps the whole parallel unit",
			contextSize:     5380,
			wantMode:        prompt.CompactionModeNormal,
			wantStages:      1,
			wantRetained:    11,
			wantToolResults: 5,
		},
		{
			name:            "emergency tail cuts the parallel unit",
			contextSize:     4250,
			wantMode:        prompt.CompactionModeEmergency,
			wantStages:      2,
			wantRetained:    2,
			wantToolResults: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := summaryProvider("summary handoff text")
			messages := parallelToolMessages()
			outcome := compactState(t, retentionFixtureRequest(tt.contextSize, providerStub), RunState{Conversation: messages, Lineage: newConversationLineage(messages)})
			if outcome.Mode != tt.wantMode || outcome.StageCount != tt.wantStages {
				t.Fatalf("Mode = %q, StageCount = %d, want %q, %d", outcome.Mode, outcome.StageCount, tt.wantMode, tt.wantStages)
			}
			retained := outcome.RetainedMessages
			if got := len(retained); got != tt.wantRetained {
				t.Fatalf("retained messages = %d, want %d", got, tt.wantRetained)
			}
			if got := countToolResults(retained); got != tt.wantToolResults {
				t.Errorf("retained tool results = %d, want %d", got, tt.wantToolResults)
			}
			assertCallResultPairs(t, retained)
			assertReplayKeepsMessages(t, retained)
		})
	}
}

// TestCompactionSubAgentResultKeepsItsPairUnderStepDown checks a read and a
// sub-agent call with their results, followed by the sub-agent completion. The
// completion is a user message, so it starts its own unit. The normal tail keeps
// it with its call. The emergency tail summarizes it without orphaning results.
func TestCompactionSubAgentResultKeepsItsPairUnderStepDown(t *testing.T) {
	tests := []struct {
		name               string
		contextSize        int
		wantMode           prompt.CompactionMode
		wantStages         int
		wantRetained       int
		wantSubAgentResult bool
	}{
		{
			name:               "normal tail keeps the call with its sub-agent result",
			contextSize:        3460,
			wantMode:           prompt.CompactionModeNormal,
			wantStages:         1,
			wantRetained:       7,
			wantSubAgentResult: true,
		},
		{
			name:               "emergency tail summarizes the call and its sub-agent result",
			contextSize:        2900,
			wantMode:           prompt.CompactionModeEmergency,
			wantStages:         2,
			wantRetained:       2,
			wantSubAgentResult: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			providerStub := summaryProvider("summary handoff text")
			messages := subAgentPairMessages()
			outcome := compactState(t, retentionFixtureRequest(tt.contextSize, providerStub), RunState{Conversation: messages, Lineage: newConversationLineage(messages)})
			if outcome.Mode != tt.wantMode || outcome.StageCount != tt.wantStages {
				t.Fatalf("Mode = %q, StageCount = %d, want %q, %d", outcome.Mode, outcome.StageCount, tt.wantMode, tt.wantStages)
			}
			retained := outcome.RetainedMessages
			if got := len(retained); got != tt.wantRetained {
				t.Fatalf("retained messages = %d, want %d", got, tt.wantRetained)
			}
			if retained[0].Role != MessageRoleUser {
				t.Errorf("first retained role = %q, want a user message at a unit boundary", retained[0].Role)
			}
			hasSubAgent := false
			for _, message := range retained {
				if message.Source == MessageSourceSubAgentResult {
					hasSubAgent = true
				}
			}
			if hasSubAgent != tt.wantSubAgentResult {
				t.Errorf("sub-agent result retained = %v, want %v", hasSubAgent, tt.wantSubAgentResult)
			}
			assertCallResultPairs(t, retained)
			assertReplayKeepsMessages(t, retained)
		})
	}
}

// TestCompactionShrinkingTailMovesNewestUserIntoSource checks the request block
// when the normal tail steps down from three units to two. The newest real user
// message moves from the tail into the source. The verbatim request stays
// RequestBlockNew and within the window's cap, which the step-down does not change.
func TestCompactionShrinkingTailMovesNewestUserIntoSource(t *testing.T) {
	tests := []struct {
		name        string
		contextSize int
		wantRequest string
	}{
		{name: "three-unit tail leaves turn 1 as the newest source request", contextSize: 7440, wantRequest: "turn 1 user"},
		{name: "two-unit tail moves turn 2 into the source", contextSize: 4930, wantRequest: "turn 2 user"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := retentionFixtureMessages()
			req := retentionFixtureRequest(tt.contextSize, summaryProvider("summary handoff text"))
			state := RunState{Conversation: messages, Lineage: newConversationLineage(messages)}
			source, _, _, ok, err := selectRetention(context.Background(), req, state, messages, messages, prompt.CompactionModeNormal, normalCompactionRetainTurns)
			if err != nil || !ok {
				t.Fatalf("selectRetention() ok = %v, err = %v, want a fitting tail", ok, err)
			}
			if request, status := latestRequestBlock(source, req.ModelBudget.ContextSize); request != tt.wantRequest || status != output.RequestBlockNew {
				t.Fatalf("latestRequestBlock() = (%q, %q), want (%q, %q)", request, status, tt.wantRequest, output.RequestBlockNew)
			}
			outcome := compactState(t, req, state)
			if outcome.RequestBlock != output.RequestBlockNew {
				t.Errorf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
			}
			if got, want := outcome.RequestBlockBytes, len(tt.wantRequest); got != want {
				t.Errorf("RequestBlockBytes = %d, want %d (verbatim, not truncated)", got, want)
			}
			if got, limit := outcome.RequestBlockBytes, requestBlockCapBytes(tt.contextSize); got > limit {
				t.Errorf("RequestBlockBytes = %d, want at most the cap %d", got, limit)
			}
			if _, block, ok := SplitRequestBlock(outcome.SummaryText); !ok || block != tt.wantRequest {
				t.Errorf("summary trailer = %q (ok %v), want %q", block, ok, tt.wantRequest)
			}
		})
	}
}

// TestWithoutProtectedOutputs checks which unit messages the model has not read
// yet: tool and sub-agent results after the unit's last assistant message.
func TestWithoutProtectedOutputs(t *testing.T) {
	subAgent := Message{Role: MessageRoleUser, Content: "sub", Source: MessageSourceSubAgentResult}
	tests := []struct {
		name string
		unit []Message
		want []string
	}{
		{
			name: "tool results after the last assistant are protected",
			unit: []Message{
				{Role: MessageRoleUser, Content: "u"},
				{Role: MessageRoleAssistant, Content: "a"},
				{Role: MessageRoleTool, Content: "t1"},
				{Role: MessageRoleTool, Content: "t2"},
			},
			want: []string{"user:u", "assistant:a"},
		},
		{
			name: "tool results before the last assistant stay",
			unit: []Message{
				{Role: MessageRoleAssistant, Content: "a1"},
				{Role: MessageRoleTool, Content: "t1"},
				{Role: MessageRoleAssistant, Content: "a2"},
				{Role: MessageRoleTool, Content: "t2"},
			},
			want: []string{"assistant:a1", "tool:t1", "assistant:a2"},
		},
		{
			name: "sub-agent result alone is protected",
			unit: []Message{subAgent},
			want: nil,
		},
		{
			name: "unit without an assistant protects its tool results",
			unit: []Message{
				{Role: MessageRoleUser, Content: "u"},
				{Role: MessageRoleTool, Content: "t"},
			},
			want: []string{"user:u"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]string, 0, len(tt.unit))
			for _, message := range withoutProtectedOutputs(tt.unit) {
				got = append(got, string(message.Role)+":"+message.Content)
			}
			if !slices.Equal(got, tt.want) {
				t.Fatalf("withoutProtectedOutputs() = %q, want %q", got, tt.want)
			}
		})
	}
}
