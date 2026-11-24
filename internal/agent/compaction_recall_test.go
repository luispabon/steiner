package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// recallCompactionConversation returns five turns whose removed window carries a
// tool result and a sub-agent envelope, and whose retained window carries a
// second tool result that must not be archived.
func recallCompactionConversation(t *testing.T) (msgs []Message, want []RecallEntry) {
	t.Helper()
	const (
		removedToolBody  = "removed tool result body"
		retainedToolBody = "retained tool result body"
	)
	envelope, inner := recallTestEnvelope(t, recallTestCompletion("code", "call-agent-1", "sub-agent body"))
	msgs = []Message{
		{Role: MessageRoleUser, Content: "turn 0 user"},
		{Role: MessageRoleAssistant, Content: "removed tool call", ToolCalls: []ToolCall{{ID: "call-1", Name: "bash"}}},
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: removedToolBody},
		{Role: MessageRoleUser, Content: envelope + "\n\nand what now?"},
		{Role: MessageRoleAssistant, Content: "turn 1 answer"},
		{Role: MessageRoleUser, Content: "turn 2 user"},
		{Role: MessageRoleAssistant, Content: "turn 2 answer"},
		{Role: MessageRoleUser, Content: "turn 3 user"},
		{Role: MessageRoleAssistant, Content: "turn 3 answer"},
		{Role: MessageRoleUser, Content: "turn 4 user"},
		{Role: MessageRoleAssistant, Content: "retained tool call", ToolCalls: []ToolCall{{ID: "call-2", Name: "read"}}},
		{Role: MessageRoleTool, Name: "read", ToolCallID: "call-2", Content: retainedToolBody},
	}
	want = []RecallEntry{
		{Kind: "tool", Tool: "bash", CallID: "call-1", Body: removedToolBody},
		{Kind: "agent", Tool: "code", CallID: "call-agent-1", Body: inner},
	}
	return msgs, want
}

// denseRecallConversation returns denseHistoryConversation with one tool result
// in the normal stage's source and one in the emergency stage's source.
func denseRecallConversation() (msgs []Message, want []RecallEntry) {
	base := denseHistoryConversation()
	const (
		normalToolBody    = "normal stage tool body"
		emergencyToolBody = "emergency stage tool body"
	)
	msgs = []Message{
		base[0], base[1],
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-normal", Content: normalToolBody},
		base[2], base[3],
		{Role: MessageRoleTool, Name: "bash", ToolCallID: "call-emergency", Content: emergencyToolBody},
		base[4], base[5], base[6], base[7],
	}
	want = []RecallEntry{
		{Kind: "tool", Tool: "bash", CallID: "call-normal", Body: normalToolBody},
		{Kind: "tool", Tool: "bash", CallID: "call-emergency", Body: emergencyToolBody},
	}
	return msgs, want
}

// compactionRecallEntries collects the entries of every body the outcome drops.
func compactionRecallEntries(outcome CompactionOutcome) []RecallEntry {
	return collectRecallEntries(outcome.pendingRecall)
}

// assertNoConversationWarnings fails when rec holds any conversation warning.
func assertNoConversationWarnings(t *testing.T, rec *eventRecorder) {
	t.Helper()
	for _, event := range rec.snapshot() {
		if event.Type == output.EventTypeConversationWarning {
			t.Errorf("unexpected warning event: %+v", event.Payload)
		}
	}
}

// TestCompactionStageCapturesRecallSourceWithoutHistoryArchive proves a stage
// captures the removed source for recall even when no history archive is set.
func TestCompactionStageCapturesRecallSourceWithoutHistoryArchive(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall stage summary")}}
	candidate, ok := selectCompactionCandidate(state.Lineage, map[string]bool{})
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}

	outcome, err := summarizeCompactor{}.Compact(context.Background(), req, state, 1, candidate)
	if err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want true")
	}
	if len(outcome.pendingArchive) != 0 {
		t.Errorf("pending history sources = %d, want 0 without a history archive", len(outcome.pendingArchive))
	}
	assertRecallEntries(t, compactionRecallEntries(outcome), want)
}

// TestCompactionRecallAutoPathArchivesRemovedBodies proves the auto path writes
// one entry per removed result body, in message order, once the outcome applies.
func TestCompactionRecallAutoPathArchivesRemovedBodies(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	var rec eventRecorder
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall summary handoff")}}
	req.RecallArchive = archive
	req.Events = rec.sink()

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	assertRecallEntries(t, archive.written, want)
	if got, want := state.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Errorf("adopted first role = %q, want %q", got, want)
	}
	assertNoConversationWarnings(t, &rec)
}

