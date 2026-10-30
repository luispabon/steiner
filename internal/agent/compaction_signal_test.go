package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// TestSummarizeCompactionStageAttachesRetirementSignalOnlyOnMain proves a main
// summary runs on a request context carrying the backend state retirement
// signal, while a fallback summary runs on the caller's context unchanged.
func TestSummarizeCompactionStageAttachesRetirementSignalOnlyOnMain(t *testing.T) {
	tests := []struct {
		name        string
		fallback    bool
		wantSameCtx bool
	}{
		{name: "main summary carries the signal"},
		{name: "fallback summary carries none", fallback: true, wantSameCtx: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msgs := compactionFallbackSummaryMessages()
			var gotCtx context.Context
			prov := &fakeProvider{chatFn: func(ctx context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
				gotCtx = ctx
				return compactionFallbackSummaryResponse(false), nil
			}}
			req := compactionFallbackSummaryFixture(prov, &testRecorder{})
			var summaryReq *RunRequest
			if tt.fallback {
				target := compactionFallbackSummaryTarget(prov, compactionFallbackSummaryBudget())
				fallback, err := compactionFallbackRequest(req, target)
				if err != nil {
					t.Fatalf("compactionFallbackRequest() error = %v", err)
				}
				summaryReq = &fallback
			}
			state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
			parentCtx := context.Background()
			outcome, err := summarizeCompactionStage(parentCtx, req, state, 1, ConversationCandidate{Messages: msgs}, compactionFallbackSummaryParams(128, summaryReq))
			if err != nil {
				t.Fatalf("summarizeCompactionStage() error = %v", err)
			}
			if !outcome.Applied {
				t.Fatal("Applied = false, want true")
			}
			if gotCtx == nil {
				t.Fatal("summary call did not run")
			}
			if same := gotCtx == parentCtx; same != tt.wantSameCtx {
				t.Errorf("summary context unchanged = %v, want %v", same, tt.wantSameCtx)
			}
		})
	}
}

// TestSummarizeCompactionStageSignaledRetirementClearsBackendState proves a
// signal without a response clear still drops the generation's backend state on
// success.
func TestSummarizeCompactionStageSignaledRetirementClearsBackendState(t *testing.T) {
	stubBackendStateRetirement(t, func(context.Context) bool { return true })
	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	latestID := state.Lineage.latestGenerationID()
	state.Lineage = state.Lineage.withBackendState(latestID, []byte(`{"previous_response_id":"resp_1"}`), false)
	prov := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
	req := compactionFallbackSummaryFixture(prov, &testRecorder{})

	outcome, err := summarizeCompactionStage(context.Background(), req, state, 1, ConversationCandidate{Messages: msgs}, compactionFallbackSummaryParams(128, nil))
	if err != nil {
		t.Fatalf("summarizeCompactionStage() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if !outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = false, want the signalled retirement reported")
	}
	if got := outcome.State.Lineage.latestBackendState(); got != nil {
		t.Errorf("latest generation kept backend state %s, want it cleared", got)
	}
}

// TestCompactConversationForBudgetSignaledRetirementOnCanceledSummary proves a
// canceled summary that signalled its retirement drops only the latest lineage
// backend state, keeping the conversation and generation IDs, and reports the
// retirement on the returned error.
func TestCompactConversationForBudgetSignaledRetirementOnCanceledSummary(t *testing.T) {
	stubBackendStateRetirement(t, func(context.Context) bool { return true })
	conv := compactionTestTurns()
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	latestID := state.Lineage.latestGenerationID()
	state.Lineage = state.Lineage.withBackendState(latestID, []byte(`{"version":1}`), false)
	originalIDs := lineageGenerationIDs(state.Lineage)

	canceled := fmt.Errorf("summary: %w", context.Canceled)
	prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, canceled
	}}
	req := compactionTestBudget()
	req.Provider = prov

	_, applied, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 0, nil, map[string]bool{}, nil, "")
	if err == nil {
		t.Fatal("compactConversationForBudgetWithSteering() error = nil, want the canceled summary")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want it to wrap context.Canceled", err)
	}
	if !provider.BackendStateRetired(err) {
		t.Error("provider.BackendStateRetired(err) = false, want true for a signalled retirement")
	}
	if applied {
		t.Error("applied = true, want false")
	}
	if len(state.Conversation) != len(conv) {
		t.Fatalf("conversation length = %d, want the original %d", len(state.Conversation), len(conv))
	}
	for i := range conv {
		if state.Conversation[i].Content != conv[i].Content {
			t.Errorf("conversation[%d] = %q, want the original %q", i, state.Conversation[i].Content, conv[i].Content)
		}
	}
	if got := lineageGenerationIDs(state.Lineage); !slices.Equal(got, originalIDs) {
		t.Errorf("lineage generation IDs = %v, want the original %v", got, originalIDs)
	}
	if state.Lineage.latestBackendState() != nil {
		t.Errorf("latest generation kept backend state %s, want it cleared", state.Lineage.latestBackendState())
	}
}

