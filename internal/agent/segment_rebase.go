package agent

import "github.com/luispabon/steiner/internal/prompt"

// segmentDecision is the append-or-rebase choice for one segmented compaction.
type segmentDecision struct {
	Rebase              bool
	Reason              string // append | chain-start | invalid-chain | emergency | max-segments | chain-tokens | post-compaction-tokens | no-progress | retry
	Sequence            int    // sequence of the segment this compaction will write
	ChainTokens         int
	ProjectedPostTokens int
	Budget              int
}

// segmentDecisionInput carries everything decideSegmentAction needs to choose
// between appending an immutable segment and rebasing the chain.
type segmentDecisionInput struct {
	Policy              SegmentedCompactionPolicy
	Generation          ConversationGeneration
	Mode                prompt.CompactionMode
	ContextSize         int
	CompactionThreshold int // 0 means unknown, so ContextSize alone bounds the decision
	ChainTokens         int // sum of provider.EstimateMessageTokens over the projected segment messages
	ProjectedPostTokens int
	Retry               bool
}

// decideSegmentAction returns the first matching decision rule, so emergency and
// retry always rebase, and a broken or missing chain rebases before any budget
// rule can append. Percent rules are strict (equal to a limit still appends);
// no-progress fires at equality because a summary as large as the threshold
// cannot buy progress.
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
	case segmentChainStart(in.Generation.SummaryPrefix):
		return rebase("chain-start")
	}

	segments, _, ok := segmentChain(in.Generation)
	if !ok {
		return rebase("invalid-chain")
	}

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

// segmentChainStart reports whether the prefix carries no segment chain yet: it
// is empty, or it holds only legacy plain summaries. segmentChain rejects both,
// so they are classified apart from a malformed chain.
func segmentChainStart(prefix []Message) bool {
	for _, message := range prefix {
		if message.Role != MessageRoleSummary || message.Segment != nil {
			return false
		}
	}
	return true
}
