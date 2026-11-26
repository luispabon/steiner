package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func retryEmptyReply() provider.ChatResponse {
	return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "stop"}
}

func retryToolCallReply(calls ...provider.ToolCall) provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: calls},
		FinishReason: "tool_calls",
		Usage:        &provider.UsageStats{PromptTokens: 50, CompletionTokens: 10},
	}
}

func retryReadCall(path string) provider.ToolCall {
	return provider.ToolCall{Name: "read", RawArguments: `{"path":"` + path + `"}`}
}

// retryRun is a main-only compaction over the fallback fixture, with the main
// provider answering responses in order.
func retryRun(responses ...provider.ChatResponse) *fallbackOperationRun {
	r := newFallbackOperationRun(fallbackOperationNow)
	r.req.CompactionFallback = CompactionFallback{}
	r.main.responses = responses
	return r
}

func retryFinalContent(req provider.ChatRequest) string {
	return req.Messages[len(req.Messages)-1].Content
}

// TestCompactionSummaryRetryLadder proves which replies are retried and how many
// main calls one stage makes: at most three, and one for a length stop.
func TestCompactionSummaryRetryLadder(t *testing.T) {
	t.Parallel()
	text := summaryResponse("handoff summary")
	textWithCalls := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "handoff summary", ToolCalls: []provider.ToolCall{retryReadCall("a.go")}},
		FinishReason: "tool_calls",
	}
	length := provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "length"}
	lengthWithCalls := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{retryReadCall("a.go")}},
		FinishReason: "length",
	}
	tests := []struct {
		name          string
		responses     []provider.ChatResponse
		wantRequests  int
		wantSummary   string
		wantReason    string
		wantAttempts  int
		wantToolCalls int
	}{
		{name: "empty then text recovers", responses: []provider.ChatResponse{retryEmptyReply(), text}, wantRequests: 2, wantSummary: "handoff summary"},
		{name: "whitespace only is empty", responses: []provider.ChatResponse{summaryResponse(" \n\t "), text}, wantRequests: 2, wantSummary: "handoff summary"},
		{name: "tool calls then text recovers", responses: []provider.ChatResponse{retryToolCallReply(retryReadCall("a.go")), text}, wantRequests: 2, wantSummary: "handoff summary"},
		{name: "empty twice then text recovers on the final attempt", responses: []provider.ChatResponse{retryEmptyReply(), retryEmptyReply(), text}, wantRequests: 3, wantSummary: "handoff summary"},
		{name: "text with tool calls is accepted without retry", responses: []provider.ChatResponse{textWithCalls}, wantRequests: 1, wantSummary: "handoff summary"},
		{name: "all empty fails after three calls", responses: []provider.ChatResponse{retryEmptyReply(), retryEmptyReply(), retryEmptyReply()}, wantRequests: 3, wantReason: "no_text", wantAttempts: 3},
		{name: "tool calls only fail after three calls", responses: []provider.ChatResponse{retryToolCallReply(retryReadCall("a.go")), retryToolCallReply(retryReadCall("b.go")), retryToolCallReply(retryReadCall("c.go"))}, wantRequests: 3, wantReason: "tool_calls", wantAttempts: 3, wantToolCalls: 1},
		{name: "length is not retried", responses: []provider.ChatResponse{length}, wantRequests: 1, wantReason: "length", wantAttempts: 1},
		{name: "length with tool calls classifies as length", responses: []provider.ChatResponse{lengthWithCalls}, wantRequests: 1, wantReason: "length", wantAttempts: 1, wantToolCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := retryRun(tt.responses...)
			outcome, err := r.compact(context.Background())
			if got := len(r.main.requests); got != tt.wantRequests {
				t.Errorf("main requests = %d, want %d", got, tt.wantRequests)
			}
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("compact() error = %v, want nil", err)
				}
				if !outcome.Applied || !strings.Contains(outcome.SummaryText, tt.wantSummary) {
					t.Errorf("Applied = %t, SummaryText = %q, want applied with %q", outcome.Applied, outcome.SummaryText, tt.wantSummary)
				}
				return
			}
			var summaryErr *compactionSummaryError
			if !errors.As(err, &summaryErr) {
				t.Fatalf("compact() error = %v, want *compactionSummaryError", err)
			}
			if summaryErr.Reason != tt.wantReason || summaryErr.Attempts != tt.wantAttempts || summaryErr.ToolCalls != tt.wantToolCalls {
				t.Errorf("error reason, attempts, tool calls = %q, %d, %d, want %q, %d, %d", summaryErr.Reason, summaryErr.Attempts, summaryErr.ToolCalls, tt.wantReason, tt.wantAttempts, tt.wantToolCalls)
			}
			if outcome.Applied {
				t.Error("Applied = true, want false for a failed summary")
			}
		})
	}
}

