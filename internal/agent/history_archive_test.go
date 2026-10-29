package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// testHistoryNote is the note the fake archive returns.
const testHistoryNote = HistoryNoteMarker + " archived at /data/history.md"

// denseHistoryBudget is small enough that the 1200-word normal summary in
// denseHistoryConversation overflows and forces an emergency stage.
var denseHistoryBudget = prompt.ModelTokenBudget{
	ContextSize:               6000,
	MaxCompletionTokens:       128,
	SafetyMarginTokens:        0,
	SummaryMaxTokens:          64,
	NormalSummaryMaxTokens:    64,
	EmergencySummaryMaxTokens: 16,
}

// fakeHistoryArchive records Note and Append calls. noteErr fails Note calls,
// limited to the noteErrCall-th call (1-based) when set. appendErr fails every
// Append call.
type fakeHistoryArchive struct {
	note        string
	noteErr     error
	noteErrCall int
	appendErr   error
	notes       [][]Message
	appends     [][]Message
}

func (a *fakeHistoryArchive) Note(source []Message) (string, error) {
	a.notes = append(a.notes, source)
	if a.noteErr != nil && (a.noteErrCall == 0 || a.noteErrCall == len(a.notes)) {
		return "", a.noteErr
	}
	return a.note, nil
}

func (a *fakeHistoryArchive) Append(_ context.Context, source []Message) error {
	a.appends = append(a.appends, source)
	return a.appendErr
}

func summaryResponse(content string) provider.ChatResponse {
	return provider.ChatResponse{
		Message:      provider.Message{Role: provider.MessageRoleAssistant, Content: content},
		FinishReason: "stop",
	}
}

// historyArchiveRequest returns a compaction request whose summarizer answers
// with responses in order.
func historyArchiveRequest(archive HistoryArchive, budget prompt.ModelTokenBudget, responses []provider.ChatResponse) RunRequest {
	req := compactionTestBudget()
	req.Provider = &fakeProvider{responses: responses}
	req.ModelBudget = budget
	req.HistoryArchive = archive
	return req
}

// denseHistoryConversation returns four turns of text long enough for both
// compaction stages to run under denseHistoryBudget.
func denseHistoryConversation() []Message {
	var msgs []Message
	for i := 1; i <= 4; i++ {
		msgs = append(msgs,
			Message{Role: MessageRoleUser, Content: strings.Repeat(fmt.Sprintf("turn %d user detail ", i), 4)},
			Message{Role: MessageRoleAssistant, Content: strings.Repeat(fmt.Sprintf("turn %d assistant detail ", i), 4)},
		)
	}
	return msgs
}

func historyArchiveState(conv []Message) RunState {
	return RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
}

// compactIntoState runs one compaction through the auto path, which adopts the
// outcome into state, and reports whether a stage applied.
func compactIntoState(t *testing.T, req RunRequest, state *RunState) bool {
	t.Helper()
	var compactionCount int
	_, applied, _, err := (&Runner{}).compactConversationForBudget(context.Background(), req, state, 0, nil, map[string]bool{}, &compactionCount)
	if err != nil {
		t.Fatalf("compactConversationForBudget() error = %v", err)
	}
	return applied
}

func historyArchiveContents(msgs []Message) []string {
	contents := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		contents = append(contents, msg.Content)
	}
	return contents
}

// assertSingleCompactWarning checks that rec holds exactly one conversation
// warning of kind compact with the given message.
func assertSingleCompactWarning(t *testing.T, rec *eventRecorder, want string) {
	t.Helper()
	var warnings []output.ConversationWarningEvent
	for _, event := range rec.snapshot() {
		if event.Type != output.EventTypeConversationWarning {
			continue
		}
		payload, ok := event.Payload.(output.ConversationWarningEvent)
		if !ok {
			t.Fatalf("warning payload = %T, want output.ConversationWarningEvent", event.Payload)
		}
		warnings = append(warnings, payload)
	}
	if len(warnings) != 1 {
		t.Fatalf("conversation warnings = %+v, want exactly one", warnings)
	}
	if got, want := warnings[0].Kind, output.ConversationWarningKindCompact; got != want {
		t.Errorf("warning kind = %q, want %q", got, want)
	}
	if got := warnings[0].Message; got != want {
		t.Errorf("warning message = %q, want %q", got, want)
	}
}

