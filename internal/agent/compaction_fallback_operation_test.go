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
	"github.com/luispabon/steiner/internal/usagestats"
)

// fallbackOperationNow is the fixed clock time of the fallback operation fixtures.
var fallbackOperationNow = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

// fallbackOperationAfter is the idle threshold after which the main cache is cold.
const fallbackOperationAfter = 5 * time.Minute

// Usage model names of the fallback target and of main in these fixtures.
const (
	fallbackOperationTargetRef = "anthropic/claude-small"
	fallbackOperationMainRef   = "openai/gpt-main"
)

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
			Ref:   fallbackOperationTargetRef,
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

// fallbackUsageModels returns the provider alias and backend model of each
// recorded usage observation, in order.
func fallbackUsageModels(obs []usagestats.Observation) []string {
	models := make([]string, 0, len(obs))
	for _, o := range obs {
		models = append(models, o.ProviderAlias+"/"+o.BackendModelID)
	}
	return models
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
		wantStages         int
		wantStatus         string
		wantModel          string
		wantReason         string
		wantReasonContains string
		wantUsage          []string
	}{
		{
			name:       "fallback disabled runs main",
			setup:      func(r *fallbackOperationRun) { r.req.CompactionFallback = CompactionFallback{} },
			wantMain:   2,
			wantStages: 2,
			wantUsage:  []string{fallbackOperationMainRef, fallbackOperationMainRef},
		},
		{
			name: "warm main cache runs main",
			setup: func(r *fallbackOperationRun) {
				r.state.LastMainRequest = LastMainRequest{At: fallbackOperationNow.Add(-time.Minute)}
			},
			wantMain:   2,
			wantStages: 2,
			wantUsage:  []string{fallbackOperationMainRef, fallbackOperationMainRef},
		},
		{
			name:       "cold cache without main request runs target",
			wantTarget: 2,
			wantStages: 2,
			wantStatus: output.CompactionFallbackUsed,
			wantModel:  fallbackOperationTargetRef,
			wantReason: "no recent request",
			wantUsage:  []string{fallbackOperationTargetRef, fallbackOperationTargetRef},
		},
		{
			name: "cold cache after idle time runs target",
			setup: func(r *fallbackOperationRun) {
				r.state.LastMainRequest = LastMainRequest{At: fallbackOperationNow.Add(-10 * time.Minute)}
			},
			wantTarget: 2,
			wantStages: 2,
			wantStatus: output.CompactionFallbackUsed,
			wantModel:  fallbackOperationTargetRef,
			wantReason: "idle 10m > 5m",
			wantUsage:  []string{fallbackOperationTargetRef, fallbackOperationTargetRef},
		},
		{
			name: "target summary fits in one stage",
			setup: func(r *fallbackOperationRun) {
				r.target.responses = []provider.ChatResponse{compactionFallbackSummaryResponse(false)}
			},
			wantTarget: 1,
			wantStages: 1,
			wantStatus: output.CompactionFallbackUsed,
			wantModel:  fallbackOperationTargetRef,
			wantReason: "no recent request",
			wantUsage:  []string{fallbackOperationTargetRef},
		},
		{
			name: "target too small for summary reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolved.ModelBudget = prompt.ModelTokenBudget{ContextSize: 1, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 16}
			},
			wantMain:   2,
			wantStages: 2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  fallbackOperationMainRef,
			wantReason: "context too large for " + fallbackOperationTargetRef,
			wantUsage:  []string{fallbackOperationMainRef, fallbackOperationMainRef},
		},
		{
			// The target records its stage 1 usage before stage 2 fails. The
			// rerun then records both main stages.
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
			wantStages:         2,
			wantStatus:         output.CompactionFallbackSkipped,
			wantModel:          fallbackOperationMainRef,
			wantReasonContains: "fallback failed: target emergency failed",
			wantUsage:          []string{fallbackOperationTargetRef, fallbackOperationMainRef, fallbackOperationMainRef},
		},
		{
			name: "resolve error reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolveErr = errors.New("resolve failed")
			},
			wantMain:   2,
			wantStages: 2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  fallbackOperationMainRef,
			wantReason: "fallback failed: resolve compaction fallback: resolve failed",
			wantUsage:  []string{fallbackOperationMainRef, fallbackOperationMainRef},
		},
		{
			name: "target without provider reruns on main",
			setup: func(r *fallbackOperationRun) {
				r.resolved.Provider = nil
			},
			wantMain:   2,
			wantStages: 2,
			wantStatus: output.CompactionFallbackSkipped,
			wantModel:  fallbackOperationMainRef,
			wantReason: "fallback failed: compaction fallback target has no provider",
			wantUsage:  []string{fallbackOperationMainRef, fallbackOperationMainRef},
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
			if outcome.StageCount != tt.wantStages {
				t.Errorf("StageCount = %d, want %d", outcome.StageCount, tt.wantStages)
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
			if got := fallbackUsageModels(r.recorder.obs); !reflect.DeepEqual(got, tt.wantUsage) {
				t.Errorf("usage observations = %v, want %v", got, tt.wantUsage)
			}
		})
	}
}

