package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// fallbackOperationNow is the fixed clock time of the fallback operation fixtures.
var fallbackOperationNow = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

// fallbackOperationAfter is the idle threshold after which the main cache is cold.
const fallbackOperationAfter = 5 * time.Minute

// fallbackOperationMessages is the conversation of
// TestSummarizeCompactorRunsEmergencyStageWhenNormalCompactionLeavesPromptTooDense.
func fallbackOperationMessages() []Message {
	return []Message{
		{Role: MessageRoleUser, Content: strings.Repeat("turn 1 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 1 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 2 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 2 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 3 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 3 assistant detail ", 4)},
		{Role: MessageRoleUser, Content: strings.Repeat("turn 4 user detail ", 4)},
		{Role: MessageRoleAssistant, Content: strings.Repeat("turn 4 assistant detail ", 4)},
	}
}

// fallbackOperationResponses returns the normal and emergency summary responses
// from that test. The normal summary overflows the main budget, so the operation
// needs a second, emergency stage.
func fallbackOperationResponses() []provider.ChatResponse {
	return []provider.ChatResponse{
		{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: strings.Repeat("normal summary detail ", 1200)},
			FinishReason: "stop",
			Usage:        &provider.UsageStats{PromptTokens: 50, CompletionTokens: 10},
		},
		{
			Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: "emergency handoff summary"},
			FinishReason: "stop",
			Usage:        &provider.UsageStats{PromptTokens: 50, CompletionTokens: 10},
		},
	}
}

// fallbackOperationRun is one two-stage compaction over the emergency fixture
// with a fallback target. Tests change its fields before compacting.
type fallbackOperationRun struct {
	main     *fakeProvider
	target   *fakeProvider
	recorder *testRecorder
	req      RunRequest
	state    RunState
	// resolved and resolveErr are what the fallback Resolve returns.
	resolved   CompactionTarget
	resolveErr error
}

func newFallbackOperationRun(now time.Time) *fallbackOperationRun {
	r := &fallbackOperationRun{
		main:     &fakeProvider{responses: fallbackOperationResponses()},
		target:   &fakeProvider{responses: fallbackOperationResponses()},
		recorder: &testRecorder{},
	}
	r.resolved = compactionFallbackSummaryTarget(r.target, prompt.ModelTokenBudget{ContextSize: 100000, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 16})
	msgs := fallbackOperationMessages()
	r.state = RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	r.req = RunRequest{
		Provider:      r.main,
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
		ModelBudget: prompt.ModelTokenBudget{
			ContextSize:               6000,
			MaxCompletionTokens:       128,
			SummaryMaxTokens:          64,
			NormalSummaryMaxTokens:    64,
			EmergencySummaryMaxTokens: 16,
		},
		UsageRecorder: r.recorder,
		Events:        output.SinkFunc(func(output.Event) {}),
		Clock:         fixedCompactionClock{now: now},
		CompactionFallback: CompactionFallback{
			After: fallbackOperationAfter,
			Ref:   "anthropic/claude-small",
			Resolve: func(context.Context) (CompactionTarget, error) {
				return r.resolved, r.resolveErr
			},
		},
	}
	return r
}

// compact runs one compaction of the run's state through summarizeCompactor.
func (r *fallbackOperationRun) compact(ctx context.Context) (CompactionOutcome, error) {
	candidate, ok := selectCompactionCandidate(r.state.Lineage, nil)
	if !ok {
		return CompactionOutcome{}, errors.New("selectCompactionCandidate() ok = false, want true")
	}
	return summarizeCompactor{}.Compact(ctx, r.req, r.state, 5, candidate, "")
}

