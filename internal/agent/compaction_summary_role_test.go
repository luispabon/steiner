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
	if strings.Contains(messagesText(firstReq.Messages), summaryText) {
		t.Fatal("first compaction request contains a summary before one exists")
	}
	assertCompactionSourceAccounting(t, firstReq)

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
	assertSystemMessagesFirst(t, normalReq.Messages)
	assertSingleLeadingSystem(t, normalReq.Messages)
	assertSingleLeadingSystem(t, prefix)

	wantSummary := summaryContextPrefix + summaryText + summaryContextSuffix
	if got := countProviderContent(normalReq.Messages, wantSummary); got != 1 {
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

func assertSystemMessagesFirst(t *testing.T, messages []provider.Message) {
	t.Helper()
	seenNonSystem := false
	for _, message := range messages {
		if message.Role == provider.MessageRoleSystem {
			if seenNonSystem {
				t.Fatalf("system message follows a non-system message: %v", rolesOf(messages))
			}
			continue
		}
		seenNonSystem = true
	}
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

func countProviderContent(messages []provider.Message, content string) int {
	count := 0
	for _, message := range messages {
		if message.Content == content {
			count++
		}
	}
	return count
}

func conversationHasRole(messages []Message, role MessageRole) bool {
	for _, message := range messages {
		if message.Role == role {
			return true
		}
	}
	return false
}

func messagesText(messages []provider.Message) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.Content)
	}
	return b.String()
}
