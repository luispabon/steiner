package agent

import (
	"context"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// compactionEventPayloads returns the compaction diagnostics in events, in order.
func compactionEventPayloads(t *testing.T, events []output.Event) []output.ContextCompactionEvent {
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
	return payloads
}

// selectionTestTarget returns a fallback target resolved from the named alias
// "ds-flash".
func selectionTestTarget() CompactionTarget {
	return CompactionTarget{
		Provider: &fakeProvider{},
		ResolvedModel: provider.ResolvedModel{
			Alias:          "ds-flash",
			ProviderAlias:  "opencode-go",
			BackendModelID: "deepseek-v4.1-flash",
		},
	}
}

// TestCompactWithFallbackEmitsSelectionBeforeSummary proves a selected fallback
// is announced on the compaction turn when its summary stage passes the fit check,
// before the summary call runs, carrying the canonical model, the configured
// alias, the used status and the reason. The operation itself emits nothing.
func TestCompactWithFallbackEmitsSelectionBeforeSummary(t *testing.T) {
	t.Parallel()
	now := fallbackOperationNow
	var rec eventRecorder
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
		Clock:         fixedCompactionClock{now: now},
		Events:        rec.sink(),
		CompactionFallback: CompactionFallback{
			After: 5 * time.Minute,
			Ref:   "ds-flash",
			Resolve: func(context.Context) (CompactionTarget, error) {
				return selectionTestTarget(), nil
			},
		},
	}

	run := func(summaryReq *RunRequest, announce *compactionFallbackAnnouncement) (CompactionOutcome, error) {
		if summaryReq == nil || announce == nil {
			t.Fatal("run did not get the fallback target and its announcement")
		}
		if events := rec.snapshot(); len(events) != 0 {
			t.Fatalf("events before the fit check = %d, want 0", len(events))
		}
		// A passing fit check announces the selection just before the summary call.
		announce.announce()
		selected := compactionEventPayloads(t, rec.snapshot())
		if len(selected) != 1 {
			t.Fatalf("compaction events before the summary call = %d, want 1", len(selected))
		}
		if got := selected[0].Severity; got != "compacting" {
			t.Errorf("selection severity = %q, want %q", got, "compacting")
		}
		if got := selected[0].FallbackStatus; got != output.CompactionFallbackUsed {
			t.Errorf("selection FallbackStatus = %q, want %q", got, output.CompactionFallbackUsed)
		}
		return CompactionOutcome{Applied: true}, nil
	}

	outcome, err := compactWithFallbackAnnounced(context.Background(), req, RunState{}, 3, run)
	if err != nil {
		t.Fatalf("compactWithFallbackAnnounced() error = %v, want nil", err)
	}

	payloads := compactionEventPayloads(t, rec.snapshot())
	events := rec.snapshot()
	if len(events) != 1 {
		t.Fatalf("events = %d, want exactly the selection event", len(events))
	}
	if len(payloads) != 1 {
		t.Fatalf("compaction events = %d, want 1", len(payloads))
	}
	payload := payloads[0]
	if payload.Severity != "compacting" {
		t.Errorf("Severity = %q, want %q", payload.Severity, "compacting")
	}
	if payload.Scope != "conversation" {
		t.Errorf("Scope = %q, want %q", payload.Scope, "conversation")
	}
	if payload.Turn != 3 {
		t.Errorf("Turn = %d, want 3", payload.Turn)
	}
	if want := "opencode-go/deepseek-v4.1-flash"; payload.CompactionModel != want {
		t.Errorf("CompactionModel = %q, want %q", payload.CompactionModel, want)
	}
	if payload.CompactionModelAlias != "ds-flash" {
		t.Errorf("CompactionModelAlias = %q, want %q", payload.CompactionModelAlias, "ds-flash")
	}
	if payload.FallbackStatus != output.CompactionFallbackUsed {
		t.Errorf("FallbackStatus = %q, want %q", payload.FallbackStatus, output.CompactionFallbackUsed)
	}
	if payload.FallbackReason != "no recent request" {
		t.Errorf("FallbackReason = %q, want %q", payload.FallbackReason, "no recent request")
	}
	if got := outcome.CompactionModelAlias; got != "ds-flash" {
		t.Errorf("outcome.CompactionModelAlias = %q, want %q", got, "ds-flash")
	}
	if want := "opencode-go/deepseek-v4.1-flash"; outcome.CompactionModel != want {
		t.Errorf("outcome.CompactionModel = %q, want %q", outcome.CompactionModel, want)
	}
}

