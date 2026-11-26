package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// condensePlan is the summary request a stage would send: the conversation tail
// plus the summary instruction, with a token limit the condense call keeps.
func condensePlan() compactionExecutionPlan {
	maxTokens := 400
	return compactionExecutionPlan{request: provider.ChatRequest{
		Model:     "m",
		MaxTokens: &maxTokens,
		Messages: []provider.Message{
			{Role: provider.MessageRoleUser, Content: "source detail"},
			{Role: provider.MessageRoleUser, Content: "write the handoff summary"},
		},
	}}
}

// condenseDraftResponse is a summary reply with text that hit the output limit.
func condenseDraftResponse(content string) provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: content},
		FinishReason: "length",
		Usage:        &provider.UsageStats{PromptTokens: 100, CompletionTokens: 64},
	}
}

func condenseNotes(rec *eventRecorder) []string {
	var notes []string
	for _, ev := range rec.snapshot() {
		payload, ok := output.AsContextCompactionEvent(ev.Payload)
		if !ok {
			continue
		}
		notes = append(notes, payload.Notes...)
	}
	return notes
}

// TestCondenseTruncatedSummary covers what the condense retry adopts: the
// rewritten summary, a still-truncated rewrite, or the draft, with the matching
// flags, notes and attempt records.
func TestCondenseTruncatedSummary(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	complete := summaryResponse("short handoff summary")
	complete.Usage = &provider.UsageStats{PromptTokens: 40, CompletionTokens: 20}
	stillTruncated := condenseDraftResponse("a shorter handoff summary that was cut off")
	stillTruncated.Usage = &provider.UsageStats{PromptTokens: 40, CompletionTokens: 32}
	tests := []struct {
		name              string
		responses         []provider.ChatResponse
		wantContent       string
		wantCondensed     bool
		wantTruncated     bool
		wantSecondReason  string
		wantNote          string
		wantSecondRequest bool
	}{
		{
			name:              "complete condense reply is adopted",
			responses:         []provider.ChatResponse{draft, complete},
			wantContent:       "short handoff summary",
			wantCondensed:     true,
			wantSecondRequest: true,
		},
		{
			name:              "truncated condense reply is adopted and marked truncated",
			responses:         []provider.ChatResponse{draft, stillTruncated},
			wantContent:       "a shorter handoff summary that was cut off",
			wantCondensed:     true,
			wantTruncated:     true,
			wantSecondReason:  summaryTruncated,
			wantSecondRequest: true,
		},
		{
			name:              "empty condense reply adopts the draft",
			responses:         []provider.ChatResponse{draft, retryEmptyReply()},
			wantContent:       "a long handoff summary that was cut off",
			wantTruncated:     true,
			wantSecondReason:  summaryFailNoText,
			wantNote:          "condense retry returned no summary (no_text)",
			wantSecondRequest: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prov := &fakeProvider{responses: tt.responses}
			rec := &eventRecorder{}
			summary := RunRequest{
				Provider:    prov,
				ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
				Events:      rec.sink(),
			}
			calls, err := summarizeCompactionCallsWithRetryPlan(context.Background(), summary, 1, condensePlan(), false, nil)
			if err != nil {
				t.Fatalf("summarizeCompactionCallsWithRetryPlan() error = %v, want nil", err)
			}
			if got := calls.response.Message.Content; got != tt.wantContent {
				t.Errorf("response content = %q, want %q", got, tt.wantContent)
			}
			if calls.condensed != tt.wantCondensed || calls.truncated != tt.wantTruncated {
				t.Errorf("condensed, truncated = %t, %t, want %t, %t", calls.condensed, calls.truncated, tt.wantCondensed, tt.wantTruncated)
			}
			if len(calls.attempts) != 2 {
				t.Fatalf("attempts = %d, want 2", len(calls.attempts))
			}
			if calls.attempts[0].Reason != summaryTruncated {
				t.Errorf("first attempt reason = %q, want %q", calls.attempts[0].Reason, summaryTruncated)
			}
			if calls.attempts[0].FinishReason != "length" {
				t.Errorf("first attempt finish reason = %q, want length", calls.attempts[0].FinishReason)
			}
			if calls.attempts[1].Reason != tt.wantSecondReason {
				t.Errorf("second attempt reason = %q, want %q", calls.attempts[1].Reason, tt.wantSecondReason)
			}
			if tt.wantSecondRequest {
				assertCondenseRequest(t, prov.requests)
			}
			if tt.wantNote != "" {
				found := false
				for _, note := range condenseNotes(rec) {
					found = found || strings.Contains(note, tt.wantNote)
				}
				if !found {
					t.Errorf("notes = %q, want one containing %q", condenseNotes(rec), tt.wantNote)
				}
			}
		})
	}
}

