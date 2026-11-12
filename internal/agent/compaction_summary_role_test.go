package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCompactionSummaryUserRoleStableAcrossRequests pins the user-role
// projection of a stored compaction summary. The first compaction produces a
// stored summary; the next normal turn and the next compaction request must
// share an identical, cache-stable prefix in which the summary is user-role
// historical context carrying the deterministic wrapper.
func TestCompactionSummaryUserRoleStableAcrossRequests(t *testing.T) {
	t.Parallel()

	const summaryText = "stable summary of earlier turns"
	conversation := []Message{
		{Role: MessageRoleUser, Content: "question one"},
		{Role: MessageRoleAssistant, Content: "answer one"},
		{Role: MessageRoleUser, Content: "question two"},
		{Role: MessageRoleAssistant, Content: "answer two"},
		{Role: MessageRoleUser, Content: "question three"},
		{Role: MessageRoleAssistant, Content: "answer three"},
		{Role: MessageRoleUser, Content: "question four"},
		{Role: MessageRoleAssistant, Content: "answer four"},
		{Role: MessageRoleUser, Content: "read a file"},
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "read"}}},
		{Role: MessageRoleTool, Content: "file body", ToolCallID: "call-1"},
		{Role: MessageRoleUser, Content: "last question"},
		{Role: MessageRoleAssistant, Content: "last answer"},
	}
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	sessionDate := prompt.NewSessionDate(time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC))
	req := RunRequest{
		Provider:      &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: summaryText}}}},
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-summary-role"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               100000,
			MaxCompletionTokens:       128,
			SummaryMaxTokens:          64,
			NormalSummaryMaxTokens:    64,
			EmergencySummaryMaxTokens: 16,
		},
		Events: output.NoopSink{},
		Prompt: prompt.AssemblyOptions{
			SessionDate:     sessionDate,
			PromptOverrides: config.ModelPrompts{System: "summary role integration system prompt"},
		},
	}

	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	firstReq, _, _, err := buildCompactionRequestWithMode(context.Background(), req, state, candidate, prompt.CompactionModeNormal, 64)
	if err != nil {
		t.Fatalf("first buildCompactionRequestWithMode() error = %v", err)
	}
	if messageContentsContain(firstReq.Messages, summaryText) {
		t.Fatal("first compaction request contains a summary before one exists")
	}
	assertCompactionSourceAccounting(t, firstReq)
	if got, want := firstReq.CompactionSourceMessages, 14; got != want {
		t.Fatalf("first CompactionSourceMessages = %d, want fixed count %d", got, want)
	}
	assertCompactionInstruction(t, firstReq, prompt.CompactionModeNormal, req.Prompt.PromptOverrides.Compaction)

	beforeProgressor := newTurnProgressor(req, prepareBasePrompt(req), nil)
	_, beforeNormal, _, err := beforeProgressor.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("pre-compaction prepareTurn() error = %v", err)
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Compact() Applied = false, want true")
	}

	stored := outcome.State.Conversation[0]
	if stored.Role != MessageRoleSummary || stored.Content != summaryText {
		t.Fatalf("stored summary = %+v, want role %q and exact text %q", stored, MessageRoleSummary, summaryText)
	}
	if !conversationHasRole(outcome.State.Conversation, MessageRoleTool) {
		t.Fatal("compacted state lost the retained tool tail")
	}

	progressor := newTurnProgressor(req, prepareBasePrompt(req), nil)
	_, normalReq, _, err := progressor.prepareTurn(context.Background(), outcome.State)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	nextCandidate, ok := selectCompactionCandidate(outcome.State.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() after compaction ok = false, want true")
	}
	secondReq, _, _, err := buildCompactionRequestWithMode(context.Background(), req, outcome.State, nextCandidate, prompt.CompactionModeNormal, 64)
	if err != nil {
		t.Fatalf("second buildCompactionRequestWithMode() error = %v", err)
	}

	prefix := secondReq.Messages[:len(secondReq.Messages)-1]
	if !reflect.DeepEqual(prefix, normalReq.Messages) {
		t.Fatal("recompaction prefix differs from the normal turn prefix")
	}
	assertSingleLeadingSystem(t, normalReq.Messages)
	assertSingleLeadingSystem(t, prefix)
	assertToolCallBeforeResult(t, normalReq.Messages, "call-1")
	assertToolCallBeforeResult(t, prefix, "call-1")

	wantSummary := summaryContextPrefix + summaryText + summaryContextSuffix
	if got := countProviderMessagesWithContent(normalReq.Messages, wantSummary); got != 1 {
		t.Fatalf("wrapped summary appears %d times in the normal turn, want 1", got)
	}
	summaryIndex := -1
	for i, message := range normalReq.Messages {
		if message.Content == wantSummary {
			if message.Role != provider.MessageRoleUser {
				t.Fatalf("summary role = %q, want user", message.Role)
			}
			summaryIndex = i
			break
		}
	}
	if summaryIndex < 0 {
		t.Fatal("normal turn lacks the wrapped summary")
	}
	if got, want := summaryIndex, 2; got != want {
		t.Fatalf("summary index = %d, want %d (static system plus fixed session date)", got, want)
	}
	if got, want := normalReq.Messages[1].Content, "Current date: 2026-11-12"; !strings.Contains(got, want) {
		t.Fatalf("session date message = %q, want it to contain %q", got, want)
	}
	beforePrefix := normalReq.Messages[:summaryIndex]
	if len(beforeNormal.Messages) < summaryIndex {
		t.Fatalf("pre-compaction request has %d messages, want at least %d", len(beforeNormal.Messages), summaryIndex)
	}
	if !reflect.DeepEqual(beforePrefix, beforeNormal.Messages[:summaryIndex]) {
		t.Fatal("prefix before the inserted summary differs from the pre-compaction request prefix")
	}

	repeatedProgressor := newTurnProgressor(req, prepareBasePrompt(req), nil)
	_, repeatedReq, _, err := repeatedProgressor.prepareTurn(context.Background(), outcome.State)
	if err != nil {
		t.Fatalf("repeated prepareTurn() error = %v", err)
	}
	assertSingleLeadingSystem(t, repeatedReq.Messages)
	if !reflect.DeepEqual(repeatedReq.Messages[:summaryIndex+1], normalReq.Messages[:summaryIndex+1]) {
		t.Fatal("repeated normal request prefix through the summary differs")
	}

	assertCompactionSourceAccounting(t, secondReq)
	if got, want := secondReq.CompactionSourceMessages, 9; got != want {
		t.Fatalf("recompaction CompactionSourceMessages = %d, want fixed count %d", got, want)
	}
	assertCompactionInstruction(t, secondReq, prompt.CompactionModeNormal, req.Prompt.PromptOverrides.Compaction)
	summaryOrdinal := -1
	ordinal := 0
	for _, message := range secondReq.Messages[:len(secondReq.Messages)-1] {
		if message.Role == provider.MessageRoleSystem {
			continue
		}
		if message.Content == wantSummary {
			summaryOrdinal = ordinal
		}
		ordinal++
	}
	if summaryOrdinal < 0 || summaryOrdinal >= secondReq.CompactionSourceMessages {
		t.Fatalf("summary ordinal = %d, want inside the %d source messages so the next compaction summarizes it", summaryOrdinal, secondReq.CompactionSourceMessages)
	}

	projected := ToProviderMessages(outcome.State.Conversation)
	if len(projected) == 0 || projected[0].Role != provider.MessageRoleUser {
		t.Fatal("stored summary did not project to a leading user message")
	}
	if got, want := projected[0].Content, wantSummary; got != want {
		t.Fatalf("projected summary = %q, want %q", got, want)
	}
	if stored.Role != MessageRoleSummary || stored.Content != summaryText {
		t.Fatal("projection changed the stored summary")
	}
}

