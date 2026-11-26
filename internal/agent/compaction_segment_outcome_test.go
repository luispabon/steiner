package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func outcomeNonSystemMessages(messages []provider.Message) []provider.Message {
	var filtered []provider.Message
	for _, message := range messages {
		if message.Role != provider.MessageRoleSystem {
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func TestSegmentPolicyRebasesAreAdoptedAndArchived(t *testing.T) {
	tests := []struct {
		name       string
		messages   func() []Message
		policy     func() config.SegmentedCompactionPolicy
		wantReason string
	}{
		{
			name:     "max segments",
			messages: segmentStageConversation,
			policy: func() config.SegmentedCompactionPolicy {
				p := segmentedCompactionPolicy()
				p.MaxSegments = 2
				return p
			},
			wantReason: "max-segments",
		},
		{
			name: "invalid chain",
			messages: func() []Message {
				messages := segmentStageConversation()
				messages[1].Segment.Sequence = 3
				messages[1].Content = segmentHeader(3) + "broken facts"
				return messages
			},
			policy:     segmentedCompactionPolicy,
			wantReason: "invalid-chain",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
			providerStub := oneSummaryProvider("replacement facts")
			req := segmentedCompactionRequest(providerStub, tt.policy())
			req.HistoryArchive = archive
			req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
			state := stateFromMessages(tt.messages())
			applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
			if err != nil || !applied || !compacted {
				t.Fatalf("compaction applied=%t compacted=%t error=%v, want adoption", applied, compacted, err)
			}
			latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
			if len(latest.SummaryPrefix) != 2 || latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 {
				t.Fatalf("adopted prefix = %+v, want new sequence 1 segment and tail", latest.SummaryPrefix)
			}
			if !strings.Contains(latest.SummaryPrefix[0].Content, "replacement facts") {
				t.Fatalf("adopted segment = %q, want replacement facts", latest.SummaryPrefix[0].Content)
			}
			if len(archive.notes) != 1 || len(archive.appends) != 1 {
				t.Fatalf("archive Note calls = %d Append calls = %d, want one each", len(archive.notes), len(archive.appends))
			}
			lines := segmentDecisionLines(t, req.CompactionLogPath)
			if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=rebase reason="+tt.wantReason+" sequence=1") {
				t.Fatalf("decision lines = %v, want only final %s rebase", lines, tt.wantReason)
			}
		})
	}
}

func TestSegmentEmergencyRetryAdoptsOnlyFinalEmergencyDecision(t *testing.T) {
	full := segmentStageConversation()
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: strings.Repeat("long normal summary ", 1200)}, FinishReason: "stop"},
		retryEmptyReply(),
		summaryResponse("emergency retry facts"),
	}}
	state := stateFromMessages(full)
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.ModelBudget.ContextSize = 7500
	req.ModelBudget.NormalSummaryMaxTokens = 64
	req.ModelBudget.SummaryMaxTokens = 64
	req.ModelBudget.EmergencySummaryMaxTokens = 16
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	req.HistoryArchive = archive
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want emergency retry adoption", applied, compacted, err)
	}
	if len(providerStub.requests) != 3 {
		t.Fatalf("summary calls = %d, want normal, emergency and emergency retry", len(providerStub.requests))
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 2 {
		t.Fatalf("adopted prefix = %+v, want emergency segment and tail", latest.SummaryPrefix)
	}
	if got, want := latest.SummaryPrefix[0].Segment, (&SummarySegment{Sequence: 1, SourceMessageCount: 4}); got == nil || *got != *want {
		t.Fatalf("adopted segment = %+v, want %+v", got, want)
	}
	if got, want := latest.SummaryPrefix[0].Content, segmentHeader(1)+segmentBody("emergency retry facts"); got != want {
		t.Fatalf("adopted segment content = %q, want %q", got, want)
	}
	if body, block, ok := SplitRequestBlock(latest.SummaryPrefix[1].Content); !ok || body != "[steiner:history] archived" || block != "recent two" {
		t.Fatalf("adopted tail = %q (body %q block %q ok %t), want the archive note and carried request", latest.SummaryPrefix[1].Content, body, block, ok)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=rebase reason=emergency sequence=1") {
		t.Fatalf("decision lines = %v, want one final emergency rebase", lines)
	}
	if len(archive.notes) != 2 || len(archive.appends) != 2 {
		t.Fatalf("archive Note calls = %d Append calls = %d, want both stage notes committed with the adopted emergency result", len(archive.notes), len(archive.appends))
	}
	// The normal append kept the three-segment chain and the last three turns;
	// the emergency rebase summarized that prefix plus the first two retained
	// turns, never the turn it kept.
	if got, want := len(archive.notes[1]), 8; got != want {
		t.Fatalf("emergency archive source len = %d, want %d", got, want)
	}
	if archive.notes[1][0].Segment == nil || *archive.notes[1][0].Segment != (SummarySegment{Sequence: 1, SourceMessageCount: 2}) {
		t.Fatalf("emergency archive segment 1 = %+v, want the kept normal chain", archive.notes[1][0].Segment)
	}
	if archive.notes[1][2].Segment == nil || *archive.notes[1][2].Segment != (SummarySegment{Sequence: 3, SourceMessageCount: 2}) {
		t.Fatalf("emergency archive segment 3 = %+v, want the normal append", archive.notes[1][2].Segment)
	}
	if got, want := archive.notes[1][3].Content, "[steiner:history] archived"; got != want {
		t.Fatalf("emergency archive tail = %q, want the note body without its request block", got)
	}
	if !sameMessages(archive.notes[1][4:], full[5:9]) {
		t.Fatalf("emergency archive span = %+v, want the first two retained turns", archive.notes[1][4:])
	}
	if !sameMessages(archive.notes[0], append(stripRequestBlocks(full[2:3]), full[3:5]...)) {
		t.Fatalf("normal archive source = %+v, want the previous tail and the append span", archive.notes[0])
	}
}