// TestCompactWithFallbackSelectedWithoutEventSink proves a selected fallback
// still runs its summary on the target when the request has no event sink.
func TestCompactWithFallbackSelectedWithoutEventSink(t *testing.T) {
	t.Parallel()
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
		Clock:         fixedCompactionClock{now: fallbackOperationNow},
		CompactionFallback: CompactionFallback{
			After: 5 * time.Minute,
			Ref:   "ds-flash",
			Resolve: func(context.Context) (CompactionTarget, error) {
				return selectionTestTarget(), nil
			},
		},
	}

	ran := false
	run := func(summaryReq *RunRequest, _ *compactionFallbackAnnouncement) (CompactionOutcome, error) {
		if summaryReq == nil {
			t.Error("run used main, want the fallback target")
		}
		ran = true
		return CompactionOutcome{Applied: true}, nil
	}

	outcome, err := compactWithFallbackAnnounced(context.Background(), req, RunState{}, 3, run)
	if err != nil {
		t.Fatalf("compactWithFallbackAnnounced() error = %v, want nil", err)
	}
	if !ran {
		t.Error("run did not execute, want the fallback operation")
	}
	if got := outcome.CompactionModelAlias; got != "ds-flash" {
		t.Errorf("outcome.CompactionModelAlias = %q, want %q", got, "ds-flash")
	}
}

// TestCompactWithFallbackNoSelectionEventWhenWarm proves a warm cache selects no
// target, emits no selection event, and runs the operation on main.
func TestCompactWithFallbackNoSelectionEventWhenWarm(t *testing.T) {
	t.Parallel()
	now := fallbackOperationNow
	var rec eventRecorder
	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
		Clock:         fixedCompactionClock{now: now},
		Events:        rec.sink(),
		CompactionFallback: CompactionFallback{
			After: 5 * time.Minute,
			Ref:   "ds-flash",
			Resolve: func(context.Context) (CompactionTarget, error) {
				return selectionTestTarget(), nil
			},
		},
	}
	state := RunState{LastMainRequest: LastMainRequest{At: now.Add(-time.Minute)}}

	ran := false
	run := func(summaryReq *RunRequest, _ *compactionFallbackAnnouncement) (CompactionOutcome, error) {
		if summaryReq != nil {
			t.Error("run used the fallback target, want main on a warm cache")
		}
		ran = true
		return CompactionOutcome{Applied: true}, nil
	}

	outcome, err := compactWithFallbackAnnounced(context.Background(), req, state, 3, run)
	if err != nil {
		t.Fatalf("compactWithFallbackAnnounced() error = %v, want nil", err)
	}
	if !ran {
		t.Error("run did not execute, want the main operation")
	}
	if events := rec.snapshot(); len(events) != 0 {
		t.Errorf("events = %d, want none for a warm cache", len(events))
	}
	if outcome.FallbackStatus != "" {
		t.Errorf("FallbackStatus = %q, want empty for a warm cache", outcome.FallbackStatus)
	}
	if outcome.CompactionModelAlias != "" {
		t.Errorf("CompactionModelAlias = %q, want empty for a warm cache", outcome.CompactionModelAlias)
	}
}

