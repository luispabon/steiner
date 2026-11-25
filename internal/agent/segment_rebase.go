package agent

import "github.com/luispabon/steiner/internal/prompt"

// segmentDecision is the append-or-rebase choice for one segmented compaction.
type segmentDecision struct {
	Rebase              bool
	Reason              string // append | chain-start | invalid-chain | split-in-chain | empty-span | emergency | max-segments | chain-tokens | post-compaction-tokens | no-progress | retry
	Sequence            int    // sequence of the segment this compaction will write
	ChainTokens         int
	ProjectedPostTokens int
	Budget              int
}

// segmentDecisionInput carries everything decideSegmentAction needs to choose
// between appending an immutable segment and rebasing the chain.
type segmentDecisionInput struct {
	Policy              SegmentedCompactionPolicy
	Chain               segmentChainScan
	Mode                prompt.CompactionMode
	ContextSize         int
	CompactionThreshold int // 0 means unknown, so ContextSize alone bounds the decision
	ChainTokens         int // sum of provider.EstimateMessageTokens over the projected segment messages
	ProjectedPostTokens int
	RetentionStart      int // index into the scanned slice where retained messages begin
	// AppendSourceMessages counts the non-summary messages in the append span
	// FullMessages()[Chain.End:RetentionStart]. Zero means the next segment
	// would summarize no new message.
	AppendSourceMessages int
	Retry                bool
}

// decideSegmentAction returns the first matching decision rule, so emergency and
// retry always rebase, and a broken or missing chain rebases before any budget
// rule can append. The append source never starts inside the chain: a retention
// start before the end of the leading summary run rebases. An append span with
// no non-summary message also rebases, before any budget rule can append an
// empty segment. Percent rules are
// strict (equal to a limit still appends); no-progress fires at equality because
// a summary as large as the threshold cannot buy progress.
func decideSegmentAction(in segmentDecisionInput) segmentDecision {
	budget := in.ContextSize
	if in.CompactionThreshold > 0 {
		budget = min(budget, in.CompactionThreshold)
	}

	decision := segmentDecision{
		Reason:              "append",
		Sequence:            1,
		ChainTokens:         in.ChainTokens,
		ProjectedPostTokens: in.ProjectedPostTokens,
		Budget:              budget,
	}
	rebase := func(reason string) segmentDecision {
		decision.Rebase = true
		decision.Reason = reason
		return decision
	}

	switch {
	case in.Mode == prompt.CompactionModeEmergency:
		return rebase("emergency")
	case in.Retry:
		return rebase("retry")
	case in.Chain.Status == chainNone:
		return rebase("chain-start")
	case in.Chain.Status == chainInvalid:
		return rebase("invalid-chain")
	case in.RetentionStart < in.Chain.End:
		return rebase("split-in-chain")
	case in.AppendSourceMessages == 0:
		return rebase("empty-span")
	}

	segments := in.Chain.Segments
	switch {
	case len(segments)+1 > in.Policy.MaxSegments:
		return rebase("max-segments")
	case float64(in.ChainTokens) > float64(budget)*in.Policy.MaxChainPercent/100:
		return rebase("chain-tokens")
	case float64(in.ProjectedPostTokens) > float64(budget)*in.Policy.MaxPostCompactionPercent/100:
		return rebase("post-compaction-tokens")
	case in.CompactionThreshold > 0 && in.ProjectedPostTokens >= in.CompactionThreshold:
		return rebase("no-progress")
	}

	decision.Sequence = len(segments) + 1
	return decision
}
