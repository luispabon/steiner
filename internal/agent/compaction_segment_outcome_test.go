package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func TestSegmentPolicyRebasesAreAdoptedAndArchived(t *testing.T) {
	tests := []struct {
		name       string
		messages   func() []Message
		policy     func() SegmentedCompactionPolicy
		wantReason string
	}{
		{
			name:     "max segments",
			messages: segmentStageConversation,
			policy: func() SegmentedCompactionPolicy {
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
	if len(latest.SummaryPrefix) != 2 || latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 {
		t.Fatalf("adopted prefix = %+v, want emergency sequence 1 and tail", latest.SummaryPrefix)
	}
	if !strings.Contains(latest.SummaryPrefix[0].Content, "emergency retry facts") {
		t.Fatalf("adopted segment = %q, want emergency retry facts", latest.SummaryPrefix[0].Content)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=rebase reason=emergency sequence=1") {
		t.Fatalf("decision lines = %v, want one final emergency rebase", lines)
	}
	if len(archive.notes) != 2 || len(archive.appends) != 2 {
		t.Fatalf("archive Note calls = %d Append calls = %d, want both stage notes committed with the adopted emergency result", len(archive.notes), len(archive.appends))
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
	if len(latest.SummaryPrefix) != 1 || latest.SummaryPrefix[0].Segment != nil || !strings.Contains(latest.SummaryPrefix[0].Content, "main plain summary") {
		t.Fatalf("main rerun prefix = %+v, want plain main summary", latest.SummaryPrefix)
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
	state := stateFromMessages(segmentStageConversation())
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
	if len(latest.SummaryPrefix) != 4 || latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 || !strings.Contains(latest.SummaryPrefix[2].Content, "main rerun facts") {
		t.Fatalf("adopted prefix = %+v, want original chain plus main sequence 3 append", latest.SummaryPrefix)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=append reason=append sequence=3") {
		t.Fatalf("decision lines = %v, want only the adopted main append decision", lines)
	}
	if len(archive.appends) != 1 {
		t.Fatalf("archive Append calls = %d, want only the main rerun archive", len(archive.appends))
	}
}

func TestSegmentFallbackTargetSuccessIsAdopted(t *testing.T) {
	main := &fakeProvider{}
	targetProvider := oneSummaryProvider("fallback segment facts")
	archive := &fakeHistoryArchive{note: "[steiner:history] archived"}
	state := stateFromMessages(segmentStageConversation())
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
	if latest.SummaryPrefix[0].Segment == nil || latest.SummaryPrefix[0].Segment.Sequence != 1 || !strings.Contains(latest.SummaryPrefix[0].Content, "fallback segment facts") {
		t.Fatalf("fallback adopted prefix = %+v, want rebased sequence 1", latest.SummaryPrefix)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "segment_decision action=rebase reason=retry sequence=1") {
		t.Fatalf("decision lines = %v, want adopted fallback retry rebase", lines)
	}
	if len(archive.notes) != 1 || len(archive.appends) != 1 {
		t.Fatalf("archive Note calls = %d Append calls = %d, want one adopted archive", len(archive.notes), len(archive.appends))
	}
}