// TestCompactionSummaryRetryRequestShape proves a retry changes only the final
// instruction, drops the failed reply, and sums the usage of every call.
func TestCompactionSummaryRetryRequestShape(t *testing.T) {
	t.Parallel()
	calls := retryToolCallReply(retryReadCall("a.go"))
	text := summaryResponse("handoff summary")
	text.Usage = &provider.UsageStats{PromptTokens: 40, CompletionTokens: 20}
	r := retryRun(calls, text)
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if len(r.main.requests) != 2 {
		t.Fatalf("main requests = %d, want 2", len(r.main.requests))
	}
	first, retry := r.main.requests[0], r.main.requests[1]

	firstHead, retryHead := first, retry
	firstHead.Messages, retryHead.Messages = nil, nil
	firstV, retryV := reflect.ValueOf(firstHead), reflect.ValueOf(retryHead)
	for i := range firstV.NumField() {
		// The raw estimate is per-call estimator metadata, never wire payload.
		if name := firstV.Type().Field(i).Name; name != "RawPromptTokenEstimate" && !reflect.DeepEqual(firstV.Field(i).Interface(), retryV.Field(i).Interface()) {
			t.Errorf("retry field %s differs from the first call, want it identical", name)
		}
	}

	last := len(first.Messages) - 1
	if len(retry.Messages) != len(first.Messages) {
		t.Fatalf("retry messages = %d, want %d: the failed reply must not be appended", len(retry.Messages), len(first.Messages))
	}
	for i := 0; i < last; i++ {
		if !reflect.DeepEqual(retry.Messages[i], first.Messages[i]) {
			t.Fatalf("message %d differs in the retry, want the shared prefix unchanged", i)
		}
	}
	final := retry.Messages[last].Content
	if !strings.HasPrefix(final, first.Messages[last].Content+"\n\n") || !strings.Contains(final, "contained tool calls and no text") {
		t.Errorf("retry final message = %q, want the original instruction followed by the steering", final)
	}
	if strings.Contains(retryFinalContent(first), "This is a retry") {
		t.Error("first call carries the retry steering, want it only in the retry")
	}
	for _, msg := range retry.Messages {
		if len(msg.ToolCalls) > 0 {
			t.Error("retry carries the failed tool-call reply, want it absent")
		}
	}
	if outcome.Usage == nil || outcome.Usage.PromptTokens != 90 || outcome.Usage.CompletionTokens != 30 {
		t.Errorf("Usage = %+v, want prompt 90 and completion 30 summed over both calls", outcome.Usage)
	}
}

// TestCompactionSummaryRetryFinalAttemptQuotesToolCalls proves the final retry
// names the tool calls of the reply before it, and only that reply.
func TestCompactionSummaryRetryFinalAttemptQuotesToolCalls(t *testing.T) {
	t.Parallel()
	r := retryRun(
		retryToolCallReply(retryReadCall("a.go")),
		retryToolCallReply(provider.ToolCall{Name: "grep", RawArguments: `{"pattern":"TODO"}`}),
		summaryResponse("handoff summary"),
	)
	if _, err := r.compact(context.Background()); err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if len(r.main.requests) != 3 {
		t.Fatalf("main requests = %d, want 3", len(r.main.requests))
	}
	if second := retryFinalContent(r.main.requests[1]); strings.Contains(second, "This is the final attempt.") {
		t.Error("second retry is marked final, want only the third call final")
	}
	final := retryFinalContent(r.main.requests[2])
	for _, want := range []string{"This is the final attempt.", "- grep {\"pattern\":\"TODO\"}", "begin directly with the summary"} {
		if !strings.Contains(final, want) {
			t.Errorf("final retry message lacks %q", want)
		}
	}
	if strings.Contains(final, "a.go") {
		t.Error("final retry quotes the first reply's tool calls, want only the reply before it")
	}
}