// TestCompactionFallbackOperationRerunsOnMainAfterEmptyTargetSummary checks that a
// target summary with no text is discarded and the operation reruns on main.
func TestCompactionFallbackOperationRerunsOnMainAfterEmptyTargetSummary(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	r.target.responses = []provider.ChatResponse{{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "stop"}}
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want the main rerun to succeed", err)
	}
	if got := len(r.target.requests); got != 1 {
		t.Errorf("target requests = %d, want 1 (the empty normal summary)", got)
	}
	if len(r.main.requests) == 0 {
		t.Error("main requests = 0, want the operation rerun on main")
	}
	if !outcome.Applied {
		t.Error("outcome.Applied = false, want the main rerun to apply")
	}
	if outcome.FallbackStatus != output.CompactionFallbackSkipped {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackSkipped)
	}
}

// TestCompactionEmptyMainSummaryKeepsFallbackText checks that an empty summary on
// main still applies the deterministic summary text and is not rerun.
func TestCompactionEmptyMainSummaryKeepsFallbackText(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	r.req.CompactionFallback = CompactionFallback{}
	empty := provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant}, FinishReason: "stop"}
	r.main.responses = []provider.ChatResponse{empty, empty}
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil for an empty main summary", err)
	}
	if !outcome.Applied {
		t.Error("outcome.Applied = false, want the deterministic summary to apply on main")
	}
	if outcome.FallbackStatus != "" {
		t.Errorf("FallbackStatus = %q, want empty when no fallback was selected", outcome.FallbackStatus)
	}
}

// TestCompactionFallbackNotAppliedIsNotLabeledUsed checks that a fallback attempt
// that applies nothing reports no fallback status and no compaction model.
func TestCompactionFallbackNotAppliedIsNotLabeledUsed(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	state := RunState{LastMainRequest: LastMainRequest{At: fallbackOperationNow.Add(-time.Hour)}}
	run := func(summaryReq *RunRequest) (CompactionOutcome, error) {
		if summaryReq == nil {
			return CompactionOutcome{}, errors.New("run used main, want the fallback target")
		}
		return CompactionOutcome{}, nil
	}
	outcome, err := compactWithFallback(context.Background(), r.req, state, run)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v, want nil", err)
	}
	if outcome.FallbackStatus != "" {
		t.Errorf("FallbackStatus = %q, want empty for a fallback that applied nothing", outcome.FallbackStatus)
	}
	if outcome.CompactionModel != "" {
		t.Errorf("CompactionModel = %q, want empty for a fallback that applied nothing", outcome.CompactionModel)
	}
}