// TestCompactionSummaryPromptSuffixAppliedOnce covers a compacted state whose
// only user message is the summary. The prompt suffix lands on that summary
// once, the tool pair stays ordered, and a repeated normal request is
// byte-identical.
func TestCompactionSummaryPromptSuffixAppliedOnce(t *testing.T) {
	t.Parallel()

	const summaryText = "suffix summary"
	const suffix = "Be concise."
	conversation := []Message{
		{Role: MessageRoleSummary, Content: summaryText},
		{Role: MessageRoleAssistant, ToolCalls: []ToolCall{{ID: "call-1", Name: "read"}}},
		{Role: MessageRoleTool, Content: "file body", ToolCallID: "call-1"},
	}
	state := RunState{Conversation: conversation, Lineage: newConversationLineage(conversation)}
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-suffix", PromptSuffix: suffix},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:         100000,
			MaxCompletionTokens: 128,
		},
		Events: output.NoopSink{},
		Prompt: prompt.AssemblyOptions{
			SessionDate:     prompt.NewSessionDate(time.Date(2026, 11, 12, 9, 30, 0, 0, time.UTC)),
			PromptOverrides: config.ModelPrompts{System: "summary role suffix system prompt"},
		},
	}

	progressor := newTurnProgressor(req, prepareBasePrompt(req), nil)
	_, first, _, err := progressor.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("prepareTurn() error = %v", err)
	}
	assertSingleLeadingSystem(t, first.Messages)
	assertToolCallBeforeResult(t, first.Messages, "call-1")

	wantSummary := summaryContextPrefix + summaryText + summaryContextSuffix
	summaryIndex := -1
	for i, message := range first.Messages {
		if !strings.HasPrefix(message.Content, wantSummary) {
			continue
		}
		summaryIndex = i
		if got, want := message.Role, provider.MessageRoleUser; got != want {
			t.Fatalf("summary role = %q, want %q", got, want)
		}
		if got, want := message.Content, wantSummary+" "+suffix; got != want {
			t.Fatalf("summary content with suffix = %q, want %q", got, want)
		}
	}
	if summaryIndex < 0 {
		t.Fatal("normal request lacks the wrapped summary")
	}
	if got := strings.Count(first.Messages[summaryIndex].Content, suffix); got != 1 {
		t.Fatalf("suffix appears %d times on the summary, want 1", got)
	}
	totalSuffix := 0
	for _, message := range first.Messages {
		totalSuffix += strings.Count(message.Content, suffix)
	}
	if totalSuffix != 1 {
		t.Fatalf("suffix appears %d times across the request, want 1 (only on the summary)", totalSuffix)
	}

	repeatedProgressor := newTurnProgressor(req, prepareBasePrompt(req), nil)
	_, repeated, _, err := repeatedProgressor.prepareTurn(context.Background(), state)
	if err != nil {
		t.Fatalf("repeated prepareTurn() error = %v", err)
	}
	assertSingleLeadingSystem(t, repeated.Messages)
	if !reflect.DeepEqual(repeated.Messages, first.Messages) {
		t.Fatal("repeated normal request differs from the first normal request")
	}
}

