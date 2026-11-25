package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// maxSummaryAttempts is the empty-summary ladder's call count per main-model
// stage: the first call plus two tail-only retries. A fallback target makes one
// ladder call per stage and never retries it. A reply with text that hit the
// output limit adds at most one condense call per stage (condenseTruncatedSummary),
// outside this budget, on main or a fallback target. Worst case per operation with
// a fallback selected: up to 4 fallback calls (the normal stage, then the emergency
// stage after an applied normal stage), then a main rerun of up to 8 calls (4
// normal, then 4 emergency), so 12 in total. Without a fallback, an operation makes
// up to 8 main calls. An empty normal stage returns its error before the emergency
// stage runs.
const maxSummaryAttempts = 3

// Caps on the tool calls quoted into a final retry. Arguments can be large or
// sensitive, so each is cut short and only the first few calls are quoted.
const (
	maxQuotedToolCalls = 3
	maxQuotedArgChars  = 120
)

// summaryCalls is the accumulated result of the summary calls of one stage.
type summaryCalls struct {
	// response is the reply whose text is the summary. It is set only on success.
	response provider.ChatResponse
	// usage and cacheReported sum every call of the stage, empty replies included.
	usage         *provider.UsageStats
	cacheReported int
	// truncated reports that the adopted summary may be incomplete (hit the output limit).
	truncated bool
	// condensed reports that the adopted summary came from the condense retry.
	condensed bool
	// cleared reports that a call retired the main generation's backend state.
	cleared bool
	// attempts records every reply of the stage in order, with its classification.
	attempts []output.CompactionSummaryAttempt
}

// summarizeCompactionCalls runs the summary calls of one stage. The first call is
// plan.request unchanged. A reply with no summary text is retried with tail-only
// steering: the retry keeps the tools, params, cache key, history and (unless a
// call retired it) the backend state, and only the final instruction changes. The
// failed reply is never replayed. A fallback target gets no retry, so its empty
// reply fails the stage and the operation reruns on main.
func summarizeCompactionCalls(ctx context.Context, summary RunRequest, turn int, plan compactionExecutionPlan, onFallback bool) (summaryCalls, error) {
	maxAttempts := maxSummaryAttempts
	if onFallback {
		maxAttempts = 1
	}
	var calls summaryCalls
	request := plan.request
	for attempt := 1; ; attempt++ {
		// Each main call gets its own retirement signal, so the outcome can tell
		// which call retired the backend state.
		attemptCtx := ctx
		if !onFallback {
			attemptCtx = provider.WithBackendStateRetirement(ctx)
		}
		response, err := completeCompactionCall(attemptCtx, summary, turn, attempt, request, summary.ModelBudget, plan.blocks)
		// A canceled or failed summary can lose its terminal chunk, so the response
		// alone does not report every retirement: read the request signal too.
		calls.cleared = calls.cleared || compactionSummaryRetired(attemptCtx, onFallback, response, err)
		if err != nil {
			return calls, err
		}
		calls.usage = sumUsageStats(calls.usage, response.Usage)
		calls.cacheReported += cacheReportedInputTokens(response.Usage)

		reason := emptySummaryReason(response)
		calls.attempts = append(calls.attempts, summaryAttemptRecord(attempt, response, reason))
		if reason == "" {
			draftIndex := len(calls.attempts) - 1
			if response.FinishReason == "length" {
				if err := condenseTruncatedSummary(ctx, summary, turn, attempt, plan, onFallback, &calls, response); err != nil {
					return calls, err
				}
			} else {
				calls.response = response
			}
			// Only the ladder attempts belong in the note: the condense call is not one.
			noteSummaryReply(summary.Events, turn, calls.attempts[:draftIndex+1], calls.response)
			return calls, nil
		}
		// Retrying a reply that hit the output limit repeats the same limit, so it fails at once.
		if reason == summaryFailLength || attempt >= maxAttempts {
			return calls, &compactionSummaryError{Reason: reason, Attempts: attempt, ToolCalls: len(response.Message.ToolCalls), Details: calls.attempts}
		}

		retry := prompt.CompactionRetry{
			ToolCalls: reason == summaryFailToolCalls,
			Final:     attempt+1 == maxAttempts,
			Attempted: quoteAttemptedToolCalls(response.Message.ToolCalls),
		}
		request = retryCompactionRequest(plan.request, retry, calls.cleared)
		fit, err := summary.ModelBudget.FitCompactionRequest(ctx, request)
		if err != nil {
			return calls, err
		}
		if !fit.Fits {
			return calls, &compactionSummaryError{Reason: summaryFailRetryNoFit, Attempts: attempt, Details: calls.attempts}
		}
	}
}