// TestHistoryArchiveNilKeepsSummaryUnchanged proves a nil archive leaves the
// summary text exactly as the summarizer returned it.
func TestHistoryArchiveNilKeepsSummaryUnchanged(t *testing.T) {
	state := historyArchiveState(compactionTestTurns())
	req := historyArchiveRequest(nil, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got, want := state.Conversation[0].Content, "summary handoff text"; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
}

// TestHistoryArchiveSuccessAppendsSourceOnAdoption proves the note is appended
// to the summary and the stage's source is appended once, after adoption.
func TestHistoryArchiveSuccessAppendsSourceOnAdoption(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	state := historyArchiveState(compactionTestTurns())
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("Note calls = %d, Append calls = %d, want 1 and 1", len(archive.notes), len(archive.appends))
	}
	want := []string{"turn 0 user", "turn 0 answer", "turn 1 user", "turn 1 answer"}
	if got := historyArchiveContents(archive.appends[0]); !slices.Equal(got, want) {
		t.Errorf("appended contents = %q, want %q", got, want)
	}
	if got, want := state.Conversation[0].Content, "summary handoff text\n\n"+testHistoryNote; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
}

// TestHistoryArchiveAppendsImageBearingSource proves the archive receives the
// original source message, including image data, rather than the image marker
// sent to the summarizer.
func TestHistoryArchiveAppendsImageBearingSource(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	conv := compactionTestTurns()
	conv[0].Images = []ImageBlock{{MediaType: "image/png", Data: "AAAA"}}
	state := historyArchiveState(conv)
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("Note calls = %d, Append calls = %d, want 1 and 1", len(archive.notes), len(archive.appends))
	}
	for name, source := range map[string][]Message{"Note": archive.notes[0], "Append": archive.appends[0]} {
		t.Run(name, func(t *testing.T) {
			if got := source[0].Images; len(got) != 1 || got[0].Data != "AAAA" {
				t.Errorf("archived source images = %+v, want the original image data", got)
			}
		})
	}
}

// TestHistoryArchiveNoteErrorAppliesWithoutNoteOrAppend proves a Note error
// still applies the compaction, leaves the summary without a note, appends
// nothing, and warns.
func TestHistoryArchiveNoteErrorAppliesWithoutNoteOrAppend(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{noteErr: errors.New("disk full")}
	state := historyArchiveState(compactionTestTurns())
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	req.Events = rec.sink()
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got, want := state.Conversation[0].Content, "summary handoff text"; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
	if len(archive.appends) != 0 {
		t.Errorf("Append calls = %d, want 0", len(archive.appends))
	}
	assertSingleCompactWarning(t, &rec, "archive compacted history: disk full")
}

// TestHistoryArchiveAppendErrorAfterAdoptionKeepsState proves an Append error
// happens after adoption: the compacted state and its note are kept, and a
// warning is emitted.
func TestHistoryArchiveAppendErrorAfterAdoptionKeepsState(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{note: testHistoryNote, appendErr: errors.New("write failed")}
	state := historyArchiveState(compactionTestTurns())
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	req.Events = rec.sink()
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got, want := state.Conversation[0].Content, "summary handoff text\n\n"+testHistoryNote; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
	if len(archive.appends) != 1 {
		t.Errorf("Append calls = %d, want 1", len(archive.appends))
	}
	assertSingleCompactWarning(t, &rec, "archive compacted history: write failed")
}