// TestQuoteAttemptedToolCalls proves at most maxQuotedToolCalls calls are quoted,
// with arguments cut to maxQuotedArgChars.
func TestQuoteAttemptedToolCalls(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 500)
	got := quoteAttemptedToolCalls([]provider.ToolCall{
		{Name: "a", RawArguments: "{}"},
		{Name: "b", RawArguments: long},
		{Name: "c", Arguments: map[string]any{"k": "v"}},
		{Name: "d", RawArguments: "{}"},
	})
	if len(got) != maxQuotedToolCalls {
		t.Fatalf("quoted = %d calls, want %d", len(got), maxQuotedToolCalls)
	}
	if got[0] != "a {}" || got[2] != `c {"k":"v"}` {
		t.Errorf("quoted = %q, want the name and arguments of each call", got)
	}
	if !strings.HasPrefix(got[1], "b xxx") || len(got[1]) > len("b ")+maxQuotedArgChars+3 {
		t.Errorf("long arguments quoted as %d bytes, want cut to about %d", len(got[1]), maxQuotedArgChars)
	}
}

// TestCompactionSummaryRetryBackendState proves the retry sends the backend state
// until a call retires it, and that the cleared flag reaches the outcome, also on
// failure.
func TestCompactionSummaryRetryBackendState(t *testing.T) {
	t.Parallel()
	retiredEmpty := retryEmptyReply()
	retiredEmpty.ClearBackendState = true
	tests := []struct {
		name             string
		responses        []provider.ChatResponse
		wantErr          bool
		wantCleared      bool
		wantBackendState []bool
	}{
		{
			name:             "no retirement keeps the state",
			responses:        []provider.ChatResponse{retryEmptyReply(), summaryResponse("handoff summary")},
			wantBackendState: []bool{true, true},
		},
		{
			name:             "retirement on the first call drops the state for the retry",
			responses:        []provider.ChatResponse{retiredEmpty, summaryResponse("handoff summary")},
			wantCleared:      true,
			wantBackendState: []bool{true, false},
		},
		{
			name:             "retirement on a failing call is reported",
			responses:        []provider.ChatResponse{retryEmptyReply(), retryEmptyReply(), retiredEmpty},
			wantErr:          true,
			wantCleared:      true,
			wantBackendState: []bool{true, true, true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := retryRun(tt.responses...)
			gens := r.state.Lineage.Generations
			gens[len(gens)-1].BackendState = []byte(`{"transcript":"main"}`)
			outcome, err := r.compact(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("compact() error = %v, want error %t", err, tt.wantErr)
			}
			if outcome.BackendStateCleared != tt.wantCleared {
				t.Errorf("BackendStateCleared = %t, want %t", outcome.BackendStateCleared, tt.wantCleared)
			}
			if len(r.main.requests) != len(tt.wantBackendState) {
				t.Fatalf("main requests = %d, want %d", len(r.main.requests), len(tt.wantBackendState))
			}
			for i, want := range tt.wantBackendState {
				if got := r.main.requests[i].BackendState != nil; got != want {
					t.Errorf("call %d carries backend state = %t, want %t", i+1, got, want)
				}
			}
		})
	}
}

// TestCompactionSummaryRetryDoesNotFit proves a retry that the budget would not
// send fails the stage with retry_does_not_fit after its first call, without
// stripping blocks or sending anything.
func TestCompactionSummaryRetryDoesNotFit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	req := provider.ChatRequest{Model: "m", Messages: []provider.Message{
		{Role: provider.MessageRoleUser, Content: strings.Repeat("source ", 200)},
		{Role: provider.MessageRoleUser, Content: "instruction"},
	}}
	budget := func(size int) prompt.ModelTokenBudget {
		return prompt.ModelTokenBudget{ContextSize: size, MaxCompletionTokens: 128, SummaryMaxTokens: 64}
	}
	fits := func(size int) bool {
		fit, err := budget(size).FitCompactionRequest(ctx, req)
		if err != nil {
			t.Fatalf("FitCompactionRequest() error = %v", err)
		}
		return fit.Fits
	}
	// Fit is monotone in the context size, so the smallest size that fits the first
	// call is the tightest one: a retry with its steering is too large for it.
	lo, hi := 1, 1<<20
	for lo < hi {
		if mid := (lo + hi) / 2; fits(mid) {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	if !fits(lo) {
		t.Fatal("no context size fits the first call")
	}
	prov := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("never sent")}}
	summary := RunRequest{Provider: prov, ModelBudget: budget(lo), Events: output.NoopSink{}}
	_, err := summarizeCompactionCallsWithRetryPlan(ctx, summary, 1, compactionExecutionPlan{request: req}, false, nil)
	var summaryErr *compactionSummaryError
	if !errors.As(err, &summaryErr) || summaryErr.Reason != summaryFailRetryNoFit || summaryErr.Attempts != 1 {
		t.Fatalf("error = %v, want retry_does_not_fit after 1 attempt", err)
	}
	if got := len(prov.requests); got != 1 {
		t.Errorf("provider calls = %d, want 1: the retry must not be sent", got)
	}
}