func TestCompactionFallbackOperation(t *testing.T) {
	t.Parallel()
	disabled := newFallbackOperationRun(fallbackOperationNow)
	disabled.req.CompactionFallback = CompactionFallback{}
	if _, err := disabled.compact(context.Background()); err != nil {
		t.Fatalf("disabled compact() error = %v", err)
	}
	golden := disabled.main.requests
	if len(golden) != 2 {
		t.Fatalf("golden main requests = %d, want 2 (normal, emergency)", len(golden))
	}

	tests := []struct {
		name               string
		setup              func(r *fallbackOperationRun)
		wantMain           int
		wantTarget         int
		wantStatus         string
		wantModel          string
		wantReason         string
		wantReasonContains string
		wantFirstUsage     string
	}{
		{
			name:     "fallback disabled runs main",
			setup:    func(r *fallbackOperationRun) { r.req.CompactionFallback = CompactionFallback{} },
			wantMain: 2,
		},
		{
			name: "warm main cache runs main",
			setup: func(r *fallbackOperationRun) {
				r.state.LastMainRequest = LastMainRequest{At: fallbackOperationNow.Add(-time.Minute)}
			},
			wantMain: 2,
		},
		{
			name:           "cold cache without main request runs target",
			wantTarget:     2,
			wantStatus:     output.CompactionFallbackUsed,
			wantModel:      "anthropic/claude-small",
			wantReason:     "no recent request",
			wantFirstUsage: "anthropic/claude-small",
		},
		{
			name: "cold cache after idle time runs target",
			setup: func(r *fallbackOperationRun) {
				r.state.LastMainRequest = LastMainRequest{At: fallbackOperationNow.Add(-10 * time.Minute)}
			},
			wantTarget:     2,
			wantStatus:     output.CompactionFallbackUsed,
			wantModel:      "anthropic/claude-small",
			wantReason:     "idle 10 > 5",
			wantFirstUsage: "anthropic/claude-small",
		},
		{
			name: "target too small for summary reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolved.ModelBudget = prompt.ModelTokenBudget{ContextSize: 1, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 16}
			},
			wantMain:   2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  "openai/gpt-main",
			wantReason: "context too large for anthropic/claude-small",
		},
		{
			name: "target emergency stage failure reruns on main",
			setup: func(r *fallbackOperationRun) {
				calls := 0
				r.target.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
					calls++
					if calls == 1 {
						return fallbackOperationResponses()[0], nil
					}
					return provider.ChatResponse{}, errors.New("target emergency failed")
				}
			},
			wantMain:           2,
			wantTarget:         2,
			wantStatus:         output.CompactionFallbackSkipped,
			wantModel:          "openai/gpt-main",
			wantReasonContains: "fallback failed: ",
			wantFirstUsage:     "anthropic/claude-small",
		},
		{
			name: "resolve error reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolveErr = errors.New("resolve failed")
			},
			wantMain:   2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  "openai/gpt-main",
			wantReason: "fallback failed: resolve compaction fallback: resolve failed",
		},
		{
			name: "target without provider reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolved.Provider = nil
			},
			wantMain:   2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  "openai/gpt-main",
			wantReason: "fallback failed: compaction fallback target has no provider",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newFallbackOperationRun(fallbackOperationNow)
			if tt.setup != nil {
				tt.setup(r)
			}
			outcome, err := r.compact(context.Background())
			if err != nil {
				t.Fatalf("compact() error = %v", err)
			}
			if !outcome.Applied {
				t.Error("Applied = false, want true")
			}
			if outcome.StageCount != 2 {
				t.Errorf("StageCount = %d, want 2", outcome.StageCount)
			}
			if got := len(r.main.requests); got != tt.wantMain {
				t.Errorf("main requests = %d, want %d", got, tt.wantMain)
			}
			if got := len(r.target.requests); got != tt.wantTarget {
				t.Errorf("target requests = %d, want %d", got, tt.wantTarget)
			}
			if tt.wantMain > 0 && !reflect.DeepEqual(r.main.requests, golden) {
				t.Error("main requests differ from the fallback-disabled run, want identical main requests")
			}
			if outcome.CompactionModel != tt.wantModel {
				t.Errorf("CompactionModel = %q, want %q", outcome.CompactionModel, tt.wantModel)
			}
			if outcome.FallbackStatus != tt.wantStatus {
				t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, tt.wantStatus)
			}
			switch {
			case tt.wantReasonContains != "":
				if !strings.Contains(outcome.FallbackReason, tt.wantReasonContains) {
					t.Errorf("FallbackReason = %q, want it to contain %q", outcome.FallbackReason, tt.wantReasonContains)
				}
			case outcome.FallbackReason != tt.wantReason:
				t.Errorf("FallbackReason = %q, want %q", outcome.FallbackReason, tt.wantReason)
			}
			if tt.wantFirstUsage != "" {
				if len(r.recorder.obs) == 0 {
					t.Fatal("no usage recorded, want the target stage usage first")
				}
				obs := r.recorder.obs[0]
				if got := obs.ProviderAlias + "/" + obs.BackendModelID; got != tt.wantFirstUsage {
					t.Errorf("first usage attributed to %q, want %q", got, tt.wantFirstUsage)
				}
			}
		})
	}
}

