package agent

import (
	"context"
	"errors"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// summaryTruncated is the attempt Reason for a reply with text that hit the
// output limit. Such a reply is a draft: the condense retry asks for it again,
// shorter, and the draft is adopted if that retry does not produce a summary.
const summaryTruncated = "truncated"

// summaryTruncatedMarker ends a summary body built from a reply that hit the
// output limit, so a reader knows later sections may be missing. It is appended
// at compaction time only, before the request block.
const summaryTruncatedMarker = "[Summary truncated at the output token limit; later sections may be missing.]"

// condenseTargetWords is the approximate word budget for a condense rewrite of a
// reply cut off at the output limit. It is a soft size hint derived from the
// call's token limit, which is never lowered.
func condenseTargetWords(maxTokens *int) int {
	if maxTokens == nil || *maxTokens <= 0 {
		return 0
	}
	return max(1, *maxTokens*8/10*3/4)
}

// condenseTruncatedSummary retries a summary reply that carried text but hit the
// output limit, asking for the same summary shorter. The draft is quoted in the
// appended instruction, never replayed as an assistant message, and the call
// keeps the stage's request and token limit. A condense call that does not fit,
// fails or returns no text adopts the draft, with a note; cancellation is
// returned to the caller.
func condenseTruncatedSummary(ctx context.Context, summary RunRequest, turn, attempt int, plan compactionExecutionPlan, onFallback bool, calls *summaryCalls, draft provider.ChatResponse) error {
	// The loop appended the draft's record; the condense call is not a ladder attempt.
	calls.attempts[len(calls.attempts)-1].Reason = summaryTruncated

	retry := prompt.CompactionRetry{
		Condense:    true,
		TargetWords: condenseTargetWords(plan.request.MaxTokens),
		Draft:       escapeCompactionTags(strings.TrimSpace(draft.Message.Content), truncatedSummaryTagPattern),
	}
	request := retryCompactionRequest(plan.request, retry, calls.cleared)
	fit, err := summary.ModelBudget.FitCompactionRequest(ctx, request)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return err
		}
		adoptTruncatedDraft(calls, draft, summary.Events, turn, "compaction summary hit the output limit; condense retry did not fit, adopted the truncated summary")
		return nil
	}
	if !fit.Fits {
		adoptTruncatedDraft(calls, draft, summary.Events, turn, "compaction summary hit the output limit; condense retry did not fit, adopted the truncated summary")
		return nil
	}

	// Same per-attempt retirement rule as the ladder: a fallback call reports no
	// retirement, and only a main call carries its own signal.
	attemptCtx := ctx
	if !onFallback {
		attemptCtx = provider.WithBackendStateRetirement(ctx)
	}
	response, err := completeCompactionCall(attemptCtx, summary, turn, attempt+1, request, summary.ModelBudget, plan.blocks)
	calls.cleared = calls.cleared || compactionSummaryRetired(attemptCtx, onFallback, response, err)
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			return err
		}
		adoptTruncatedDraft(calls, draft, summary.Events, turn, "compaction summary hit the output limit; condense retry failed ("+provider.ErrorClass(ctx, err)+"), adopted the truncated summary")
		return nil
	}
	calls.usage = sumUsageStats(calls.usage, response.Usage)
	calls.cacheReported += cacheReportedInputTokens(response.Usage)

	emptyReason := emptySummaryReason(response)
	reason := emptyReason
	if reason == "" && response.FinishReason == "length" {
		reason = summaryTruncated
	}
	calls.attempts = append(calls.attempts, summaryAttemptRecord(attempt+1, response, reason))
	if emptyReason != "" {
		adoptTruncatedDraft(calls, draft, summary.Events, turn, "compaction summary hit the output limit; condense retry returned no summary ("+emptyReason+"), adopted the truncated summary")
		return nil
	}
	calls.response = response
	calls.condensed = true
	calls.truncated = response.FinishReason == "length"
	return nil
}

// adoptTruncatedDraft keeps the truncated draft as the stage's summary and says
// why the condense retry did not replace it.
func adoptTruncatedDraft(calls *summaryCalls, draft provider.ChatResponse, sink output.EventSink, turn int, note string) {
	calls.response = draft
	calls.truncated = true
	emitCompactionNote(sink, turn, []string{note})
}
