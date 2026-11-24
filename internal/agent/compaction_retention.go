package agent

import (
	"context"

	"github.com/luispabon/steiner/internal/prompt"
)

// selectRetention returns the largest retained tail of 1 to maxUnits units that
// fits req's main budget with its latest request block and the summary reserve
// for mode. Selection is pure computation: it issues no model call. It returns
// the source and retained messages, and ok. When no tail fits, ok is false and the
// source and retained messages are the one-unit candidate, for the too-large breakdown.
func selectRetention(ctx context.Context, req RunRequest, state RunState, full, base []Message, mode prompt.CompactionMode, maxUnits int) ([]Message, []Message, bool, error) {
	var source, retained []Message
	for units := maxUnits; units >= 1; units-- {
		source, retained = compactionSourceAndRetention(full, base, units)
		fits, err := tailFitsWithBlock(ctx, req, state, source, retained, mode)
		if err != nil {
			return nil, nil, false, err
		}
		if fits {
			return source, retained, true, nil
		}
	}
	return source, retained, false, nil
}

// selectEmergencyRetention returns the one-unit emergency tail of full. When that
// unit does not fit with its request block, it is kept without the block if that
// fits: the stage then drops the block if its summary still misses the budget. It
// returns a compactionBudgetMissError wrapping compactionTailTooLargeError when the
// unit does not fit even without it.
func selectEmergencyRetention(ctx context.Context, req RunRequest, state RunState, full, base []Message) ([]Message, []Message, error) {
	source, retained, ok, err := selectRetention(ctx, req, state, full, base, prompt.CompactionModeEmergency, emergencyCompactionRetainTurns)
	if err != nil {
		return nil, nil, err
	}
	if ok {
		return source, retained, nil
	}
	// A nil source carries no request block, so this is the plain fit check.
	fitsWithoutBlock, err := tailFitsWithBlock(ctx, req, state, nil, retained, prompt.CompactionModeEmergency)
	if err != nil {
		return nil, nil, err
	}
	if fitsWithoutBlock {
		return source, retained, nil
	}
	tooLarge, err := newCompactionTailTooLargeError(ctx, req, state, source, retained, prompt.CompactionModeEmergency)
	if err != nil {
		return nil, nil, err
	}
	return nil, nil, &compactionBudgetMissError{err: tooLarge}
}

// tailFitsWithBlock reports whether retained fits req's hard limit with the latest
// request block of source and the summary reserve for mode added. The plain
// retained fit must also pass: it is the guard summarizeCompactionStage applies,
// so a selected tail never trips that guard.
func tailFitsWithBlock(ctx context.Context, req RunRequest, state RunState, source, retained []Message, mode prompt.CompactionMode) (bool, error) {
	plain, err := fitConversationState(ctx, req, state.WithConversation(retained))
	if err != nil {
		return false, err
	}
	if plain.ContextSize <= 0 {
		// With no known window there is no hard limit to fit.
		return true, nil
	}
	if !plain.Fits {
		return false, nil
	}
	withBlock, err := fitWithRequestBlock(ctx, req, state, source, retained)
	if err != nil {
		return false, err
	}
	reserve := compactionSummaryMaxTokensForMode(req.ModelBudget, mode)
	return withBlock.EstimatedPromptTokens+reserve+plain.SafetyMarginTokens <= plain.ContextSize, nil
}

// fitWithRequestBlock fits retained plus the latest request block of source. The
// summary carries that block as a trailer, so it adds to the prompt once applied.
func fitWithRequestBlock(ctx context.Context, req RunRequest, state RunState, source, retained []Message) (prompt.RequestTokenBudget, error) {
	conversation := cloneMessages(retained)
	if block, _ := latestRequestBlock(source, req.ModelBudget.ContextSize); block != "" {
		// Estimated as a user message: the trailer's tokens, not its exact placement.
		conversation = append(conversation, Message{Role: MessageRoleUser, Content: block})
	}
	return fitConversationState(ctx, req, state.WithConversation(conversation))
}

// newCompactionTailTooLargeError measures the breakdown of the one-unit tail that
// does not fit mode's budget. Each part is a fit delta: protected outputs and the
// rest of the tail against the tail without them, and the request block against
// the tail alone. It runs only when the error is built.
func newCompactionTailTooLargeError(ctx context.Context, req RunRequest, state RunState, source, retained []Message, mode prompt.CompactionMode) (*compactionTailTooLargeError, error) {
	tail, err := fitConversationState(ctx, req, state.WithConversation(retained))
	if err != nil {
		return nil, err
	}
	retainedTokens, protectedTokens, err := compactionTailTokens(ctx, req, state, retained, tail)
	if err != nil {
		return nil, err
	}
	withBlock, err := fitWithRequestBlock(ctx, req, state, source, retained)
	if err != nil {
		return nil, err
	}
	return &compactionTailTooLargeError{
		Fit:                  tail,
		ProtectedTokens:      protectedTokens,
		RetainedTokens:       retainedTokens,
		RequestBlockTokens:   withBlock.EstimatedPromptTokens - tail.EstimatedPromptTokens,
		SummaryReserveTokens: compactionSummaryMaxTokensForMode(req.ModelBudget, mode),
	}, nil
}

// compactionTailTokens measures a retained tail as fit deltas, the measure
// compactionTailTooLargeError reports. tail is the fit of retained. It returns the
// tokens of retained without the newest unit's protected outputs, and those
// protected outputs alone.
func compactionTailTokens(ctx context.Context, req RunRequest, state RunState, retained []Message, tail prompt.RequestTokenBudget) (int, int, error) {
	empty, err := fitConversationState(ctx, req, state.WithConversation(nil))
	if err != nil {
		return 0, 0, err
	}
	unit := newestCompactionUnit(retained)
	withoutProtected := append(cloneMessages(retained[:len(retained)-len(unit)]), withoutProtectedOutputs(unit)...)
	unprotected, err := fitConversationState(ctx, req, state.WithConversation(withoutProtected))
	if err != nil {
		return 0, 0, err
	}
	return unprotected.EstimatedPromptTokens - empty.EstimatedPromptTokens, tail.EstimatedPromptTokens - unprotected.EstimatedPromptTokens, nil
}

// newestCompactionUnit returns the last unit of messages, or nil when it has none.
func newestCompactionUnit(messages []Message) []Message {
	turns := splitCompactionTurns(messages)
	if len(turns) == 0 {
		return nil
	}
	return turns[len(turns)-1]
}

// withoutProtectedOutputs returns unit without its protected outputs: the tool and
// sub-agent results after its last assistant message. The model has not read those
// yet, so compaction cannot summarize them away.
func withoutProtectedOutputs(unit []Message) []Message {
	start := 0
	for i := len(unit) - 1; i >= 0; i-- {
		if unit[i].Role == MessageRoleAssistant {
			start = i + 1
			break
		}
	}
	out := cloneMessages(unit[:start])
	for _, message := range unit[start:] {
		if message.Role != MessageRoleTool && message.Source != MessageSourceSubAgentResult {
			out = append(out, message)
		}
	}
	return out
}