// TestCompactionRecallManualPathArchivesRemovedBodies proves the manual
// compaction path archives the removed bodies too.
func TestCompactionRecallManualPathArchivesRemovedBodies(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall summary handoff")}}
	req.RecallArchive = archive

	compacted, _, applied, err := (&Runner{}).CompactWithLineage(context.Background(), req, ConversationLineage{}, msgs)
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	assertRecallEntries(t, archive.written, want)
	if got, want := compacted[0].Role, MessageRoleSummary; got != want {
		t.Errorf("compacted first role = %q, want %q", got, want)
	}
}

// TestCompactionRecallWriteErrorWarnsAndAdopts proves a recall write failure
// warns without aborting the compaction: the state is adopted and the remaining
// bodies are still written.
func TestCompactionRecallWriteErrorWarnsAndAdopts(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	var rec eventRecorder
	archive := &fakeRecallArchive{failAt: map[int]error{1: errors.New("disk full")}}
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall summary handoff")}}
	req.RecallArchive = archive
	req.Events = rec.sink()

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	assertRecallEntries(t, archive.written, want)
	if got, want := state.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Errorf("adopted first role = %q, want %q", got, want)
	}
	assertSingleCompactWarning(t, &rec, "archive recall body: disk full")
}

// TestCompactionRecallTwoStageArchivesBothStagesInOrder proves the two-stage
// outcome merges both stages' removed bodies in stage order.
func TestCompactionRecallTwoStageArchivesBothStagesInOrder(t *testing.T) {
	t.Parallel()
	msgs, want := denseRecallConversation()
	state := historyArchiveState(msgs)
	var rec eventRecorder
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.ModelBudget = denseHistoryBudget
	req.Provider = &fakeProvider{responses: denseTwoStageResponses()}
	req.RecallArchive = archive
	req.Events = rec.sink()

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	assertRecallEntries(t, archive.written, want)
	assertNoConversationWarnings(t, &rec)
}

// TestCompactionRecallDiscardedOutcomeWritesNothing proves a compaction that
// fails after its normal stage applies writes no recall entry.
func TestCompactionRecallDiscardedOutcomeWritesNothing(t *testing.T) {
	t.Parallel()
	msgs, _ := denseRecallConversation()
	state := historyArchiveState(msgs)
	archive := &fakeRecallArchive{}
	req := compactionTestBudget()
	req.ModelBudget = denseHistoryBudget
	// Only the normal stage has a response; the emergency call fails.
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse(strings.Repeat("normal summary detail ", 1200))}}
	req.RecallArchive = archive

	var compactionCount int
	if _, _, _, err := (&Runner{}).compactConversationForBudget(context.Background(), req, &state, 0, nil, map[string]bool{}, &compactionCount); err == nil {
		t.Fatal("compactConversationForBudget() error = nil, want emergency failure")
	}
	if len(archive.written) != 0 {
		t.Errorf("recall writes = %d, want 0", len(archive.written))
	}
	if state.Conversation[0].Role == MessageRoleSummary {
		t.Error("state adopted a summary from the discarded outcome")
	}
}

// TestCompactionRecallFallbackArchivesTargetSource proves a fallback compaction
// archives the removed bodies of the target stage that was adopted, and issues
// no main summary call.
func TestCompactionRecallFallbackArchivesTargetSource(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	archive := &fakeRecallArchive{}
	mainProv := &fakeProvider{}
	targetProv := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("target recall summary")}}
	req := compactionTestBudget()
	budget := req.ModelBudget
	req.Provider = mainProv
	req.RecallArchive = archive
	req.CompactionFallback = CompactionFallback{
		After: time.Minute,
		Ref:   "anthropic/claude-small",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      targetProv,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "anthropic", BackendModelID: "claude-small"},
				ModelBudget:   budget,
			}, nil
		},
	}

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got := len(mainProv.requests); got != 0 {
		t.Errorf("main provider requests = %d, want 0", got)
	}
	assertRecallEntries(t, archive.written, want)
	if got := state.Conversation[0].Content; !strings.Contains(got, "target recall summary") {
		t.Errorf("adopted summary = %q, want the fallback target's summary", got)
	}
}

// TestCompactionRecallNilArchiveAdoptsWithoutWrites proves a nil recall archive
// leaves the applied compaction untouched and emits no warning.
func TestCompactionRecallNilArchiveAdoptsWithoutWrites(t *testing.T) {
	t.Parallel()
	msgs, _ := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	var rec eventRecorder
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall summary handoff")}}
	req.Events = rec.sink()

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got, want := state.Conversation[0].Role, MessageRoleSummary; got != want {
		t.Errorf("adopted first role = %q, want %q", got, want)
	}
	assertNoConversationWarnings(t, &rec)
}