// TestCompactWithFallbackMainRetryKeepsSignaledRetirement proves a main rerun
// after a failed, marked fallback attempt reports its own signalled retirement,
// and that the fallback attempt's marker never clears main state.
func TestCompactWithFallbackMainRetryKeepsSignaledRetirement(t *testing.T) {
	baseCtx := context.Background()
	stubBackendStateRetirement(t, func(ctx context.Context) bool { return ctx != baseCtx })

	msgs := compactionFallbackSummaryMessages()
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	latestID := state.Lineage.latestGenerationID()
	state.Lineage = state.Lineage.withBackendState(latestID, []byte(`{"previous_response_id":"resp_1"}`), false)

	targetErr := markCompactionBackendStateRetired(errors.New("target retired its transcript"))
	targetProv := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, targetErr
	}}
	mainProv := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(false)}}
	req := compactionFallbackSummaryFixture(mainProv, &testRecorder{})
	req.CompactionFallback.Resolve = func(context.Context) (CompactionTarget, error) {
		return compactionFallbackSummaryTarget(targetProv, compactionFallbackSummaryBudget()), nil
	}

	outcome, err := summarizeCompactor{}.Compact(baseCtx, req, state, 1, ConversationCandidate{Messages: msgs}, "")
	if err != nil {
		t.Fatalf("Compact() error = %v, want the main rerun to succeed", err)
	}
	if !outcome.Applied {
		t.Error("Applied = false, want the main rerun summary")
	}
	if !outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = false, want the main rerun signal reported")
	}
	if got := len(mainProv.requests); got != 1 {
		t.Errorf("main summary requests = %d, want 1", got)
	}
	if got := len(targetProv.requests); got != 1 {
		t.Errorf("target summary requests = %d, want 1", got)
	}
	if outcome.FallbackStatus != output.CompactionFallbackSkipped {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackSkipped)
	}
	if got := outcome.State.Lineage.latestBackendState(); got != nil {
		t.Errorf("latest generation kept backend state %s, want it cleared", got)
	}
}

// TestCompactWithFallbackHidesTargetRetirementWhenCanceled proves a canceled
// fallback attempt returns its error with the target's retirement marker hidden,
// so the caller cannot mistake it for a main state retirement.
func TestCompactWithFallbackHidesTargetRetirementWhenCanceled(t *testing.T) {
	targetErr := markCompactionBackendStateRetired(errors.New("target retired its transcript"))
	req := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	req.CompactionFallback.Resolve = func(context.Context) (CompactionTarget, error) {
		return compactionFallbackSummaryTarget(&fakeProvider{}, compactionFallbackSummaryBudget()), nil
	}
	run := func(summaryReq *RunRequest) (CompactionOutcome, error) {
		if summaryReq != nil {
			return CompactionOutcome{}, targetErr
		}
		t.Error("main rerun must not happen after a canceled fallback attempt")
		return CompactionOutcome{}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	outcome, err := compactWithFallback(ctx, req, RunState{}, run)
	if !errors.Is(err, targetErr) {
		t.Fatalf("compactWithFallback() error = %v, want it to wrap %v", err, targetErr)
	}
	if provider.BackendStateRetired(err) {
		t.Error("provider.BackendStateRetired(error) = true, want a fallback target retirement hidden")
	}
	if outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = true, want false for a fallback target")
	}
}

// TestCompactWithFallbackDropsTargetClearFlag proves a fallback outcome that
// reports a clear cannot clear main state, even when its summary applies.
func TestCompactWithFallbackDropsTargetClearFlag(t *testing.T) {
	req := compactionFallbackSummaryFixture(&fakeProvider{}, &testRecorder{})
	req.CompactionFallback.Resolve = func(context.Context) (CompactionTarget, error) {
		return compactionFallbackSummaryTarget(&fakeProvider{}, compactionFallbackSummaryBudget()), nil
	}
	run := func(summaryReq *RunRequest) (CompactionOutcome, error) {
		if summaryReq == nil {
			t.Error("run used main, want the fallback target")
		}
		return CompactionOutcome{Applied: true, BackendStateCleared: true}, nil
	}

	outcome, err := compactWithFallback(context.Background(), req, RunState{}, run)
	if err != nil {
		t.Fatalf("compactWithFallback() error = %v", err)
	}
	if !outcome.Applied {
		t.Error("Applied = false, want the fallback summary")
	}
	if outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = true, want a fallback target clear dropped")
	}
	if outcome.FallbackStatus != output.CompactionFallbackUsed {
		t.Errorf("FallbackStatus = %q, want %q", outcome.FallbackStatus, output.CompactionFallbackUsed)
	}
}
