package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// compactionMarkerRun compacts the fallback fixture twice with the same replies:
// once through the compactor for its outcome and requests, once through the
// runner for the finished success event.
func compactionMarkerRun(t *testing.T, responses ...provider.ChatResponse) (CompactionOutcome, output.ContextCompactionEvent, []provider.ChatRequest) {
	t.Helper()
	run := retryRun(responses...)
	outcome, err := run.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	eventRun := retryRun(responses...)
	events, err := compactionDiagnosticsRun(t, eventRun.req, eventRun.state)
	if err != nil {
		t.Fatalf("compaction error = %v, want nil", err)
	}
	if len(events) != 1 {
		t.Fatalf("finished compaction diagnostics = %d, want 1", len(events))
	}
	return outcome, events[0], run.main.requests
}

// TestCompactionSummaryTruncatedMarker proves an adopted truncated draft ends
// its summary body with the marker, before the intact request block.
func TestCompactionSummaryTruncatedMarker(t *testing.T) {
	t.Parallel()
	r := retryRun(condenseDraftResponse("a long handoff summary that was cut off"), retryEmptyReply())
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want the adopted draft to apply")
	}
	if !outcome.SummaryTruncated {
		t.Error("SummaryTruncated = false, want true for the adopted draft")
	}
	if outcome.SummaryCondensed {
		t.Error("SummaryCondensed = true, want false: the draft was adopted, not the condense rewrite")
	}
	stored := outcome.State.Conversation[0]
	body, request, ok := SplitRequestBlock(stored.Content)
	if stored.Role != MessageRoleSummary || !ok || request == "" {
		t.Fatalf("stored summary = (%q, %q, %t), want a summary with a request block", body, request, ok)
	}
	if !strings.HasSuffix(body, summaryTruncatedMarker) {
		t.Errorf("summary body = %q, want it to end with the truncation marker", body)
	}
	if storedBody, _, _ := SplitRequestBlock(outcome.SummaryText); storedBody != body {
		t.Errorf("SummaryText body = %q, want the stored body %q", storedBody, body)
	}
}

// TestCompactionSummaryTruncatedMarkerSurvivesRequestBlockDrop proves dropping
// the request block keeps the marker at the end of the stored summary.
func TestCompactionSummaryTruncatedMarkerSurvivesRequestBlockDrop(t *testing.T) {
	t.Parallel()
	r := retryRun(condenseDraftResponse("a long handoff summary that was cut off"), retryEmptyReply())
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if outcome.RequestBlock != output.RequestBlockNew {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockNew)
	}
	stages := summarizeCompactionStages{
		fitRunner: func(context.Context, RunRequest, RunState) (prompt.RequestTokenBudget, error) {
			return prompt.RequestTokenBudget{Fits: true, ContextSize: 1000, PromptUsage: 0.1}, nil
		},
	}
	dropped, ok := stages.adoptWithoutRequestBlock(context.Background(), r.req, outcome, &compactionBudgetMissError{err: errors.New("over threshold")})
	if !ok {
		t.Fatal("adoptWithoutRequestBlock() ok = false, want the block dropped")
	}
	if dropped.RequestBlock != output.RequestBlockDropped {
		t.Errorf("RequestBlock = %q, want %q", dropped.RequestBlock, output.RequestBlockDropped)
	}
	if !dropped.SummaryTruncated {
		t.Error("SummaryTruncated = false, want the flag kept after the drop")
	}
	stored := dropped.State.Conversation[0].Content
	if strings.Contains(stored, requestBlockHeaderPrefix) {
		t.Errorf("stored summary = %q, want the request block gone", stored)
	}
	if !strings.HasSuffix(stored, summaryTruncatedMarker) {
		t.Errorf("stored summary = %q, want it to still end with the truncation marker", stored)
	}
	if !strings.HasSuffix(dropped.SummaryText, summaryTruncatedMarker) {
		t.Errorf("SummaryText = %q, want it to still end with the truncation marker", dropped.SummaryText)
	}
}

// TestCompactionSummaryCondensedNoMarker proves a clean condense rewrite is
// reported as condensed and carries no truncation marker.
func TestCompactionSummaryCondensedNoMarker(t *testing.T) {
	t.Parallel()
	r := retryRun(condenseDraftResponse("a long handoff summary that was cut off"), summaryResponse("condensed handoff summary"))
	outcome, err := r.compact(context.Background())
	if err != nil {
		t.Fatalf("compact() error = %v, want nil", err)
	}
	if !outcome.Applied {
		t.Fatal("Applied = false, want the condensed summary to apply")
	}
	if !outcome.SummaryCondensed {
		t.Error("SummaryCondensed = false, want true for the adopted rewrite")
	}
	if outcome.SummaryTruncated {
		t.Error("SummaryTruncated = true, want false for the clean rewrite")
	}
	body, request, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok || request == "" {
		t.Fatalf("stored summary = (%q, %q, %t), want a summary with a request block", body, request, ok)
	}
	if strings.Contains(body, summaryTruncatedMarker) {
		t.Errorf("summary body = %q, want no truncation marker", body)
	}
	if !strings.Contains(body, "condensed handoff summary") {
		t.Errorf("summary body = %q, want the condensed rewrite", body)
	}
}