// TestHistoryArchiveTwoStageAppendsBothSourcesInOrder proves a normal stage
// followed by an emergency stage appends both sources in order, and the second
// source starts with the normal summary.
func TestHistoryArchiveTwoStageAppendsBothSourcesInOrder(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	req := historyArchiveRequest(archive, denseHistoryBudget, []provider.ChatResponse{
		summaryResponse(strings.Repeat("normal summary detail ", 1200)),
		summaryResponse("emergency handoff summary"),
	})
	state := historyArchiveState(denseHistoryConversation())
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.appends) != 2 {
		t.Fatalf("Append calls = %d, want 2", len(archive.appends))
	}
	normal, emergency := archive.appends[0], archive.appends[1]
	if normal[0].Role == MessageRoleSummary {
		t.Error("normal source starts with a summary, want raw turns")
	}
	if got, want := emergency[0].Role, MessageRoleSummary; got != want {
		t.Errorf("emergency source first role = %q, want %q", got, want)
	}
	for _, msg := range normal {
		for _, later := range emergency {
			if msg.Role != MessageRoleSummary && later.Role != MessageRoleSummary && msg.Content == later.Content {
				t.Errorf("message %q appears in both sources", msg.Content)
			}
		}
	}
	if got := strings.Count(state.Conversation[0].Content, HistoryNoteMarker); got != 1 {
		t.Errorf("summary carries %d history notes, want 1", got)
	}
}

// TestHistoryArchiveDiscardedOutcomeAppendsNothing proves that when the normal
// stage applies but the emergency stage fails, the compaction is not adopted and
// nothing is appended.
func TestHistoryArchiveDiscardedOutcomeAppendsNothing(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	// Only the normal stage has a response; the emergency call fails.
	req := historyArchiveRequest(archive, denseHistoryBudget, []provider.ChatResponse{
		summaryResponse(strings.Repeat("normal summary detail ", 1200)),
	})
	state := historyArchiveState(denseHistoryConversation())
	var compactionCount int
	if _, _, _, err := (&Runner{}).compactConversationForBudget(context.Background(), req, &state, 0, nil, map[string]bool{}, &compactionCount); err == nil {
		t.Fatal("compactConversationForBudget() error = nil, want emergency failure")
	}
	if len(archive.notes) != 1 {
		t.Errorf("Note calls = %d, want 1: the normal stage must reach Note before the emergency failure", len(archive.notes))
	}
	if len(archive.appends) != 0 {
		t.Errorf("Append calls = %d, want 0", len(archive.appends))
	}
	if state.Conversation[0].Role == MessageRoleSummary {
		t.Error("state adopted a summary from the discarded outcome")
	}
}

// TestHistoryArchiveStaleNoteReplaced proves a history note copied from the
// previous summary is replaced, so the summary carries one current note.
func TestHistoryArchiveStaleNoteReplaced(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{
		summaryResponse("kept detail\n" + HistoryNoteMarker + " old note at /old.md\nmore detail"),
	})
	state := historyArchiveState(compactionTestTurns())
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if got, want := state.Conversation[0].Content, "kept detail\nmore detail\n\n"+testHistoryNote; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
}