// emptySummaryReason returns why response has no summary text, or "" when it has
// text. Tool calls with no text are classified before the no-text case.
func emptySummaryReason(response provider.ChatResponse) string {
	if strings.TrimSpace(response.Message.Content) != "" {
		return ""
	}
	switch {
	case response.FinishReason == "length":
		return summaryFailLength
	case len(response.Message.ToolCalls) > 0:
		return summaryFailToolCalls
	default:
		return summaryFailNoText
	}
}

// retryCompactionRequest copies base with its final instruction replaced by the
// instruction plus the retry steering. Base is the first call's request, so the
// steering never stacks on an earlier retry. A retired backend state is not resent:
// the retry is then a full reseed from the messages.
func retryCompactionRequest(base provider.ChatRequest, retry prompt.CompactionRetry, retired bool) provider.ChatRequest {
	out := base
	out.Messages = provider.CloneMessages(base.Messages)
	last := &out.Messages[len(out.Messages)-1]
	last.Content += "\n\n" + prompt.RenderCompactionRetryInstruction(retry)
	if retired {
		out.BackendState = nil
	}
	return out
}

// quoteAttemptedToolCalls quotes up to maxQuotedToolCalls tool calls as a name
// and a capped argument excerpt.
func quoteAttemptedToolCalls(calls []provider.ToolCall) []string {
	if len(calls) > maxQuotedToolCalls {
		calls = calls[:maxQuotedToolCalls]
	}
	quoted := make([]string, 0, len(calls))
	for _, call := range calls {
		args := call.RawArguments
		if args == "" {
			encoded, err := json.Marshal(call.Arguments)
			if err != nil {
				args = fmt.Sprintf("<arguments not encodable: %v>", err)
			} else {
				args = string(encoded)
			}
		}
		quoted = append(quoted, call.Name+" "+summarizeTextPreview(args, maxQuotedArgChars))
	}
	return quoted
}

// summaryAttemptRecord records one reply as the provider sent it, with the stage's
// classification. CompletionTokens stays nil when the provider reported no usage, so
// a reported zero is kept apart from no report.
func summaryAttemptRecord(attempt int, response provider.ChatResponse, reason string) output.CompactionSummaryAttempt {
	record := output.CompactionSummaryAttempt{
		Attempt:          attempt,
		FinishReason:     response.FinishReason,
		ToolCalls:        len(response.Message.ToolCalls),
		ReasoningContent: response.Message.ReasoningContent != "",
		Reason:           reason,
	}
	if response.Usage != nil {
		completion := response.Usage.CompletionTokens
		record.CompletionTokens = &completion
	}
	return record
}

// noteSummaryReply reports a recovered retry with each failed attempt's details, and
// tool calls that came with the summary text. Both are notes: the stage still succeeds.
func noteSummaryReply(sink output.EventSink, turn int, attempts []output.CompactionSummaryAttempt, response provider.ChatResponse) {
	var notes []string
	if n := len(attempts); n > 1 {
		notes = append(notes, fmt.Sprintf("compaction summary recovered on attempt %d of %d", n, maxSummaryAttempts))
		for _, failed := range attempts[:n-1] {
			notes = append(notes, "failed summary attempt: "+failed.String())
		}
	}
	if n := len(response.Message.ToolCalls); n > 0 {
		notes = append(notes, fmt.Sprintf("compaction reply carried %d tool call(s) with its text; the tool calls were ignored", n))
	}
	if len(notes) > 0 {
		emitCompactionNote(sink, turn, notes)
	}
}

// emitCompactionNote reports notes while a compaction is still running. Its
// compacting severity keeps the TUI in the compacting state, as the fallback
// announcement does.
func emitCompactionNote(sink output.EventSink, turn int, notes []string) {
	emitEvent(sink, output.NewContextDiagnosticsEvent(output.ContextDiagnosticsEvent{
		Kind:     "compaction",
		Scope:    "conversation",
		Turn:     turn,
		Severity: "compacting",
		Notes:    notes,
	}))
}