// TestCompactionFallbackFailureReasonKeptWhenMainAppliesNothing checks that a
// failed fallback whose main rerun applies nothing keeps the fallback failure as
// the skip reason and does not report the fallback as used.
func TestCompactionFallbackFailureReasonKeptWhenMainAppliesNothing(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	state := RunState{LastMainRequest: LastMainRequest{At: fallbackOperationNow.Add(-time.Hour)}}
	run := func(summaryReq *RunRequest) (CompactionOutcome, error) {
		if summaryReq != nil {
			return CompactionOutcome{}, errors.New("target unavailable")
		}
		return CompactionOutcome{}, nil
	}
	outcome, err := compactWithFallback(context.Background(), r.req, state, run)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v, want nil", err)
	}
	if outcome.Applied {
		t.Error("outcome.Applied = true, want false when main applies nothing")
	}
	if outcome.FallbackStatus != output.CompactionFallbackSkipped {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackSkipped)
	}
	if want := "fallback failed: target unavailable"; outcome.FallbackReason != want {
		t.Errorf("FallbackReason = %q, want %q", outcome.FallbackReason, want)
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
		{name: "model call timeout", cause: errModelCallTimeout},
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
		{name: "mismatched seed is dropped and runs target", seed: mismatched, wantTarget: 2, wantStatus: output.CompactionFallbackUsed, wantModel: fallbackOperationTargetRef, wantReason: "no recent request"},
		{name: "matching idle seed runs target", seed: idle, wantTarget: 2, wantStatus: output.CompactionFallbackUsed, wantModel: fallbackOperationTargetRef, wantReason: "idle 10m > 5m"},
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

// TestCompactWithLineageFallbackSkippedEvent checks the diagnostic of a discarded
// fallback: main produces the summary, and the event names main with the skip reason.
func TestCompactWithLineageFallbackSkippedEvent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		setup      func(r *fallbackOperationRun)
		wantReason string
	}{
		{
			name: "target summary does not fit",
			setup: func(r *fallbackOperationRun) {
				r.resolved.ModelBudget = prompt.ModelTokenBudget{ContextSize: 1, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 16}
			},
			wantReason: "context too large for " + fallbackOperationTargetRef,
		},
		{
			name: "target provider error",
			setup: func(r *fallbackOperationRun) {
				r.target.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
					return provider.ChatResponse{}, errors.New("target unavailable")
				}
			},
			wantReason: "fallback failed: target unavailable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newFallbackOperationRun(fallbackOperationNow)
			tt.setup(r)
			var events []output.Event
			r.req.Events = output.SinkFunc(func(event output.Event) { events = append(events, event) })

			_, _, applied, err := NewRunner().CompactWithLineage(context.Background(), r.req, ConversationLineage{}, r.state.Conversation)
			if err != nil {
				t.Fatalf("CompactWithLineage() error = %v", err)
			}
			if !applied {
				t.Error("applied = false, want true")
			}
			if got := len(r.main.requests); got != 2 {
				t.Errorf("main requests = %d, want 2 (rerun)", got)
			}
			payload := fallbackCompactionPayload(t, events)
			if payload.CompactionModel != fallbackOperationMainRef {
				t.Errorf("CompactionModel = %q, want %q", payload.CompactionModel, fallbackOperationMainRef)
			}
			if payload.FallbackStatus != output.CompactionFallbackSkipped {
				t.Errorf("FallbackStatus = %q, want %q", payload.FallbackStatus, output.CompactionFallbackSkipped)
			}
			if payload.FallbackReason != tt.wantReason {
				t.Errorf("FallbackReason = %q, want %q", payload.FallbackReason, tt.wantReason)
			}
		})
	}
}

// TestCompactionUsedReason checks the used-fallback reason: whole minutes when
// they differ, exact durations when rounding makes them equal.
func TestCompactionUsedReason(t *testing.T) {
	t.Parallel()
	now := fallbackOperationNow
	tests := []struct {
		name  string
		last  time.Time // zero means no recorded main request
		after time.Duration
		want  string
	}{
		{name: "no recorded request", after: 5 * time.Minute, want: "no recent request"},
		{name: "whole minutes", last: now.Add(-10 * time.Minute), after: 5 * time.Minute, want: "idle 10m > 5m"},
		{name: "idle rounds down", last: now.Add(-7*time.Minute - 20*time.Second), after: 5 * time.Minute, want: "idle 7m > 5m"},
		{name: "rounded idle ties threshold", last: now.Add(-5*time.Minute - 10*time.Second), after: 5 * time.Minute, want: "idle 5m10s > 5m0s"},
		{name: "sub-second excess ties threshold", last: now.Add(-5*time.Minute - 400*time.Millisecond), after: 5 * time.Minute, want: "idle 5m0.4s > 5m0s"},
		{name: "half-minute threshold ties", last: now.Add(-5*time.Minute - 31*time.Second), after: 5*time.Minute + 30*time.Second, want: "idle 5m31s > 5m30s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compactionUsedReason(now, LastMainRequest{At: tt.last}, tt.after); got != tt.want {
				t.Errorf("compactionUsedReason() = %q, want %q", got, tt.want)
			}
		})
	}
}

// firstReadClock reports first on its first Now call and later on every call after.
type firstReadClock struct {
	realClock
	first, later time.Time
	calls        int
}

func (c *firstReadClock) Now() time.Time {
	c.calls++
	if c.calls == 1 {
		return c.first
	}
	return c.later
}

// TestCompactionFallbackReasonUsesSelectionClock proves the used reason comes
// from the clock read that made the selection. A second read would report 70
// minutes idle instead of 10.
func TestCompactionFallbackReasonUsesSelectionClock(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	r.req.Clock = &firstReadClock{first: fallbackOperationNow, later: fallbackOperationNow.Add(time.Hour)}
	r.state.LastMainRequest = LastMainRequest{At: fallbackOperationNow.Add(-10 * time.Minute)}
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v", err)
	}
	if outcome.FallbackStatus != output.CompactionFallbackUsed {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackUsed)
	}
	if outcome.FallbackReason != "idle 10m > 5m" {
		t.Errorf("FallbackReason = %q, want %q", outcome.FallbackReason, "idle 10m > 5m")
	}
}