// TestSummarizeCompactionStagesSelectionEmittedOnce proves a fallback operation
// whose normal and emergency stages both pass the fit check announces the
// selection once, before the first summary call.
func TestSummarizeCompactionStagesSelectionEmittedOnce(t *testing.T) {
	t.Parallel()
	mainReq := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	targetProv := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false), compactionFallbackSummaryResponse(false)}}
	target := compactionFallbackSummaryTarget(targetProv, compactionFallbackSummaryBudget())
	fallback, err := compactionFallbackRequest(mainReq, target)
	if err != nil {
		t.Fatalf("compactionFallbackRequest() error = %v", err)
	}
	var rec eventRecorder
	announce := &compactionFallbackAnnouncement{sink: rec.sink(), turn: 1, model: compactionModelName(target.ResolvedModel), reason: "no recent request"}
	// Five turns leave a source for the normal stage (it keeps three turns) and
	// for the emergency stage (it keeps one turn of the normal result).
	msgs := []Message{
		{Role: MessageRoleUser, Content: "question 1"}, {Role: MessageRoleAssistant, Content: "answer 1"},
		{Role: MessageRoleUser, Content: "question 2"}, {Role: MessageRoleAssistant, Content: "answer 2"},
		{Role: MessageRoleUser, Content: "question 3"}, {Role: MessageRoleAssistant, Content: "answer 3"},
		{Role: MessageRoleUser, Content: "question 4"}, {Role: MessageRoleAssistant, Content: "answer 4"},
		{Role: MessageRoleUser, Content: "question 5"}, {Role: MessageRoleAssistant, Content: "answer 5"},
	}
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}

	fitCalls := 0
	stages := summarizeCompactionStages{
		stageRunner: func(ctx context.Context, req RunRequest, state RunState, turn int, candidate ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
			params.announce = announce
			return summarizeCompactionStage(ctx, req, state, turn, candidate, params)
		},
		// The normal result needs emergency compaction; the emergency result fits.
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			fitCalls++
			if fitCalls == 1 {
				return prompt.RequestTokenBudget{ContextSize: 4000, PromptUsage: 1}, nil
			}
			return prompt.RequestTokenBudget{ContextSize: 4000, PromptUsage: 0.1, Fits: true}, nil
		},
		summaryReq: &fallback,
	}
	if _, err := stages.run(context.Background(), mainReq, state, 1, candidate); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got := len(targetProv.requests); got != 2 {
		t.Fatalf("target provider requests = %d, want 2 (normal and emergency)", got)
	}
	if got := len(compactionEventPayloads(t, rec.snapshot())); got != 1 {
		t.Errorf("selection events = %d, want 1 across both stages", got)
	}
}

// TestCompactionModelAlias checks which resolved models carry a configured alias
// distinct from their canonical provider/model name.
func TestCompactionModelAlias(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		model provider.ResolvedModel
		want  string
	}{
		{
			name:  "raw reference repeats the canonical name",
			model: provider.ResolvedModel{Alias: "anthropic/claude-small", ProviderAlias: "anthropic", BackendModelID: "claude-small"},
		},
		{
			name:  "configured alias",
			model: provider.ResolvedModel{Alias: "ds-flash", ProviderAlias: "opencode-go", BackendModelID: "deepseek-v4.1-flash"},
			want:  "ds-flash",
		},
		{
			name:  "alias already carries the provider prefix",
			model: provider.ResolvedModel{Alias: "openai/gpt-4", ProviderAlias: "proxy", BackendModelID: "gpt-4"},
			want:  "openai/gpt-4",
		},
		{
			name:  "alias with surrounding space",
			model: provider.ResolvedModel{Alias: "  ds-flash  ", ProviderAlias: "opencode-go", BackendModelID: "deepseek-v4.1-flash"},
			want:  "ds-flash",
		},
		{
			name:  "empty alias",
			model: provider.ResolvedModel{ProviderAlias: "opencode-go", BackendModelID: "deepseek-v4.1-flash"},
		},
		{
			name:  "empty provider and model fields",
			model: provider.ResolvedModel{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := compactionModelAlias(tt.model); got != tt.want {
				t.Errorf("compactionModelAlias() = %q, want %q", got, tt.want)
			}
		})
	}
}