// TestCompactionFallbackOperationDoesNotRerunOnCancellation checks that a parent
// cancellation or turn timeout during the target attempt returns the error
// without running the operation on main.
func TestCompactionFallbackOperationDoesNotRerunOnCancellation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		cause error
	}{
		{name: "parent cancellation", cause: context.Canceled},
		{name: "turn timeout", cause: errTurnTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			r := newFallbackOperationRun(fallbackOperationNow)
			r.target.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
				cancel(tt.cause)
				return provider.ChatResponse{}, errors.New("target interrupted")
			}
			if _, err := r.compact(ctx); err == nil {
				t.Fatal("compact() error = nil, want the interrupted target error")
			}
			if got := len(r.main.requests); got != 0 {
				t.Errorf("main requests = %d, want 0 (no rerun after interruption)", got)
			}
			if got := len(r.target.requests); got != 1 {
				t.Errorf("target requests = %d, want 1", got)
			}
		})
	}
}

// fallbackCompactionPayload returns the single completed compaction diagnostic in events.
func fallbackCompactionPayload(t *testing.T, events []output.Event) output.ContextCompactionEvent {
	t.Helper()
	var payloads []output.ContextCompactionEvent
	for _, event := range events {
		if event.Type != output.EventTypeContextDiagnostics || output.ContextDiagnosticKind(event.Payload) != "compaction" {
			continue
		}
		payload, ok := output.AsContextCompactionEvent(event.Payload)
		if !ok {
			t.Fatalf("diagnostic payload type = %T, want output.ContextCompactionEvent", event.Payload)
		}
		payloads = append(payloads, payload)
	}
	if len(payloads) != 1 {
		t.Fatalf("compaction diagnostics = %d, want 1", len(payloads))
	}
	return payloads[0]
}

// TestCompactWithLineageFallbackSeed checks that the manual compaction path
// takes its last main request from SeedLastMainRequest only when the seed's
// identity matches the request, and reports the fallback in its diagnostic.
func TestCompactWithLineageFallbackSeed(t *testing.T) {
	t.Parallel()
	now := fallbackOperationNow
	warm := LastMainRequest{At: now.Add(-time.Minute), ProviderAlias: "openai", BackendModelID: "gpt-main", PromptCacheKey: "cache-key"}
	idle := warm
	idle.At = now.Add(-10 * time.Minute)
	mismatched := warm
	mismatched.BackendModelID = "gpt-other"

	tests := []struct {
		name       string
		seed       LastMainRequest
		wantMain   int
		wantTarget int
		wantStatus string
		wantModel  string
		wantReason string
	}{
		{name: "matching warm seed runs main", seed: warm, wantMain: 2},
		{name: "mismatched seed is dropped and runs target", seed: mismatched, wantTarget: 2, wantStatus: output.CompactionFallbackUsed, wantModel: "anthropic/claude-small", wantReason: "no recent request"},
		{name: "matching idle seed runs target", seed: idle, wantTarget: 2, wantStatus: output.CompactionFallbackUsed, wantModel: "anthropic/claude-small", wantReason: "idle 10 > 5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newFallbackOperationRun(now)
			r.req.PromptCacheKey = "cache-key"
			r.req.SeedLastMainRequest = tt.seed
			var events []output.Event
			r.req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

			_, _, applied, err := NewRunner().CompactWithLineage(context.Background(), r.req, ConversationLineage{}, r.state.Conversation)
			if err != nil {
				t.Fatalf("CompactWithLineage() error = %v", err)
			}
			if !applied {
				t.Error("applied = false, want true")
			}
			if got := len(r.main.requests); got != tt.wantMain {
				t.Errorf("main requests = %d, want %d", got, tt.wantMain)
			}
			if got := len(r.target.requests); got != tt.wantTarget {
				t.Errorf("target requests = %d, want %d", got, tt.wantTarget)
			}
			payload := fallbackCompactionPayload(t, events)
			if payload.CompactionModel != tt.wantModel {
				t.Errorf("CompactionModel = %q, want %q", payload.CompactionModel, tt.wantModel)
			}
			if payload.FallbackStatus != tt.wantStatus {
				t.Errorf("FallbackStatus = %q, want %q", payload.FallbackStatus, tt.wantStatus)
			}
			if payload.FallbackReason != tt.wantReason {
				t.Errorf("FallbackReason = %q, want %q", payload.FallbackReason, tt.wantReason)
			}
		})
	}
}
