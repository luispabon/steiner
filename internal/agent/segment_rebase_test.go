package agent

import (
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
)

// segmentChainMessages builds count valid segments followed by an optional
// segment-free tail and one live turn.
func segmentChainMessages(count int, tail bool) []Message {
	messages := make([]Message, 0, count+2)
	for i := 1; i <= count; i++ {
		messages = append(messages, Message{
			Role:    MessageRoleSummary,
			Content: segmentHeader(i),
			Segment: &SummarySegment{Sequence: i, SourceMessageCount: 2},
		})
	}
	if tail {
		messages = append(messages, Message{Role: MessageRoleSummary, Content: "mutable tail"})
	}
	messages = append(messages, Message{Role: MessageRoleUser, Content: "current request"})
	return messages
}

func TestDecideSegmentAction(t *testing.T) {
	policy := SegmentedCompactionPolicy{MaxSegments: 4, MaxChainPercent: 12.5, MaxPostCompactionPercent: 50}
	// Default input: budget 8000 (min of 20000 and the 8000 threshold), chain
	// limit 1000, post-compaction limit 4000, retention starting after the chain.
	base := func() segmentDecisionInput {
		messages := segmentChainMessages(2, true)
		return segmentDecisionInput{
			Policy:              policy,
			Chain:               scanSegmentChain(messages),
			Mode:                prompt.CompactionModeNormal,
			ContextSize:         20000,
			CompactionThreshold: 8000,
			ChainTokens:         900,
			ProjectedPostTokens: 3000,
			RetentionStart:      len(messages) - 1,
		}
	}
	noProgressInput := func() segmentDecisionInput {
		in := base()
		in.Policy.MaxPostCompactionPercent = 100
		in.ChainTokens = 500
		in.ProjectedPostTokens = 8000
		return in
	}
	appendDecision := func(sequence, chain, post, budget int) segmentDecision {
		return segmentDecision{Reason: "append", Sequence: sequence, ChainTokens: chain, ProjectedPostTokens: post, Budget: budget}
	}
	rebaseDecision := func(reason string, chain, post, budget int) segmentDecision {
		return segmentDecision{Rebase: true, Reason: reason, Sequence: 1, ChainTokens: chain, ProjectedPostTokens: post, Budget: budget}
	}
	invalidChain := func(messages []Message) segmentDecisionInput {
		in := base()
		in.Chain = scanSegmentChain(messages)
		return in
	}

	tests := []struct {
		name  string
		input segmentDecisionInput
		want  segmentDecision
	}{
		{
			name:  "below every limit appends the next segment",
			input: base(),
			want:  appendDecision(3, 900, 3000, 8000),
		},
		{
			name: "emergency mode rebases",
			input: func() segmentDecisionInput {
				in := base()
				in.Mode = prompt.CompactionModeEmergency
				return in
			}(),
			want: rebaseDecision("emergency", 900, 3000, 8000),
		},
		{
			name: "retry rebases",
			input: func() segmentDecisionInput {
				in := base()
				in.Retry = true
				return in
			}(),
			want: rebaseDecision("retry", 900, 3000, 8000),
		},
		{
			name: "emergency wins over retry",
			input: func() segmentDecisionInput {
				in := base()
				in.Mode = prompt.CompactionModeEmergency
				in.Retry = true
				return in
			}(),
			want: rebaseDecision("emergency", 900, 3000, 8000),
		},
		{
			name: "retry wins over a broken chain",
			input: func() segmentDecisionInput {
				in := base()
				in.Retry = true
				in.Chain = scanSegmentChain(nil)
				return in
			}(),
			want: rebaseDecision("retry", 900, 3000, 8000),
		},
		{
			name: "retry wins over a retention split inside the chain",
			input: func() segmentDecisionInput {
				in := base()
				in.Retry = true
				in.RetentionStart = 1
				return in
			}(),
			want: rebaseDecision("retry", 900, 3000, 8000),
		},
		{
			name: "empty conversation starts a chain",
			input: func() segmentDecisionInput {
				in := base()
				in.Chain = scanSegmentChain(nil)
				return in
			}(),
			want: rebaseDecision("chain-start", 900, 3000, 8000),
		},
		{
			name: "legacy plain summary starts a chain",
			input: func() segmentDecisionInput {
				in := base()
				in.Chain = scanSegmentChain([]Message{{Role: MessageRoleSummary, Content: "plain"}})
				return in
			}(),
			want: rebaseDecision("chain-start", 900, 3000, 8000),
		},
		{
			name: "chain start wins over max segments",
			input: func() segmentDecisionInput {
				in := base()
				in.Chain = scanSegmentChain(nil)
				in.Policy.MaxSegments = 1
				return in
			}(),
			want: rebaseDecision("chain-start", 900, 3000, 8000),
		},
		{
			name: "chain start wins over a retention split inside the chain",
			input: func() segmentDecisionInput {
				in := base()
				in.Chain = scanSegmentChain(nil)
				in.RetentionStart = 0
				return in
			}(),
			want: rebaseDecision("chain-start", 900, 3000, 8000),
		},
		{
			name: "sequence gap is an invalid chain",
			input: invalidChain([]Message{
				{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 1, SourceMessageCount: 2}},
				{Role: MessageRoleSummary, Content: "three", Segment: &SummarySegment{Sequence: 3, SourceMessageCount: 2}},
			}),
			want: rebaseDecision("invalid-chain", 900, 3000, 8000),
		},
		{
			name: "segment after a plain summary is an invalid chain",
			input: invalidChain([]Message{
				{Role: MessageRoleSummary, Content: "plain"},
				{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 1, SourceMessageCount: 2}},
			}),
			want: rebaseDecision("invalid-chain", 900, 3000, 8000),
		},
		{
			name: "zero source message count is an invalid chain",
			input: invalidChain([]Message{
				{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 1, SourceMessageCount: 0}},
			}),
			want: rebaseDecision("invalid-chain", 900, 3000, 8000),
		},
		{
			name: "invalid chain wins over max segments",
			input: func() segmentDecisionInput {
				in := invalidChain([]Message{
					{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 3, SourceMessageCount: 2}},
				})
				in.Policy.MaxSegments = 1
				return in
			}(),
			want: rebaseDecision("invalid-chain", 900, 3000, 8000),
		},
		{
			name: "invalid chain wins over a retention split inside the chain",
			input: func() segmentDecisionInput {
				in := invalidChain([]Message{
					{Role: MessageRoleSummary, Content: "one", Segment: &SummarySegment{Sequence: 2, SourceMessageCount: 2}},
				})
				in.RetentionStart = 0
				return in
			}(),
			want: rebaseDecision("invalid-chain", 900, 3000, 8000),
		},
		{
			name: "retention inside the chain rebases with split-in-chain",
			input: func() segmentDecisionInput {
				in := base()
				in.RetentionStart = 1
				return in
			}(),
			want: rebaseDecision("split-in-chain", 900, 3000, 8000),
		},
		{
			name: "retention inside the tail rebases with split-in-chain",
			input: func() segmentDecisionInput {
				in := base()
				in.RetentionStart = 2
				return in
			}(),
			want: rebaseDecision("split-in-chain", 900, 3000, 8000),
		},
		{
			name: "retention at the chain end appends",
			input: func() segmentDecisionInput {
				in := base()
				in.RetentionStart = 3
				return in
			}(),
			want: appendDecision(3, 900, 3000, 8000),
		},
		{
			name: "split-in-chain wins over max segments",
			input: func() segmentDecisionInput {
				in := base()
				in.RetentionStart = 1
				in.Policy.MaxSegments = 2
				return in
			}(),
			want: rebaseDecision("split-in-chain", 900, 3000, 8000),
		},
		{
			name: "max segments boundary still appends",
			input: func() segmentDecisionInput {
				in := base()
				in.Policy.MaxSegments = 3
				return in
			}(),
			want: appendDecision(3, 900, 3000, 8000),
		},
		{
			name: "max segments exceeded rebases",
			input: func() segmentDecisionInput {
				in := base()
				in.Policy.MaxSegments = 2
				return in
			}(),
			want: rebaseDecision("max-segments", 900, 3000, 8000),
		},
		{
			name: "max segments wins over chain tokens",
			input: func() segmentDecisionInput {
				in := base()
				in.Policy.MaxSegments = 2
				in.ChainTokens = 5000
				return in
			}(),
			want: rebaseDecision("max-segments", 5000, 3000, 8000),
		},
		{
			name: "chain tokens at the limit append",
			input: func() segmentDecisionInput {
				in := base()
				in.ChainTokens = 1000
				return in
			}(),
			want: appendDecision(3, 1000, 3000, 8000),
		},
		{
			name: "chain tokens above the limit rebase",
			input: func() segmentDecisionInput {
				in := base()
				in.ChainTokens = 1001
				return in
			}(),
			want: rebaseDecision("chain-tokens", 1001, 3000, 8000),
		},
		{
			name: "chain tokens beat post-compaction tokens",
			input: func() segmentDecisionInput {
				in := base()
				in.ChainTokens = 1001
				in.ProjectedPostTokens = 9000
				return in
			}(),
			want: rebaseDecision("chain-tokens", 1001, 9000, 8000),
		},
		{
			name: "post-compaction tokens at the limit append",
			input: func() segmentDecisionInput {
				in := base()
				in.ProjectedPostTokens = 4000
				return in
			}(),
			want: appendDecision(3, 900, 4000, 8000),
		},
		{
			name: "post-compaction tokens above the limit rebase",
			input: func() segmentDecisionInput {
				in := base()
				in.ProjectedPostTokens = 4001
				return in
			}(),
			want: rebaseDecision("post-compaction-tokens", 900, 4001, 8000),
		},
		{
			name: "post-compaction tokens win over no-progress",
			input: func() segmentDecisionInput {
				in := noProgressInput()
				in.ProjectedPostTokens = 8001
				return in
			}(),
			want: rebaseDecision("post-compaction-tokens", 500, 8001, 8000),
		},
		{
			name:  "no-progress fires at the threshold",
			input: noProgressInput(),
			want:  rebaseDecision("no-progress", 500, 8000, 8000),
		},
		{
			name: "below the no-progress threshold appends",
			input: func() segmentDecisionInput {
				in := noProgressInput()
				in.ProjectedPostTokens = 7999
				return in
			}(),
			want: appendDecision(3, 500, 7999, 8000),
		},
		{
			name: "no-progress is ignored without a threshold",
			input: func() segmentDecisionInput {
				in := noProgressInput()
				in.CompactionThreshold = 0
				return in
			}(),
			want: appendDecision(3, 500, 8000, 20000),
		},
		{
			name: "unknown threshold uses the context size as budget",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 8000
				in.CompactionThreshold = 0
				in.ChainTokens = 1001
				in.ProjectedPostTokens = 0
				return in
			}(),
			want: rebaseDecision("chain-tokens", 1001, 0, 8000),
		},
		{
			name: "context size wins when it is the smaller budget",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 4000
				in.ChainTokens = 501
				return in
			}(),
			want: rebaseDecision("chain-tokens", 501, 3000, 4000),
		},
		{
			name: "fractional chain limit at equality appends",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 1001
				in.CompactionThreshold = 0
				in.ChainTokens = 125
				in.ProjectedPostTokens = 0
				return in
			}(),
			want: appendDecision(3, 125, 0, 1001),
		},
		{
			name: "fractional chain limit above equality rebases",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 1001
				in.CompactionThreshold = 0
				in.ChainTokens = 126
				in.ProjectedPostTokens = 0
				return in
			}(),
			want: rebaseDecision("chain-tokens", 126, 0, 1001),
		},
		{
			name: "fractional post-compaction limit at equality appends",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 1001
				in.CompactionThreshold = 0
				in.ChainTokens = 0
				in.ProjectedPostTokens = 500
				return in
			}(),
			want: appendDecision(3, 0, 500, 1001),
		},
		{
			name: "fractional post-compaction limit above equality rebases",
			input: func() segmentDecisionInput {
				in := base()
				in.ContextSize = 1001
				in.CompactionThreshold = 0
				in.ChainTokens = 0
				in.ProjectedPostTokens = 501
				return in
			}(),
			want: rebaseDecision("post-compaction-tokens", 0, 501, 1001),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decideSegmentAction(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("decideSegmentAction() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
