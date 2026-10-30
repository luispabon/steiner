package agent

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// stubBackendStateRetirement replaces the summary retirement reader for the
// rest of the test. The reader is process-wide, so the test must not run in
// parallel with other tests.
func stubBackendStateRetirement(t *testing.T, read func(context.Context) bool) {
	t.Helper()
	previous := backendStateRetirementSignaled
	backendStateRetirementSignaled = read
	t.Cleanup(func() { backendStateRetirementSignaled = previous })
}

// lineageGenerationIDs returns the generation IDs of l in order.
func lineageGenerationIDs(l ConversationLineage) []int {
	ids := make([]int, 0, len(l.Generations))
	for _, gen := range l.Generations {
		ids = append(ids, gen.ID)
	}
	return ids
}

func TestMarkCompactionBackendStateRetired(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("summary failed")
	marked := markCompactionBackendStateRetired(sentinel)
	if marked == nil {
		t.Fatal("markCompactionBackendStateRetired() = nil, want a marked error")
	}
	if !provider.BackendStateRetired(marked) {
		t.Error("provider.BackendStateRetired(marked) = false, want true")
	}
	if marked.Error() != sentinel.Error() {
		t.Errorf("marked error text = %q, want %q", marked.Error(), sentinel.Error())
	}
	if !errors.Is(marked, sentinel) {
		t.Error("marked error does not keep the original chain")
	}
	// Marking an already marked error must return it unchanged. Compare as
	// interfaces so the assertion checks identity without an errorlint-reported
	// error comparison.
	again := markCompactionBackendStateRetired(marked)
	if got, want := any(again), any(marked); got != want {
		t.Error("markCompactionBackendStateRetired() wrapped an already marked error")
	}
	if markCompactionBackendStateRetired(nil) != nil {
		t.Error("markCompactionBackendStateRetired(nil) != nil, want nil")
	}
}

// TestSummarizeCompactionStagesKeepsClearThroughFitError proves a main summary
// that retires the generation's backend state keeps that clear, and marks the
// error, when the post-stage fit check fails. The failed stage contributes no
// conversation or pending archive.
func TestSummarizeCompactionStagesKeepsClearThroughFitError(t *testing.T) {
	t.Parallel()
	msgs := compactionFallbackSummaryMessages()
	lineage := newConversationLineage(msgs).WithNewGenerationState(nil, msgs, json.RawMessage(`{"previous_response_id":"resp_1"}`))
	state := RunState{Conversation: msgs, Lineage: lineage}
	prov := &fakeProvider{responses: []provider.ChatResponse{compactionFallbackSummaryResponse(true)}}
	req := compactionFallbackSummaryFixture(prov, &testRecorder{})
	sentinel := errors.New("fit unavailable")
	candidate := ConversationCandidate{Messages: msgs}

	outcome, err := summarizeCompactionStages{
		stageRunner: summarizeCompactionStage,
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			return prompt.RequestTokenBudget{}, sentinel
		},
	}.run(context.Background(), req, state, 1, candidate)

	if err == nil {
		t.Fatal("run() error = nil, want the fit failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("run() error = %v, want it to wrap %v", err, sentinel)
	}
	if !provider.BackendStateRetired(err) {
		t.Error("provider.BackendStateRetired(err) = false, want true after a main summary cleared backend state")
	}
	if !outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = false, want true")
	}
	if len(outcome.State.Conversation) != 0 || len(outcome.State.Lineage.Generations) != 0 {
		t.Errorf("outcome carried a partial state: %+v", outcome.State)
	}
	if len(outcome.pendingArchive) != 0 {
		t.Errorf("outcome carried %d pending archive sources, want 0", len(outcome.pendingArchive))
	}
}

// TestSummarizeCompactionStagesKeepsClearThroughNoOpStage proves a clear
// reported by the normal stage survives an emergency stage that applies nothing
// but still fits.
func TestSummarizeCompactionStagesKeepsClearThroughNoOpStage(t *testing.T) {
	t.Parallel()
	preStage := RunState{Conversation: []Message{{Role: MessageRoleUser, Content: "keep me"}}}
	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	stages := summarizeCompactionStages{
		stageRunner: func(context.Context, RunRequest, RunState, int, ConversationCandidate, summarizeCompactionStageParams) (CompactionOutcome, error) {
			return CompactionOutcome{Applied: false, BackendStateCleared: true, Fit: fits}, nil
		},
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			t.Error("fitRunner must not be called for a no-op stage")
			return prompt.RequestTokenBudget{}, nil
		},
	}

	outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{})
	if err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
	if !outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = false, want the normal stage clear preserved")
	}
	if outcome.Applied {
		t.Error("outcome.Applied = true, want false for a no-op stage")
	}
	if len(outcome.State.Conversation) != 1 || outcome.State.Conversation[0].Content != "keep me" {
		t.Errorf("outcome.State.Conversation = %+v, want the pre-stage conversation", outcome.State.Conversation)
	}
}