// TestCompactionRecallRequestBlockRetryArchivesOnlyAdoptedSources proves the
// latest-request-block retry paths archive only the stages whose summaries are
// adopted: the merged emergency retry keeps both stage sources, and the
// normal-only retry drops the discarded emergency source.
func TestCompactionRecallRequestBlockRetryArchivesOnlyAdoptedSources(t *testing.T) {
	t.Parallel()

	const (
		normalBody    = "normal stage recall body"
		emergencyBody = "emergency stage recall body"
	)
	normalEntry := RecallEntry{Kind: "tool", Tool: "bash", CallID: "call-normal", Body: normalBody}
	emergencyEntry := RecallEntry{Kind: "tool", Tool: "bash", CallID: "call-emergency", Body: emergencyBody}
	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}

	tests := []struct {
		name string
		// emergencyFitsWithoutBlock keeps the emergency summary once its block
		// is dropped; otherwise the ladder falls back to the normal summary.
		emergencyFitsWithoutBlock bool
		wantSummary               string
		want                      []RecallEntry
	}{
		{
			name:                      "emergency retry keeps both stage sources",
			emergencyFitsWithoutBlock: true,
			wantSummary:               "emergency summary",
			want:                      []RecallEntry{normalEntry, emergencyEntry},
		},
		{
			name:        "normal-only retry keeps the normal source",
			wantSummary: "normal summary",
			want:        []RecallEntry{normalEntry},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conv := requestBlockTurns()
			preStage := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
					summary, body, callID := "normal summary", normalBody, "call-normal"
					if params.mode == prompt.CompactionModeEmergency {
						summary, body, callID = "emergency summary", emergencyBody, "call-emergency"
					}
					return CompactionOutcome{
						Applied:           true,
						State:             requestBlockSummaryState(summary),
						SummaryText:       withRequestBlock(summary, "latest request"),
						RequestBlock:      output.RequestBlockNew,
						RequestBlockBytes: len("latest request"),
						pendingRecall:     []Message{{Role: MessageRoleTool, Name: "bash", ToolCallID: callID, Content: body}},
					}, nil
				},
				fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
					text := state.Conversation[0].Content
					hasBlock := strings.Contains(text, requestBlockMarker)
					if strings.Contains(text, "emergency summary") {
						if hasBlock || !tt.emergencyFitsWithoutBlock {
							return dense, nil
						}
						return fits, nil
					}
					if hasBlock {
						return dense, nil
					}
					return fits, nil
				},
			}

			outcome, err := stages.run(context.Background(), RunRequest{}, preStage, 1, ConversationCandidate{Messages: conv})
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !outcome.Applied {
				t.Fatal("Applied = false, want a stage adopted")
			}
			if got, want := outcome.RequestBlock, output.RequestBlockDropped; got != want {
				t.Fatalf("RequestBlock = %q, want %q", got, want)
			}
			if got := outcome.State.Conversation[0].Content; !strings.Contains(got, tt.wantSummary) {
				t.Fatalf("adopted summary = %q, want %q", got, tt.wantSummary)
			}
			assertRecallEntries(t, collectRecallEntries(outcome.pendingRecall), tt.want)
		})
	}
}

// markerRecallArchive emits a marker warning from Write, so a test can place the
// write on the event timeline.
type markerRecallArchive struct {
	*fakeRecallArchive
	events *eventRecorder
}

func (a *markerRecallArchive) Write(ctx context.Context, entry RecallEntry) (string, error) {
	emitEvent(a.events.sink(), output.NewConversationWarningEvent(output.ConversationWarningKindCompact, "recall write marker"))
	return a.fakeRecallArchive.Write(ctx, entry)
}

// TestCompactionRecallArchivesBeforeAdoption proves the recall write happens
// before the adopted state's diagnostics, which are emitted after "*state =
// outcome.State".
func TestCompactionRecallArchivesBeforeAdoption(t *testing.T) {
	t.Parallel()
	msgs, want := recallCompactionConversation(t)
	state := historyArchiveState(msgs)
	var rec eventRecorder
	archive := &markerRecallArchive{fakeRecallArchive: &fakeRecallArchive{}, events: &rec}
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: []provider.ChatResponse{summaryResponse("recall summary handoff")}}
	req.RecallArchive = archive
	req.Events = rec.sink()

	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	assertRecallEntries(t, archive.written, want)

	marker, diagnostics := -1, -1
	for i, event := range rec.snapshot() {
		switch {
		case marker < 0 && event.Type == output.EventTypeConversationWarning:
			marker = i
		case diagnostics < 0 && event.Type == output.EventTypeContextDiagnostics:
			diagnostics = i
		}
	}
	if marker < 0 || diagnostics < 0 {
		t.Fatalf("marker index = %d, diagnostics index = %d, want both present", marker, diagnostics)
	}
	if marker > diagnostics {
		t.Errorf("recall write index = %d, diagnostics index = %d, want the write first", marker, diagnostics)
	}
}
