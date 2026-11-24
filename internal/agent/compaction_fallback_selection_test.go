package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
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

	outcome, err := compactWithFallback(context.Background(), req, RunState{}, 3, run)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v, want nil", err)
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

	outcome, err := compactWithFallback(context.Background(), req, state, 3, run)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v, want nil", err)
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

// TestTwoStageSummarizeCompactionEmitsSelectionOnce proves a fallback operation
// whose normal and emergency summary calls both run on the fallback announces
// the selection once, before the first summary call.
func TestTwoStageSummarizeCompactionEmitsSelectionOnce(t *testing.T) {
	t.Parallel()
	r := newFallbackOperationRun(fallbackOperationNow)
	r.resolved.ResolvedModel.Alias = "ds-flash"
	var rec eventRecorder
	r.req.Events = rec.sink()
	responses := fallbackOperationResponses()
	var selectedAtSummary []int
	r.target.chatFn = func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		selectedAtSummary = append(selectedAtSummary, len(compactionEventPayloads(t, rec.snapshot())))
		if len(responses) == 0 {
			return provider.ChatResponse{}, errors.New("unexpected target summary call")
		}
		resp := responses[0]
		responses = responses[1:]
		return resp, nil
	}

	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v", err)
	}
	if outcome.StageCount != 2 {
		t.Fatalf("StageCount = %d, want 2 (normal and emergency)", outcome.StageCount)
	}
	if len(selectedAtSummary) != 2 {
		t.Fatalf("target summary calls = %d, want 2", len(selectedAtSummary))
	}
	for i, n := range selectedAtSummary {
		if n != 1 {
			t.Errorf("summary call %d saw %d selection events, want 1", i+1, n)
		}
	}
	if got := len(r.main.requests); got != 0 {
		t.Errorf("main requests = %d, want 0", got)
	}

	selected := compactionEventPayloads(t, rec.snapshot())
	if len(selected) != 1 {
		t.Fatalf("selection events = %d, want 1 across both stages", len(selected))
	}
	payload := selected[0]
	if payload.Severity != "compacting" {
		t.Errorf("Severity = %q, want %q", payload.Severity, "compacting")
	}
	if payload.Turn != 5 {
		t.Errorf("Turn = %d, want 5", payload.Turn)
	}
	if payload.CompactionModel != fallbackOperationTargetRef {
		t.Errorf("CompactionModel = %q, want %q", payload.CompactionModel, fallbackOperationTargetRef)
	}
	if payload.FallbackStatus != output.CompactionFallbackUsed {
		t.Errorf("FallbackStatus = %q, want %q", payload.FallbackStatus, output.CompactionFallbackUsed)
	}
	if payload.FallbackReason != "no recent request" {
		t.Errorf("FallbackReason = %q, want %q", payload.FallbackReason, "no recent request")
	}
	if payload.CompactionModelAlias != "ds-flash" {
		t.Errorf("CompactionModelAlias = %q, want %q", payload.CompactionModelAlias, "ds-flash")
	}
	if got := outcome.CompactionModelAlias; got != "ds-flash" {
		t.Errorf("outcome.CompactionModelAlias = %q, want %q", got, "ds-flash")
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