func assertCompactionInstruction(t *testing.T, req provider.ChatRequest, mode prompt.CompactionMode, override string) {
	t.Helper()
	if len(req.Messages) == 0 {
		t.Fatal("request has no messages")
	}
	last := req.Messages[len(req.Messages)-1]
	if got, want := last.Role, provider.MessageRoleUser; got != want {
		t.Fatalf("compaction instruction role = %q, want %q", got, want)
	}
	want := prompt.RenderConversationCompactionInstruction(override, mode)
	if last.Content != want {
		t.Fatalf("compaction instruction content = %q, want %q", last.Content, want)
	}
}

func assertToolCallBeforeResult(t *testing.T, messages []provider.Message, callID string) {
	t.Helper()
	callIndex, resultIndex := -1, -1
	for i, message := range messages {
		if message.Role == provider.MessageRoleAssistant {
			for _, call := range message.ToolCalls {
				if call.ID == callID {
					callIndex = i
				}
			}
		}
		if message.Role == provider.MessageRoleTool && message.ToolCallID == callID {
			resultIndex = i
		}
	}
	if callIndex < 0 {
		t.Fatalf("assistant tool call %q not found", callID)
	}
	if resultIndex < 0 {
		t.Fatalf("tool result %q not found", callID)
	}
	if callIndex+1 != resultIndex {
		t.Fatalf("tool call %q at index %d, want immediately before its result at index %d: %v", callID, callIndex, resultIndex, rolesOf(messages))
	}
}

func assertCompactionSourceAccounting(t *testing.T, req provider.ChatRequest) {
	t.Helper()
	if !req.CompactionSummary {
		t.Fatal("CompactionSummary = false, want true")
	}
	if got, want := req.CompactionSourceMessages, nonSystemCount(req.Messages[:len(req.Messages)-1]); got != want {
		t.Fatalf("CompactionSourceMessages = %d, want %d non-system messages", got, want)
	}
}

func nonSystemCount(messages []provider.Message) int {
	count := 0
	for _, message := range messages {
		if message.Role != provider.MessageRoleSystem {
			count++
		}
	}
	return count
}

// assertSingleLeadingSystem requires exactly one system message, at index 0.
func assertSingleLeadingSystem(t *testing.T, messages []provider.Message) {
	t.Helper()
	systemCount := 0
	for i, message := range messages {
		if message.Role != provider.MessageRoleSystem {
			continue
		}
		systemCount++
		if i != 0 {
			t.Fatalf("system message at index %d, want index 0: %v", i, rolesOf(messages))
		}
	}
	if systemCount != 1 {
		t.Fatalf("system messages = %d, want exactly 1: %v", systemCount, rolesOf(messages))
	}
}

func conversationHasRole(messages []Message, role MessageRole) bool {
	for _, message := range messages {
		if message.Role == role {
			return true
		}
	}
	return false
}