func TestSegmentFallbackEmptyReplyRerunsMainWithoutTargetRetry(t *testing.T) {
	main := oneSummaryProvider("main plain summary")
	targetProvider := &fakeProvider{responses: []provider.ChatResponse{retryEmptyReply()}}
	state := plainFallbackState()
	req := segmentedCompactionRequest(main, segmentedCompactionPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	req.CompactionFallback = CompactionFallback{
		After: 1,
		Ref:   "target/model",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      targetProvider,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "target", BackendModelID: "model"},
				ModelBudget: prompt.ModelTokenBudget{
					ContextSize: 100000, MaxCompletionTokens: 256, SummaryMaxTokens: 128,
					NormalSummaryMaxTokens: 128, EmergencySummaryMaxTokens: 64,
				},
			}, nil
		},
	}
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want clean main rerun", applied, compacted, err)
	}
	if len(targetProvider.requests) != 1 || len(main.requests) != 1 {
		t.Fatalf("target calls = %d main calls = %d, want one empty target call and one main call", len(targetProvider.requests), len(main.requests))
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 1 || latest.SummaryPrefix[0].Segment != nil {
		t.Fatalf("main rerun prefix = %+v, want the plain main summary", latest.SummaryPrefix)
	}
	if body, block, ok := SplitRequestBlock(latest.SummaryPrefix[0].Content); !ok || body != "main plain summary" || block != "old request" {
		t.Fatalf("main rerun summary = %q (body %q block %q ok %t), want the plain body and carried request", latest.SummaryPrefix[0].Content, body, block, ok)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=plain reason=empty-source sequence=0") {
		t.Fatalf("decision lines = %v, want only adopted main empty-source", lines)
	}
}

