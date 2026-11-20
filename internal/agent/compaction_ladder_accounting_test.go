package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestSummarizeCompactionStagesAccountsEveryCall proves each summary call that
// ran is counted in StageCount and its tokens are summed into Usage, including a
// call whose summary is later discarded by the budget ladder.
func TestSummarizeCompactionStagesAccountsEveryCall(t *testing.T) {
	t.Parallel()

	normalUsage := &provider.UsageStats{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, CacheReadInputTokens: 60, CacheCreationInputTokens: 5}
	emergencyUsage := &provider.UsageStats{PromptTokens: 40, CompletionTokens: 4, TotalTokens: 44, CacheReadInputTokens: 20, CacheCreationInputTokens: 2}
	both := &provider.UsageStats{PromptTokens: 140, CompletionTokens: 14, TotalTokens: 154, CacheReadInputTokens: 80, CacheCreationInputTokens: 7}

	fits := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}
	dense := prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.9}

	tests := []struct {
		name string
		// emergencyApplied false means the emergency stage issued no call.
		emergencyApplied bool
		// emergencyFitsWithBlock keeps the emergency summary; otherwise the
		// ladder must adopt a dropped summary or fail.
		emergencyFitsWithBlock    bool
		emergencyFitsWithoutBlock bool
		wantStages                int
		wantUsage                 *provider.UsageStats
	}{
		{name: "normal and emergency both kept", emergencyApplied: true, emergencyFitsWithBlock: true, wantStages: 2, wantUsage: both},
		{name: "emergency adopted without its block", emergencyApplied: true, emergencyFitsWithoutBlock: true, wantStages: 2, wantUsage: both},
		{name: "normal adopted without its block", emergencyApplied: true, wantStages: 2, wantUsage: both},
		{name: "emergency issued no call", wantStages: 1, wantUsage: normalUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			conv := requestBlockTurns()
			preStage := RunState{Conversation: conv, Lineage: newConversationLineage(conv)}
			stages := summarizeCompactionStages{
				stageRunner: func(_ context.Context, _ RunRequest, _ RunState, _ int, _ ConversationCandidate, params summarizeCompactionStageParams) (CompactionOutcome, error) {
					if params.mode == prompt.CompactionModeNormal {
						return CompactionOutcome{
							Applied:           true,
							State:             requestBlockSummaryState("normal summary"),
							SummaryText:       withRequestBlock("normal summary", "latest request"),
							RequestBlock:      output.RequestBlockNew,
							RequestBlockBytes: len("latest request"),
							Usage:             normalUsage,
						}, nil
					}
					if !tt.emergencyApplied {
						return CompactionOutcome{Fit: prompt.RequestTokenBudget{Fits: false}}, nil
					}
					return CompactionOutcome{
						Applied:           true,
						State:             requestBlockSummaryState("emergency summary"),
						SummaryText:       withRequestBlock("emergency summary", "latest request"),
						RequestBlock:      output.RequestBlockNew,
						RequestBlockBytes: len("latest request"),
						Usage:             emergencyUsage,
					}, nil
				},
				fitRunner: func(_ context.Context, _ RunRequest, state RunState) (prompt.RequestTokenBudget, error) {
					text := state.Conversation[0].Content
					hasBlock := strings.Contains(text, requestBlockMarker)
					if strings.Contains(text, "emergency summary") {
						if hasBlock {
							if tt.emergencyFitsWithBlock {
								return fits, nil
							}
							return dense, nil
						}
						if tt.emergencyFitsWithoutBlock {
							return fits, nil
						}
						return dense, nil
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
			if got := outcome.StageCount; got != tt.wantStages {
				t.Fatalf("StageCount = %d, want %d", got, tt.wantStages)
			}
			if outcome.Usage == nil || *outcome.Usage != *tt.wantUsage {
				t.Fatalf("Usage = %+v, want %+v", outcome.Usage, tt.wantUsage)
			}
		})
	}
}

// TestCompactionDiagnosticsSummaryBytesExcludesRequestBlock proves the reported
// summary size is the summary text alone on both adoption paths: a kept block
// and a dropped one.
func TestCompactionDiagnosticsSummaryBytesExcludesRequestBlock(t *testing.T) {
	t.Parallel()

	const body = "the summary body"
	tests := []struct {
		name             string
		summaryText      string
		requestBlock     string
		requestBlockSize int
	}{
		{
			name:             "block kept",
			summaryText:      withRequestBlock(body, "the kept request"),
			requestBlock:     output.RequestBlockNew,
			requestBlockSize: len("the kept request"),
		},
		{
			name:         "block dropped",
			summaryText:  body,
			requestBlock: output.RequestBlockDropped,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var recorder eventRecorder
			emitCompactionDiagnostics(recorder.sink(), compactionDiagnosticsParams{
				turn:              1,
				summaryText:       tt.summaryText,
				requestBlock:      tt.requestBlock,
				requestBlockBytes: tt.requestBlockSize,
			})
			var found bool
			for _, event := range recorder.snapshot() {
				payload, ok := output.AsContextCompactionEvent(event.Payload)
				if !ok {
					continue
				}
				found = true
				if got := payload.SummaryBytes; got != len(body) {
					t.Errorf("SummaryBytes = %d, want %d (the body only)", got, len(body))
				}
				if payload.SummaryText != tt.summaryText {
					t.Errorf("SummaryText = %q, want %q", payload.SummaryText, tt.summaryText)
				}
				if got := payload.RequestBlockBytes; got != tt.requestBlockSize {
					t.Errorf("RequestBlockBytes = %d, want %d", got, tt.requestBlockSize)
				}
			}
			if !found {
				t.Fatal("no compaction diagnostics emitted")
			}
		})
	}
}