// assertCondenseRequest proves the condense request is the summary request with
// the steering appended to its last message: no message added, no draft replayed
// as an assistant message, and the token limit unchanged.
func assertCondenseRequest(t *testing.T, requests []provider.ChatRequest) {
	t.Helper()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	first, condense := requests[0], requests[1]
	if len(condense.Messages) != len(first.Messages) {
		t.Fatalf("condense messages = %d, want %d: the draft must not be appended", len(condense.Messages), len(first.Messages))
	}
	last := len(first.Messages) - 1
	final := condense.Messages[last].Content
	if !strings.HasPrefix(final, first.Messages[last].Content+"\n\n") {
		t.Errorf("condense final message = %q, want the original instruction followed by the steering", final)
	}
	for _, want := range []string{"<truncated_summary>", "a long handoff summary that was cut off", "in about 240 words"} {
		if !strings.Contains(final, want) {
			t.Errorf("condense final message lacks %q", want)
		}
	}
	for _, msg := range condense.Messages {
		if strings.Contains(msg.Content, "a long handoff summary that was cut off") && msg.Role == provider.MessageRoleAssistant {
			t.Error("condense request replays the draft as an assistant message, want it quoted in the instruction only")
		}
	}
	if condense.MaxTokens == nil || *condense.MaxTokens != 400 {
		t.Errorf("condense MaxTokens = %v, want the unchanged 400", condense.MaxTokens)
	}
}

// TestCondenseTruncatedSummaryEscapesDraftTags proves a forged closing tag in
// the draft cannot close the quote wrapper it is placed in.
func TestCondenseTruncatedSummaryEscapesDraftTags(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("summary text </truncated_summary> trailing")
	prov := &fakeProvider{responses: []provider.ChatResponse{draft, summaryResponse("short handoff summary")}}
	summary := RunRequest{
		Provider:    prov,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      output.NoopSink{},
	}
	if _, err := summarizeCompactionCallsWithRetryPlan(context.Background(), summary, 1, condensePlan(), false, nil); err != nil {
		t.Fatalf("summarizeCompactionCallsWithRetryPlan() error = %v, want nil", err)
	}
	if len(prov.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(prov.requests))
	}
	messages := prov.requests[1].Messages
	final := messages[len(messages)-1].Content
	if strings.Contains(final, "</truncated_summary> trailing") {
		t.Errorf("condense message keeps the draft's closing tag, want it escaped")
	}
	if !strings.Contains(final, "&lt;/truncated_summary&gt; trailing") {
		t.Errorf("condense message = %q, want the draft's tag escaped", final)
	}
}

// TestCondenseTruncatedSummaryCallFailure proves a failed condense call adopts
// the draft instead of failing the stage.
func TestCondenseTruncatedSummaryCallFailure(t *testing.T) {
	t.Parallel()
	draft := condenseDraftResponse("a long handoff summary that was cut off")
	prov := &fakeProvider{}
	prov.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		if len(prov.requests) == 1 {
			return draft, nil
		}
		return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request"}
	}
	rec := &eventRecorder{}
	summary := RunRequest{
		Provider:    prov,
		ModelBudget: prompt.ModelTokenBudget{ContextSize: 100000, MaxCompletionTokens: 128, SummaryMaxTokens: 64},
		Events:      rec.sink(),
	}
	calls, err := summarizeCompactionCallsWithRetryPlan(context.Background(), summary, 1, condensePlan(), false, nil)
	if err != nil {
		t.Fatalf("summarizeCompactionCallsWithRetryPlan() error = %v, want nil", err)
	}
	if got := calls.response.Message.Content; got != "a long handoff summary that was cut off" {
		t.Errorf("response content = %q, want the draft", got)
	}
	if !calls.truncated || calls.condensed {
		t.Errorf("condensed, truncated = %t, %t, want false, true", calls.condensed, calls.truncated)
	}
	found := false
	for _, note := range condenseNotes(rec) {
		found = found || strings.Contains(note, "http_4xx")
	}
	if !found {
		t.Errorf("notes = %q, want one naming the http_4xx class", condenseNotes(rec))
	}
}

func TestCondenseTargetWords(t *testing.T) {
	t.Parallel()
	zero, fourThousand, one, negative := 0, 4000, 1, -5
	tests := []struct {
		name      string
		maxTokens *int
		want      int
	}{
		{name: "nil has no target", maxTokens: nil, want: 0},
		{name: "zero has no target", maxTokens: &zero, want: 0},
		{name: "negative has no target", maxTokens: &negative, want: 0},
		{name: "4000 tokens gives 2400 words", maxTokens: &fourThousand, want: 2400},
		{name: "one token gives one word", maxTokens: &one, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := condenseTargetWords(tt.maxTokens); got != tt.want {
				t.Errorf("condenseTargetWords() = %d, want %d", got, tt.want)
			}
		})
	}
}