func TestSegmentFallbackNormalResultIsDiscardedAfterEmergencyFailure(t *testing.T) {
	main := oneSummaryProvider("main rerun facts")
	targetCalls := 0
	targetProvider := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		targetCalls++
		if targetCalls == 1 {
			return summaryResponse(strings.Repeat("long target summary ", 1200)), nil
		}
		return provider.ChatResponse{}, errors.New("target emergency failed")
	}}
	full := segmentStageConversation()
	state := stateFromMessages(full)
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	req := segmentedCompactionRequest(main, segmentedCompactionPolicy())
	req.ModelBudget.ContextSize = 7500
	req.ModelBudget.NormalSummaryMaxTokens = 64
	req.ModelBudget.SummaryMaxTokens = 64
	req.ModelBudget.EmergencySummaryMaxTokens = 16
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	req.CompactionFallback = CompactionFallback{
		After: 1,
		Ref:   "target/model",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      targetProvider,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "target", BackendModelID: "model"},
				ModelBudget:   prompt.ModelTokenBudget{ContextSize: 7500, MaxCompletionTokens: 256, SummaryMaxTokens: 64, NormalSummaryMaxTokens: 64, EmergencySummaryMaxTokens: 16},
			}, nil
		},
	}
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want clean main rerun", applied, compacted, err)
	}
	if targetCalls != 2 || len(main.requests) != 1 {
		t.Fatalf("target calls = %d main calls = %d, want failed target normal and emergency then one main rerun", targetCalls, len(main.requests))
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if len(latest.SummaryPrefix) != 4 {
		t.Fatalf("adopted prefix = %+v, want the original chain plus the main sequence 3 append and tail", latest.SummaryPrefix)
	}
	wantSegments := []SummarySegment{
		{Sequence: 1, SourceMessageCount: 2},
		{Sequence: 2, SourceMessageCount: 3},
		{Sequence: 3, SourceMessageCount: 2},
	}
	for i, want := range wantSegments {
		if latest.SummaryPrefix[i].Segment == nil || *latest.SummaryPrefix[i].Segment != want {
			t.Fatalf("adopted segment %d = %+v, want %+v", i+1, latest.SummaryPrefix[i].Segment, want)
		}
	}
	if got, want := latest.SummaryPrefix[2].Content, segmentHeader(3)+segmentBody("main rerun facts"); got != want {
		t.Fatalf("adopted append = %q, want %q", got, want)
	}
	if body, block, ok := SplitRequestBlock(latest.SummaryPrefix[3].Content); !ok || body != "[steiner:history] archived" || block != "span request" {
		t.Fatalf("adopted tail = %q (body %q block %q ok %t), want the note and carried request", latest.SummaryPrefix[3].Content, body, block, ok)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=append reason=append sequence=3") {
		t.Fatalf("decision lines = %v, want only the adopted main append decision", lines)
	}
	if len(archive.appends) != 1 {
		t.Fatalf("archive Append calls = %d, want only the main rerun archive", len(archive.appends))
	}
	if !sameMessages(archive.appends[0], append(stripRequestBlocks(full[2:3]), full[3:5]...)) {
		t.Fatalf("archive Append source = %+v, want the previous tail and the append span", archive.appends[0])
	}
}

func TestSegmentEmergencyZeroSourceFallsBackToPlain(t *testing.T) {
	const note = "[steiner:history] archived to sessions/new.jsonl"
	longNormal := strings.Repeat("long normal summary ", 1200)
	prefix := []Message{{Role: MessageRoleSummary, Content: "earlier plain summary"}}
	// One retained turn keeps the emergency retention at the normal stage's own
	// tail, so the emergency source is the normal plain summary alone.
	turn := []Message{
		{Role: MessageRoleUser, Content: "retained request"},
		{Role: MessageRoleAssistant, Content: "retained reply"},
	}
	archive := &fakeHistoryArchive{note: note}
	providerStub := oneSummaryProvider(longNormal, "zero-source emergency facts")
	state := RunState{Lineage: ConversationLineage{
		Generations:      []ConversationGeneration{newConversationGeneration(1, prefix, turn)},
		NextGenerationID: 2,
	}}
	state.Conversation = state.Lineage.FullMessages()
	req := segmentedCompactionRequest(providerStub, segmentedCompactionPolicy())
	req.ModelBudget.ContextSize = 7500
	req.ModelBudget.NormalSummaryMaxTokens = 64
	req.ModelBudget.SummaryMaxTokens = 64
	req.ModelBudget.EmergencySummaryMaxTokens = 16
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")

	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil || !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t error=%v, want the emergency plain adoption", applied, compacted, err)
	}
	if got, want := len(providerStub.requests), 2; got != want {
		t.Fatalf("summary calls = %d, want normal then emergency", got)
	}
	normalMessages := providerStub.requests[0].Messages
	if got, want := normalMessages[len(normalMessages)-1].Content, prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeNormal); got != want {
		t.Fatalf("normal instruction = %q, want the ordinary normal instruction", got)
	}
	emergencyMessages := providerStub.requests[1].Messages
	if got, want := emergencyMessages[len(emergencyMessages)-1].Content, prompt.RenderConversationCompactionInstruction("", prompt.CompactionModeEmergency); got != want {
		t.Fatalf("emergency instruction = %q, want the ordinary emergency instruction", got)
	}
	if messageContentsContain(outcomeNonSystemMessages(emergencyMessages), "retained request") {
		t.Fatal("emergency request replayed the retained turn, want only the normal plain summary")
	}

	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if got, want := len(latest.SummaryPrefix), 1; got != want {
		t.Fatalf("adopted summary prefix len = %d, want %d plain summary", got, want)
	}
	if latest.SummaryPrefix[0].Segment != nil {
		t.Fatalf("adopted segment = %+v, want the plain fallback with no segment metadata", latest.SummaryPrefix[0].Segment)
	}
	if got, want := latest.SummaryPrefix[0].Content, "zero-source emergency facts"+"\n\n"+note; got != want {
		t.Fatalf("adopted summary = %q, want %q", got, want)
	}
	if !sameMessages(latest.Messages, turn) {
		t.Fatalf("adopted retained messages = %+v, want the unchanged single turn", latest.Messages)
	}
	for _, generation := range state.Lineage.Generations {
		for _, message := range generation.SummaryPrefix {
			if message.Segment != nil && message.Segment.SourceMessageCount == 0 {
				t.Fatalf("stored segment = %+v, want no zero-count segment", message.Segment)
			}
		}
	}

	if got, want := len(archive.notes), 2; got != want {
		t.Fatalf("archive Note calls = %d, want the normal and emergency sources", got)
	}
	if !sameMessages(archive.notes[0], prefix) {
		t.Fatalf("normal archive source = %+v, want the earlier plain summary", archive.notes[0])
	}
	if len(archive.notes[1]) != 1 || archive.notes[1][0].Role != MessageRoleSummary || archive.notes[1][0].Content != strings.TrimSpace(longNormal)+"\n\n"+note {
		t.Fatalf("emergency archive source = %+v, want only the long normal plain summary", archive.notes[1])
	}
	if got, want := len(archive.appends), len(archive.notes); got != want {
		t.Fatalf("archive Append calls = %d, want %d matching the notes", got, want)
	}
	for i := range archive.notes {
		if !sameMessages(archive.appends[i], archive.notes[i]) {
			t.Fatalf("archive Append %d = %+v, want the noted source", i, archive.appends[i])
		}
	}

	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=plain reason=empty-source sequence=0") {
		t.Fatalf("decision lines = %v, want only the final plain empty-source decision", lines)
	}
}