// TestSummarizeCompactionStagesKeepsClearThroughEmergencyError proves a clear
// reported by the normal stage survives an emergency stage failure, and leaves
// no partial state behind.
func TestSummarizeCompactionStagesKeepsClearThroughEmergencyError(t *testing.T) {
	t.Parallel()
	msgs := compactionFallbackSummaryMessages()
	preStage := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}
	candidate := ConversationCandidate{Messages: msgs}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}
	compacted := RunState{
		Conversation: []Message{{Role: MessageRoleSummary, Content: "normal summary"}},
		Lineage:      newConversationLineage([]Message{{Role: MessageRoleSummary, Content: "normal summary"}}),
	}
	stages := summarizeCompactionStages{
		stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
			outcome := CompactionOutcome{Applied: true, State: compacted, Fit: dense}
			if params.mode == prompt.CompactionModeNormal {
				outcome.BackendStateCleared = true
			}
			return outcome, nil
		},
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			return dense, nil
		},
	}

	outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, candidate)
	if err == nil {
		t.Fatal("run() error = nil, want the emergency stage failure")
	}
	if !provider.BackendStateRetired(err) {
		t.Error("provider.BackendStateRetired(err) = false, want true after the normal summary cleared backend state")
	}
	if !outcome.BackendStateCleared {
		t.Error("outcome.BackendStateCleared = false, want the normal stage clear preserved")
	}
	if len(outcome.State.Conversation) != 0 || len(outcome.pendingArchive) != 0 {
		t.Errorf("outcome carried partial state %+v with %d pending archive sources, want neither", outcome.State, len(outcome.pendingArchive))
	}
	if outcome.StageCount != 0 {
		t.Errorf("outcome.StageCount = %d, want 0 for a failed operation", outcome.StageCount)
	}
}

// TestCompactConversationForBudgetClearsRetiredStateOnError proves a main
// summary that retires the generation's backend state drops only that state
// from the caller's lineage when a later stage fails. The original conversation
// and lineage are otherwise unchanged, and nothing is archived.
func TestCompactConversationForBudgetClearsRetiredStateOnError(t *testing.T) {
	t.Parallel()
	conv := denseHistoryConversation()
	state := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
	latestID := state.Lineage.latestGenerationID()
	state.Lineage = state.Lineage.withBackendState(latestID, []byte(`{"version":1}`), false)
	originalIDs := lineageGenerationIDs(state.Lineage)

	sentinel := errors.New("emergency summary failed")
	calls := 0
	prov := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		calls++
		if calls == 1 {
			return provider.ChatResponse{
				Message:           provider.Message{Role: provider.MessageRoleAssistant, Content: strings.Repeat("normal summary detail ", 1200)},
				FinishReason:      "stop",
				ClearBackendState: true,
			}, nil
		}
		return provider.ChatResponse{}, sentinel
	}}
	archive := &fakeHistoryArchive{note: testHistoryNote}
	req := historyArchiveRequest(archive, denseHistoryBudget, nil)
	req.Provider = prov

	_, applied, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 0, nil, map[string]bool{}, nil, "")
	if err == nil {
		t.Fatal("compactConversationForBudgetWithSteering() error = nil, want the emergency failure")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("error = %v, want it to wrap %v", err, sentinel)
	}
	if !provider.BackendStateRetired(err) {
		t.Error("provider.BackendStateRetired(err) = false, want true after the normal summary cleared backend state")
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
	if len(archive.appends) != 0 {
		t.Errorf("archive Append calls = %d, want 0 for a failed compaction", len(archive.appends))
	}
}

// TestCompactWithFallbackKeepsOnlyMainClear proves a failed fallback attempt
// never clears main state, while the main rerun reports its own clear on both
// success and failure.
func TestCompactWithFallbackKeepsOnlyMainClear(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)
	fallbackErr := errors.New("target failed")
	mainErr := errors.New("main failed")

	tests := []struct {
		name        string
		targetClear bool
		mainOutcome CompactionOutcome
		mainErr     error
		wantCleared bool
		wantMarked  bool
		wantApplied bool
		wantErr     error
	}{
		{
			name:        "fallback clear flag does not reach main",
			targetClear: true,
			mainErr:     mainErr,
			wantErr:     mainErr,
		},
		{
			name:        "main rerun clear survives its failure",
			mainOutcome: CompactionOutcome{BackendStateCleared: true},
			mainErr:     mainErr,
			wantCleared: true,
			wantMarked:  true,
			wantErr:     mainErr,
		},
		{
			name:        "main rerun clear survives success",
			mainOutcome: CompactionOutcome{Applied: true, BackendStateCleared: true},
			wantCleared: true,
			wantApplied: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := RunRequest{
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "openai", BackendModelID: "gpt-main"},
				Clock:         fixedCompactionClock{now: now},
				CompactionFallback: CompactionFallback{
					After: 5 * time.Minute,
					Ref:   "claude-small",
					Resolve: func(context.Context) (CompactionTarget, error) {
						return CompactionTarget{
							Provider:      &fakeProvider{},
							ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
						}, nil
					},
				},
			}
			state := RunState{LastMainRequest: LastMainRequest{At: now.Add(-time.Hour)}}
			run := func(summaryReq *RunRequest) (CompactionOutcome, error) {
				if summaryReq != nil {
					return CompactionOutcome{BackendStateCleared: tt.targetClear}, fallbackErr
				}
				return tt.mainOutcome, tt.mainErr
			}

			outcome, err := compactWithFallback(context.Background(), req, state, run)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("compactWithFallback() error = %v, want %v", err, tt.wantErr)
			}
			if got := provider.BackendStateRetired(err); got != tt.wantMarked {
				t.Errorf("provider.BackendStateRetired(error) = %v, want %v", got, tt.wantMarked)
			}
			if outcome.BackendStateCleared != tt.wantCleared {
				t.Errorf("outcome.BackendStateCleared = %v, want %v", outcome.BackendStateCleared, tt.wantCleared)
			}
			if outcome.Applied != tt.wantApplied {
				t.Errorf("outcome.Applied = %v, want %v", outcome.Applied, tt.wantApplied)
			}
		})
	}
}
