package agent

import (
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/prompt"
)

// Reasons a segment decision reports. LogSegmentDecision prints these values
// verbatim, so they are also the compaction-bench log vocabulary.
const (
	segmentReasonAppend               = "append"
	segmentReasonChainStart           = "chain-start"
	segmentReasonInvalidChain         = "invalid-chain"
	segmentReasonSplitInChain         = "split-in-chain"
	segmentReasonRetentionUnmapped    = "retention-unmapped"
	segmentReasonStrippedView         = "stripped-view"
	segmentReasonEmptySpan            = "empty-span"
	segmentReasonEmptySource          = "empty-source"
	segmentReasonEmergency            = "emergency"
	segmentReasonMaxSegments          = "max-segments"
	segmentReasonChainTokens          = "chain-tokens"
	segmentReasonPostCompactionTokens = "post-compaction-tokens"
	segmentReasonNoProgress           = "no-progress"
	segmentReasonRetry                = "retry"
)

// segmentDecision is the append-or-rebase choice for one segmented compaction.
type segmentDecision struct {
	Rebase bool
	// Plain marks the plain summary fallback: no segment metadata is stored, so
	// the summary keeps the flag-off shape. Rebase stays set with it.
	Plain bool
	// Reason names the rule that produced the decision.
	Reason string
	// Sequence is the segment this compaction writes, 0 for a plain fallback.
	Sequence            int
	ChainTokens         int
	ProjectedPostTokens int
	Budget              int
}

// segmentDecisionInput carries everything decideSegmentAction needs to choose
// between appending an immutable segment and rebasing the chain.
type segmentDecisionInput struct {
	Policy              config.SegmentedCompactionPolicy
	Chain               segmentChainScan
	Mode                prompt.CompactionMode
	ContextSize         int
	CompactionThreshold int // 0 means unknown, so ContextSize alone bounds the decision
	ChainTokens         int // sum of provider.EstimateMessageTokens over the projected segment messages
	ProjectedPostTokens int
	RetentionStart      int // index into the scanned slice where retained messages begin
	// AppendSourceMessages counts the messages the next segment would summarize
	// in the append span FullMessages()[Chain.End:RetentionStart], so a span
	// holding only the active skill replay counts zero.
	AppendSourceMessages int
	Retry                bool
}

// decideSegmentAction returns the first matching decision rule, so emergency and
// retry always rebase, and a broken or missing chain rebases before any budget
// rule can append. The append source never starts inside the chain: a retention
// start before the end of the leading summary run rebases. An append span with
// nothing to summarize, such as a span holding only the replayed skill, also
// rebases before any budget rule can append an empty segment. Percent rules are
// strict (equal to a limit still appends); no-progress fires at equality because
// a summary as large as the threshold cannot buy progress.
func decideSegmentAction(in segmentDecisionInput) segmentDecision {
	budget := in.ContextSize
	if in.CompactionThreshold > 0 {
		budget = min(budget, in.CompactionThreshold)
	}

	decision := segmentDecision{
		Reason:              segmentReasonAppend,
		Sequence:            1,
		ChainTokens:         in.ChainTokens,
		ProjectedPostTokens: in.ProjectedPostTokens,
		Budget:              budget,
	}
	rebase := func(reason string) segmentDecision {
		return segmentRebaseDecision(decision, reason, 1)
	}

	switch {
	case in.Mode == prompt.CompactionModeEmergency:
		return rebase(segmentReasonEmergency)
	case in.Retry:
		return rebase(segmentReasonRetry)
	case in.Chain.Status == chainNone:
		return rebase(segmentReasonChainStart)
	case in.Chain.Status == chainInvalid:
		return rebase(segmentReasonInvalidChain)
	case in.RetentionStart < in.Chain.End:
		return rebase(segmentReasonSplitInChain)
	case in.AppendSourceMessages == 0:
		return rebase(segmentReasonEmptySpan)
	}

	segments := in.Chain.Segments
	switch {
	case len(segments)+1 > in.Policy.MaxSegments:
		return rebase(segmentReasonMaxSegments)
	case float64(in.ChainTokens) > float64(budget)*in.Policy.MaxChainPercent/100:
		return rebase(segmentReasonChainTokens)
	case float64(in.ProjectedPostTokens) > float64(budget)*in.Policy.MaxPostCompactionPercent/100:
		return rebase(segmentReasonPostCompactionTokens)
	case in.CompactionThreshold > 0 && in.ProjectedPostTokens >= in.CompactionThreshold:
		return rebase(segmentReasonNoProgress)
	}

	decision.Sequence = len(segments) + 1
	return decision
}