func TestSegmentFallbackTargetSuccessIsAdopted(t *testing.T) {
	main := &fakeProvider{}
	full := segmentStageConversation()
	targetProvider := oneSummaryProvider("fallback segment facts")
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	state := stateFromMessages(full)
	policy := segmentedCompactionPolicy()
	policy.MaxSegments = 2
	req := segmentedCompactionRequest(main, policy)
	req.HistoryArchive = archive
	req.CompactionLogPath = filepath.Join(t.TempDir(), "compaction.log")
	req.CompactionFallback = CompactionFallback{
		After: 1,
		Ref:   "target/model",
		Resolve: func(context.Context) (CompactionTarget, error) {
			return CompactionTarget{
				Provider:      targetProvider,
				ResolvedModel: provider.ResolvedModel{ProviderAlias: "target", BackendModelID: "model"},
				ModelBudget: prompt.ModelTokenBudget{
					ContextSize: 100000, MaxCompletionTokens: 256, SummaryMaxTokens: 128,
					NormalSummaryMaxTokens: 128, EmergencySummaryMaxTokens: 64,
				},
			}, nil
		},
	}
	applied, compacted, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, 1, nil, map[string]bool{}, new(int), "")
	if err != nil {
		t.Fatalf("compaction error = %v", err)
	}
	if !applied || !compacted {
		t.Fatalf("compaction applied=%t compacted=%t, want adopted fallback", applied, compacted)
	}
	if len(targetProvider.requests) != 1 || len(main.requests) != 0 {
		t.Fatalf("target calls = %d main calls = %d, want target only", len(targetProvider.requests), len(main.requests))
	}
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	if got, want := latest.SummaryPrefix[0].Segment, (&SummarySegment{Sequence: 1, SourceMessageCount: 2}); got == nil || *got != *want {
		t.Fatalf("fallback adopted segment = %+v, want %+v", got, want)
	}
	if got, want := latest.SummaryPrefix[0].Content, segmentHeader(1)+segmentBody("fallback segment facts"); got != want {
		t.Fatalf("fallback adopted content = %q, want %q", got, want)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=rebase reason=retry sequence=1") {
		t.Fatalf("decision lines = %v, want adopted fallback retry rebase", lines)
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("archive Note calls = %d Append calls = %d, want one adopted archive", len(archive.notes), len(archive.appends))
	}
	if !sameMessages(archive.notes[0], stripRequestBlocks(full[:5])) || !sameMessages(archive.appends[0], stripRequestBlocks(full[:5])) {
		t.Fatalf("fallback archive Note=%+v Append=%+v, want the rebase source", archive.notes[0], archive.appends[0])
	}
}