// TestHistoryArchiveSecondCompactionSourceStartsWithSummary proves that the
// source of a second compaction in the same run begins with the first summary,
// which carries the first note.
func TestHistoryArchiveSecondCompactionSourceStartsWithSummary(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{
		summaryResponse("first summary"),
		summaryResponse("second summary"),
	})
	state := historyArchiveState(compactionTestTurns())
	if !compactIntoState(t, req, &state) {
		t.Fatal("first compaction applied = false, want true")
	}
	more := []Message{
		{Role: MessageRoleUser, Content: "turn 5 user"},
		{Role: MessageRoleAssistant, Content: "turn 5 answer"},
		{Role: MessageRoleUser, Content: "turn 6 user"},
		{Role: MessageRoleAssistant, Content: "turn 6 answer"},
		{Role: MessageRoleUser, Content: "turn 7 user"},
		{Role: MessageRoleAssistant, Content: "turn 7 answer"},
	}
	state.Conversation = append(state.Conversation, more...)
	state.Lineage = state.Lineage.WithAppendedMessages(more)
	if !compactIntoState(t, req, &state) {
		t.Fatal("second compaction applied = false, want true")
	}
	if len(archive.appends) != 2 {
		t.Fatalf("Append calls = %d, want 2", len(archive.appends))
	}
	second := archive.appends[1]
	if got, want := second[0].Role, MessageRoleSummary; got != want {
		t.Fatalf("second source first role = %q, want %q", got, want)
	}
	if got, want := second[0].Content, "first summary\n\n"+testHistoryNote; got != want {
		t.Errorf("second source first content = %q, want %q", got, want)
	}
}

// denseTwoStageResponses answers the normal stage with an overflowing summary
// and the emergency stage with a short one.
func denseTwoStageResponses() []provider.ChatResponse {
	return []provider.ChatResponse{
		summaryResponse(strings.Repeat("normal summary detail ", 1200)),
		summaryResponse("emergency handoff summary"),
	}
}

// runTwoStageWithFit runs the two-stage summarizer over state with fit as the
// fit check after each applied stage, and returns the run error.
func runTwoStageWithFit(t *testing.T, req RunRequest, state RunState, fit compactionFitRunner) error {
	t.Helper()
	candidate, ok := selectCompactionCandidate(state.Lineage, map[string]bool{})
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	stages := summarizeCompactionStages{stageRunner: summarizeCompactionStage, fitRunner: fit}
	_, err := stages.run(context.Background(), req, state, 0, candidate)
	return err
}

// failingFitAt returns a fit runner that fails on its call-th invocation
// (1-based) and delegates to fitConversationState otherwise.
func failingFitAt(call int) compactionFitRunner {
	var calls int
	return func(ctx context.Context, req RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
		calls++
		if calls == call {
			return prompt.RequestTokenBudget{}, errors.New("fit failed")
		}
		return fitConversationState(ctx, req, state)
	}
}

// TestHistoryArchiveFitErrorAfterAppliedStageAppendsNothing proves a fit error
// after an applied stage discards the run's outcome. Each stage reached before
// the failure called Note, and nothing was appended.
func TestHistoryArchiveFitErrorAfterAppliedStageAppendsNothing(t *testing.T) {
	tests := []struct {
		name      string
		failCall  int
		wantNotes int
	}{
		{name: "normal stage fit", failCall: 1, wantNotes: 1},
		{name: "emergency stage fit", failCall: 2, wantNotes: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := &fakeHistoryArchive{note: testHistoryNote}
			req := historyArchiveRequest(archive, denseHistoryBudget, denseTwoStageResponses())
			state := historyArchiveState(denseHistoryConversation())
			if err := runTwoStageWithFit(t, req, state, failingFitAt(tt.failCall)); err == nil {
				t.Fatal("run error = nil, want fit failure")
			}
			if len(archive.notes) != tt.wantNotes {
				t.Errorf("Note calls = %d, want %d", len(archive.notes), tt.wantNotes)
			}
			if len(archive.appends) != 0 {
				t.Errorf("Append calls = %d, want 0", len(archive.appends))
			}
		})
	}
}

// TestHistoryArchiveEmptyNoteAppliesWithoutNoteOrAppend proves an empty note
// leaves the summary unchanged, appends nothing, and emits no warning.
func TestHistoryArchiveEmptyNoteAppliesWithoutNoteOrAppend(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{note: ""}
	state := historyArchiveState(compactionTestTurns())
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	req.Events = rec.sink()
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.notes) != 1 {
		t.Errorf("Note calls = %d, want 1", len(archive.notes))
	}
	if got, want := state.Conversation[0].Content, "summary handoff text"; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
	if len(archive.appends) != 0 {
		t.Errorf("Append calls = %d, want 0", len(archive.appends))
	}
	for _, event := range rec.snapshot() {
		if event.Type == output.EventTypeConversationWarning {
			t.Errorf("unexpected warning event: %+v", event.Payload)
		}
	}
}

