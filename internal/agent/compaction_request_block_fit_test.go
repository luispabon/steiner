package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestCompactionAppliesWhenRequestBlockBreaksSummaryFit proves a window too
// small for the projected source still compacts: the stage rebuilds its summary
// plan without the carried latest-request block, and the new summary keeps
// carrying the request.
func TestCompactionAppliesWhenRequestBlockBreaksSummaryFit(t *testing.T) {
	t.Parallel()

	longRequest := "carried request " + strings.Repeat("detail ", 1600)
	msgs := []Message{{Role: MessageRoleSummary, Content: withRequestBlock("first summary", longRequest)}}
	// The first turn is too big for a window that fits the rest, so the block is
	// what decides the summary fit.
	msgs = append(msgs,
		Message{Role: MessageRoleAssistant, Content: strings.Repeat("assistant detail ", 5000)},
		Message{Role: MessageRoleTool, Content: strings.Repeat("tool detail ", 5000)},
	)
	for i := range 3 {
		msgs = append(msgs,
			Message{Role: MessageRoleAssistant, Content: fmt.Sprintf("call %d", i)},
			Message{Role: MessageRoleTool, Content: fmt.Sprintf("result %d", i)},
		)
	}
	prov := &fakeProvider{responses: []provider.ChatResponse{summaryResponse("second summary")}}
	req := compactionTestBudget()
	req.Provider = prov
	state := RunState{Conversation: msgs, Lineage: newConversationLineage(msgs)}

	ctx := context.Background()
	candidate, ok := selectCompactionCandidate(state.Lineage, nil)
	if !ok {
		t.Fatal("selectCompactionCandidate() ok = false, want true")
	}
	source, retained := compactionSourceAndRetention(cloneMessages(candidate.Messages), compactionRetentionBaseMessages(state.Lineage, candidate), normalCompactionRetainTurns)
	params := summarizeCompactionStageParams{
		sourceMessages:   source,
		retainedMessages: retained,
		mode:             prompt.CompactionModeNormal,
		maxTokens:        compactionSummaryMaxTokensForMode(req.ModelBudget, prompt.CompactionModeNormal),
	}
	strippedParams := params
	strippedParams.sourceMessages, ok = stripRequestBlockSource(source)
	if !ok {
		t.Fatal("source carries no request block, want the carried block")
	}
	stripped, fits, err := buildCompactionExecutionPlanWithMode(ctx, req, state, candidate, strippedParams)
	if err != nil || !fits {
		t.Fatalf("stripped plan fits = %t, error = %v, want a fit", fits, err)
	}

	// A window just above the stripped plan makes the block decide the fit.
	tight := req
	tight.ModelBudget.ContextSize = stripped.fit.TotalTokens + 64
	projected, fits, err := buildCompactionExecutionPlanWithMode(ctx, tight, state, candidate, params)
	if err != nil {
		t.Fatalf("projected plan error = %v", err)
	}
	if fits {
		t.Fatalf("projected plan fits at window %d, want the block to break the fit", tight.ModelBudget.ContextSize)
	}
	stripped, fits, err = buildCompactionExecutionPlanWithMode(ctx, tight, state, candidate, strippedParams)
	if err != nil || !fits {
		t.Fatalf("stripped plan fits = %t at window %d, error = %v, want a fit", fits, tight.ModelBudget.ContextSize, err)
	}
	if projected.fit.TotalTokens <= stripped.fit.TotalTokens {
		t.Fatalf("projected total = %d, stripped total = %d, want the block to cost tokens", projected.fit.TotalTokens, stripped.fit.TotalTokens)
	}

	outcome := compactRequestBlockOnce(t, tight, state, 1)
	if got, want := len(prov.requests), 1; got != want {
		t.Fatalf("provider requests = %d, want %d", got, want)
	}
	body, request, ok := SplitRequestBlock(outcome.State.Conversation[0].Content)
	if !ok || body != "second summary" {
		t.Fatalf("stored summary = (%q, %t), want the second summary body with a block", body, ok)
	}
	if !strings.HasPrefix(longRequest, strings.Split(request, "\n[truncated:")[0]) {
		t.Fatalf("stored request = %q, want the carried long request", request)
	}
	if outcome.RequestBlock != output.RequestBlockCarried {
		t.Fatalf("RequestBlock = %q, want %q", outcome.RequestBlock, output.RequestBlockCarried)
	}
}