// TestCompactionSummaryRetryLogTagsAttempts proves each attempt writes its own
// tagged request to the compaction log.
func TestCompactionSummaryRetryLogTagsAttempts(t *testing.T) {
	t.Parallel()
	r := retryRun(retryEmptyReply(), summaryResponse("handoff summary"))
	logPath := filepath.Join(t.TempDir(), "compaction.log")
	r.req.CompactionLogPath = logPath
	if _, err := r.compact(context.Background()); err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read compaction log: %v", err)
	}
	for _, want := range []string{"Attempt: 1\n", "Attempt: 2\n"} {
		if n := strings.Count(string(data), want); n != 1 {
			t.Errorf("log has %d %q lines, want 1", n, want)
		}
	}
}

// TestCompactionSummaryRetryNotes proves a recovered retry and tool calls that
// came with text are reported as notes, and the stage still applies.
func TestCompactionSummaryRetryNotes(t *testing.T) {
	t.Parallel()
	textWithCalls := provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "handoff summary", ToolCalls: []provider.ToolCall{retryReadCall("a.go")}},
		FinishReason: "tool_calls",
	}
	reasoningEmpty := provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, ReasoningContent: "thinking"}, FinishReason: "stop", Usage: &provider.UsageStats{CompletionTokens: 0}}
	tests := []struct {
		name      string
		responses []provider.ChatResponse
		wantNote  string
	}{
		{name: "recovered retry", responses: []provider.ChatResponse{retryEmptyReply(), summaryResponse("handoff summary")}, wantNote: "recovered on attempt 2 of 3"},
		{name: "text with tool calls", responses: []provider.ChatResponse{textWithCalls}, wantNote: "carried 1 tool call(s) with its text"},
		{name: "recovered retry lists failed attempt", responses: []provider.ChatResponse{reasoningEmpty, summaryResponse("handoff summary")}, wantNote: "failed summary attempt: attempt=1 finish_reason=stop completion_tokens=0 tool_calls=0 reasoning_content=true reason=no_text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := retryRun(tt.responses...)
			var rec eventRecorder
			r.req.Events = rec.sink()
			outcome, err := r.compact(context.Background())
			if err != nil || !outcome.Applied {
				t.Fatalf("compact() = applied %t, error %v, want applied with no error", outcome.Applied, err)
			}
			found := false
			for _, ev := range rec.snapshot() {
				payload, ok := output.AsContextCompactionEvent(ev.Payload)
				if !ok {
					continue
				}
				for _, note := range payload.Notes {
					found = found || strings.Contains(note, tt.wantNote)
				}
			}
			if !found {
				t.Errorf("no note contains %q", tt.wantNote)
			}
		})
	}
}

// TestCompactionFallbackEmptySummaryReruns proves an empty fallback summary gets
// no retry: the target makes one call, then main runs the operation with its own
// retries.
func TestCompactionFallbackEmptySummaryReruns(t *testing.T) {
	t.Parallel()
	empty := retryEmptyReply()
	tests := []struct {
		name          string
		mainResponses []provider.ChatResponse
		wantMain      int
		wantReason    string
	}{
		{name: "main recovers", mainResponses: []provider.ChatResponse{summaryResponse("handoff summary")}, wantMain: 1},
		{name: "main empty three times", mainResponses: []provider.ChatResponse{empty, empty, empty}, wantMain: 3, wantReason: "no_text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newFallbackOperationRun(fallbackOperationNow)
			r.target.responses = []provider.ChatResponse{empty}
			r.main.responses = tt.mainResponses
			outcome, err := r.compact(context.Background())
			if got := len(r.target.requests); got != 1 {
				t.Errorf("target requests = %d, want 1", got)
			}
			if got := len(r.main.requests); got != tt.wantMain {
				t.Errorf("main requests = %d, want %d", got, tt.wantMain)
			}
			if tt.wantReason == "" {
				if err != nil || !outcome.Applied {
					t.Fatalf("compact() = applied %t, error %v, want the main rerun to apply", outcome.Applied, err)
				}
				return
			}
			var summaryErr *compactionSummaryError
			if !errors.As(err, &summaryErr) || summaryErr.Reason != tt.wantReason || summaryErr.Attempts != 3 {
				t.Fatalf("error = %v, want %q after 3 attempts on main", err, tt.wantReason)
			}
			if outcome.Applied {
				t.Error("Applied = true, want false when main also fails")
			}
		})
	}
}