// fallbackRunFixture is a stateful main run over the seeded compaction of
// statefulRunRequest. The seed is over the threshold and no main request is
// recorded, so the compaction runs on the fallback target. Main's tools, params,
// extra params and identity are set so the next main request can be checked.
func fallbackRunFixture(main provider.Provider, target provider.Provider) RunRequest {
	req := statefulRunRequest(main, parentUsageFixture(8000))
	req.UsageSource = usagestats.SourceParent
	req.Tools = []provider.ToolSpec{compactionFallbackTestTool()}
	req.ResolvedModel = provider.ResolvedModel{
		ProviderAlias:  "openai",
		BackendModelID: "model-a",
		Params:         map[string]any{"temperature": 0.7},
		ExtraParams:    map[string]any{"top_k": "main"},
	}
	req.PromptCacheKey = "main-cache"
	req.TransportSession = "main-transport"
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   fallbackOperationTargetRef,
		Resolve: func(context.Context) (CompactionTarget, error) {
			return compactionFallbackSummaryTarget(target, compactionFallbackSummaryBudget()), nil
		},
	}
	return req
}

// TestCompactionFallbackRunKeepsMainRequest proves that after a fallback
// compaction the next main request keeps main's tools, params, extra params and
// identity, and that the summary went only to the target.
func TestCompactionFallbackRunKeepsMainRequest(t *testing.T) {
	t.Parallel()
	main := scriptedStatefulProvider(scriptedUsageStep{promptTokens: 8000})
	target := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
	if _, err := NewRunner().Run(context.Background(), fallbackRunFixture(main, target)); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := len(target.requests); got != 1 || !target.requests[0].CompactionSummary {
		t.Fatalf("target requests = %d, want one summary request", got)
	}
	if got := summaryRequests(main); got != 0 {
		t.Errorf("main summary requests = %d, want 0", got)
	}
	if got := len(main.requests); got != 1 {
		t.Fatalf("main requests = %d, want 1 normal request after compaction", got)
	}
	chatReq := main.requests[0]
	if want := []provider.ToolSpec{compactionFallbackTestTool()}; !reflect.DeepEqual(chatReq.Tools, want) {
		t.Errorf("Tools = %+v, want main tools %+v", chatReq.Tools, want)
	}
	if want := map[string]any{"temperature": 0.7}; !reflect.DeepEqual(chatReq.Params, want) {
		t.Errorf("Params = %v, want main params %v", chatReq.Params, want)
	}
	if want := map[string]any{"top_k": "main"}; !reflect.DeepEqual(chatReq.ExtraParams, want) {
		t.Errorf("ExtraParams = %v, want main extra params %v", chatReq.ExtraParams, want)
	}
	if chatReq.Model != "model-a" {
		t.Errorf("Model = %q, want model-a", chatReq.Model)
	}
	if chatReq.PromptCacheKey != "main-cache" {
		t.Errorf("PromptCacheKey = %q, want main-cache", chatReq.PromptCacheKey)
	}
	if chatReq.TransportSession != "main-transport" {
		t.Errorf("TransportSession = %q, want main-transport", chatReq.TransportSession)
	}
}

// TestCompactionFallbackRunStatefulFloor proves that on a stateful main, the first
// main response after a fallback compaction decides the floor, as it does after
// a main compaction.
func TestCompactionFallbackRunStatefulFloor(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		nextPromptTokens int
		wantFloor        int
	}{
		{name: "below threshold arms nothing", nextPromptTokens: 6999, wantFloor: 0},
		{name: "threshold arms floor", nextPromptTokens: 7000, wantFloor: 7000},
		{name: "cap arms no floor", nextPromptTokens: 9000, wantFloor: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			main := scriptedStatefulProvider(scriptedUsageStep{promptTokens: tt.nextPromptTokens})
			target := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
			state, err := NewRunner().Run(context.Background(), fallbackRunFixture(main, target))
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := len(target.requests); got != 1 {
				t.Fatalf("target requests = %d, want 1 (fallback compaction applied)", got)
			}
			if got := len(main.requests); got != 1 {
				t.Fatalf("main requests = %d, want 1", got)
			}
			if state.ParentUsage == nil {
				t.Fatal("ParentUsage = nil, want the next main response's usage")
			}
			if got := state.ParentUsage.StatefulFloor; got != tt.wantFloor {
				t.Errorf("StatefulFloor = %d, want %d", got, tt.wantFloor)
			}
		})
	}
}