// TestHistoryArchiveTwoStageEmergencyNoteErrorKeepsNormalAppend proves a Note
// error in the emergency stage warns and drops only that stage's source. The
// normal stage's source is still appended, and the adopted summary carries no
// note.
func TestHistoryArchiveTwoStageEmergencyNoteErrorKeepsNormalAppend(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{note: testHistoryNote, noteErr: errors.New("disk full"), noteErrCall: 2}
	req := historyArchiveRequest(archive, denseHistoryBudget, denseTwoStageResponses())
	req.Events = rec.sink()
	state := historyArchiveState(denseHistoryConversation())
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.appends) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(archive.appends))
	}
	for _, msg := range archive.appends[0] {
		if msg.Role == MessageRoleSummary {
			t.Errorf("appended source contains a summary, want only the normal stage's raw turns")
		}
	}
	if got, want := state.Conversation[0].Content, "emergency handoff summary"; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
	assertSingleCompactWarning(t, &rec, "archive compacted history: disk full")
}

// TestHistoryArchiveTwoStageAppendErrorStopsAfterFirstSource proves that when
// the first pending Append fails, the second source is not attempted. The
// adopted summary keeps the emergency note, and one warning is emitted.
func TestHistoryArchiveTwoStageAppendErrorStopsAfterFirstSource(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{note: testHistoryNote, appendErr: errors.New("write failed")}
	req := historyArchiveRequest(archive, denseHistoryBudget, denseTwoStageResponses())
	req.Events = rec.sink()
	state := historyArchiveState(denseHistoryConversation())
	if !compactIntoState(t, req, &state) {
		t.Fatal("applied = false, want true")
	}
	if len(archive.appends) != 1 {
		t.Fatalf("Append calls = %d, want 1 (stop after first error)", len(archive.appends))
	}
	if got, want := state.Conversation[0].Content, "emergency handoff summary\n\n"+testHistoryNote; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
	assertSingleCompactWarning(t, &rec, "archive compacted history: write failed")
}

// TestHistoryArchiveManualPathAppendsOnceWithNote proves the manual compaction
// path appends once on success and returns a summary carrying the note.
func TestHistoryArchiveManualPathAppendsOnceWithNote(t *testing.T) {
	archive := &fakeHistoryArchive{note: testHistoryNote}
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	compacted, _, applied, err := (&Runner{}).CompactWithLineage(context.Background(), req, ConversationLineage{}, compactionTestTurns())
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	if len(archive.appends) != 1 {
		t.Fatalf("Append calls = %d, want 1", len(archive.appends))
	}
	if got, want := compacted[0].Content, "summary handoff text\n\n"+testHistoryNote; got != want {
		t.Errorf("summary content = %q, want %q", got, want)
	}
}

// TestHistoryArchiveManualPathAppendErrorReachesEvents proves the manual path
// reports an Append failure through RunRequest.Events, the sink the user sees.
func TestHistoryArchiveManualPathAppendErrorReachesEvents(t *testing.T) {
	var rec eventRecorder
	archive := &fakeHistoryArchive{note: testHistoryNote, appendErr: errors.New("write failed")}
	req := historyArchiveRequest(archive, compactionTestBudget().ModelBudget, []provider.ChatResponse{summaryResponse("summary handoff text")})
	req.Events = rec.sink()
	_, _, applied, err := (&Runner{}).CompactWithLineage(context.Background(), req, ConversationLineage{}, compactionTestTurns())
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("applied = false, want true")
	}
	assertSingleCompactWarning(t, &rec, "archive compacted history: write failed")
}