// TestCompactionSuccessEventReportsSummaryFlags proves the finished compaction
// event carries the truncation and condense flags of the adopted summary.
func TestCompactionSuccessEventReportsSummaryFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		responses     []provider.ChatResponse
		wantTruncated bool
		wantCondensed bool
	}{
		{
			name:          "adopted truncated draft",
			responses:     []provider.ChatResponse{condenseDraftResponse("a long handoff summary that was cut off"), retryEmptyReply()},
			wantTruncated: true,
		},
		{
			name:          "clean condensed rewrite",
			responses:     []provider.ChatResponse{condenseDraftResponse("a long handoff summary that was cut off"), summaryResponse("condensed handoff summary")},
			wantCondensed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			outcome, event, _ := compactionMarkerRun(t, tt.responses...)
			if outcome.SummaryTruncated != tt.wantTruncated || outcome.SummaryCondensed != tt.wantCondensed {
				t.Errorf("outcome truncated, condensed = %t, %t, want %t, %t", outcome.SummaryTruncated, outcome.SummaryCondensed, tt.wantTruncated, tt.wantCondensed)
			}
			if event.SummaryTruncated != outcome.SummaryTruncated || event.SummaryCondensed != outcome.SummaryCondensed {
				t.Errorf("event truncated, condensed = %t, %t, want %t, %t", event.SummaryTruncated, event.SummaryCondensed, outcome.SummaryTruncated, outcome.SummaryCondensed)
			}
			if event.SummaryTruncated != tt.wantTruncated {
				t.Errorf("event SummaryTruncated = %t, want %t", event.SummaryTruncated, tt.wantTruncated)
			}
		})
	}
}

// TestCompactionEmergencyStageSummaryFlagsWin proves a two-stage compaction
// reports the emergency stage's flags: its summary is the one adopted, so the
// normal stage's flags must not leak into the outcome or the event.
func TestCompactionEmergencyStageSummaryFlagsWin(t *testing.T) {
	t.Parallel()
	denseNormalSummary := strings.Repeat("normal summary detail ", 1200)
	denseCondensedSummary := strings.Repeat("condensed normal detail ", 1200)
	tests := []struct {
		name          string
		responses     []provider.ChatResponse
		wantTruncated bool
		wantCondensed bool
		wantRequests  int
		// wantCondenseRequest is the index of the request carrying the condense
		// steering, or -1 when the truncated stage's condense retry did not fit
		// and the draft was adopted without it.
		wantCondenseRequest int
	}{
		{
			name: "truncated normal stage, clean emergency stage",
			responses: []provider.ChatResponse{
				condenseDraftResponse(denseNormalSummary),
				retryEmptyReply(),
				summaryResponse("emergency handoff summary"),
			},
			wantTruncated:       false,
			wantRequests:        3,
			wantCondenseRequest: 1,
		},
		{
			// The emergency draft is small enough to submit but its condense
			// retry does not fit the fixture budget, so the draft is adopted.
			name: "clean normal stage, truncated emergency stage",
			responses: []provider.ChatResponse{
				summaryResponse(denseNormalSummary),
				condenseDraftResponse("emergency handoff summary"),
			},
			wantTruncated:       true,
			wantRequests:        2,
			wantCondenseRequest: -1,
		},
		{
			// The normal stage is rewritten clear of truncation but stays dense,
			// so the plain emergency summary wins and reports neither flag.
			name: "condensed normal stage, plain emergency stage",
			responses: []provider.ChatResponse{
				condenseDraftResponse(denseNormalSummary),
				summaryResponse(denseCondensedSummary),
				summaryResponse("emergency handoff summary"),
			},
			wantRequests:        3,
			wantCondenseRequest: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			outcome, event, requests := compactionMarkerRun(t, tt.responses...)
			if got := len(requests); got != tt.wantRequests {
				t.Fatalf("provider requests = %d, want %d", got, tt.wantRequests)
			}
			if tt.wantCondenseRequest >= 0 {
				if final := retryFinalContent(requests[tt.wantCondenseRequest]); !strings.Contains(final, "<truncated_summary>") {
					t.Fatalf("request %d = %q, want it to carry the condense steering", tt.wantCondenseRequest+1, final)
				}
			}
			if got, want := outcome.StageCount, 2; got != want {
				t.Fatalf("StageCount = %d, want %d", got, want)
			}
			if outcome.SummaryTruncated != tt.wantTruncated {
				t.Errorf("outcome SummaryTruncated = %t, want %t", outcome.SummaryTruncated, tt.wantTruncated)
			}
			if outcome.SummaryCondensed != tt.wantCondensed {
				t.Errorf("outcome SummaryCondensed = %t, want %t", outcome.SummaryCondensed, tt.wantCondensed)
			}
			if event.SummaryTruncated != tt.wantTruncated {
				t.Errorf("event SummaryTruncated = %t, want %t", event.SummaryTruncated, tt.wantTruncated)
			}
			if event.SummaryCondensed != tt.wantCondensed {
				t.Errorf("event SummaryCondensed = %t, want %t", event.SummaryCondensed, tt.wantCondensed)
			}
			body, _, _ := SplitRequestBlock(outcome.State.Conversation[0].Content)
			if !strings.Contains(body, "emergency handoff summary") {
				t.Errorf("summary body = %q, want the adopted emergency summary", body)
			}
			if got := strings.Contains(body, summaryTruncatedMarker); got != tt.wantTruncated {
				t.Errorf("summary body = %q, want marker present = %t", body, tt.wantTruncated)
			}
		})
	}
}
