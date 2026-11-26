package agent

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// seamChainPolicy is the experiment policy the seam tests share: enabled, with
// loose thresholds so only the chain's own validity decides append or rebase.
func seamChainPolicy() SegmentedCompactionPolicy {
	policy := segmentedCompactionPolicy()
	policy.MaxSegments = 32
	policy.MaxChainPercent = 100
	policy.MaxPostCompactionPercent = 100
	return policy
}

// seamTurn returns one short user and assistant turn.
func seamTurn(n int) []Message {
	return []Message{
		{Role: MessageRoleUser, Content: fmt.Sprintf("seam turn %d request", n)},
		{Role: MessageRoleAssistant, Content: fmt.Sprintf("seam turn %d reply", n)},
	}
}

// seamTurns returns count turns starting at first.
func seamTurns(first, count int) []Message {
	messages := make([]Message, 0, 2*count)
	for i := range count {
		messages = append(messages, seamTurn(first+i)...)
	}
	return messages
}

// seamAppend adds messages through the state's lineage, keeping Conversation in
// step the way a run appends turn output.
func seamAppend(state RunState, messages ...Message) RunState {
	state.Lineage = state.Lineage.WithAppendedMessages(messages)
	state.Conversation = state.Lineage.FullMessages()
	return state
}

// seamCompact runs one compaction through the runner's candidate path and
// requires it to apply.
func seamCompact(t *testing.T, req RunRequest, state RunState, turn int) RunState {
	t.Helper()
	applied, ok, _, err := (&Runner{}).compactConversationForBudgetWithSteering(context.Background(), req, &state, turn, nil, map[string]bool{}, new(int), "")
	if err != nil {
		t.Fatalf("compaction turn %d error = %v", turn, err)
	}
	if !applied || !ok {
		t.Fatalf("compaction turn %d applied = %t ok = %t, want an applied compaction", turn, applied, ok)
	}
	return state
}

// seamChainFixture is the chain every seam starts from: a persisted lineage
// holding segments 1 and 2 plus a request-block tail.
type seamChainFixture struct {
	lineage ConversationLineage
	prefix  []Message
}

// seamBuildChain builds segments 1 and 2 through real compactions: a chain start
// over four turns, then an append after one more turn.
func seamBuildChain(t *testing.T, providerStub *fakeProvider) seamChainFixture {
	t.Helper()
	req := segmentedCompactionRequest(providerStub, seamChainPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "seam-chain.log")

	state := stateFromMessages(seamTurns(1, 4))
	state = seamCompact(t, req, state, 1)
	state = seamAppend(state, seamTurn(5)...)
	state = seamCompact(t, req, state, 2)

	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	prefix := latest.SummaryPrefix
	if got, want := len(prefix), 3; got != want {
		t.Fatalf("chain prefix len = %d, want %d (two segments and a tail)", got, want)
	}
	wantSegments := []Message{
		{Role: MessageRoleSummary, Content: segmentHeader(1) + "seam segment one facts", Segment: testSegment(1, 2)},
		{Role: MessageRoleSummary, Content: segmentHeader(2) + "seam segment two facts", Segment: testSegment(2, 2)},
	}
	if got, want := mustJSON(t, prefix[:2]), mustJSON(t, wantSegments); !bytes.Equal(got, want) {
		t.Fatalf("chain segments = %s, want %s", got, want)
	}
	if prefix[2].Segment != nil {
		t.Fatalf("chain tail carries segment metadata: %+v", prefix[2].Segment)
	}
	if _, block, ok := SplitRequestBlock(prefix[2].Content); !ok || block != "seam turn 2 request" {
		t.Fatalf("chain tail body = %q, want the second turn's request", prefix[2].Content)
	}

	wantLines := []string{
		"segment_decision action=rebase reason=chain-start sequence=1",
		"segment_decision action=append reason=append sequence=2",
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	if len(lines) != len(wantLines) {
		t.Fatalf("chain segment_decision lines = %v, want %d", lines, len(wantLines))
	}
	for i, want := range wantLines {
		if !strings.HasPrefix(lines[i], want) {
			t.Fatalf("chain segment_decision line %d = %q, want prefix %q", i, lines[i], want)
		}
	}
	return seamChainFixture{lineage: state.Lineage.Clone(), prefix: cloneMessages(prefix)}
}

// seamAssertChain checks the compaction at logPath grew the chain by one
// segment without touching the older segment bytes in oldSegments.
func seamAssertChain(t *testing.T, logPath string, state RunState, oldSegments []Message, wantSeq, wantCount int, wantSummary string) {
	t.Helper()
	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	full := latest.FullMessages()
	if len(full) <= len(oldSegments) {
		t.Fatalf("conversation len = %d, want a new segment after the %d old segments", len(full), len(oldSegments))
	}
	if got, want := mustJSON(t, full[:len(oldSegments)]), mustJSON(t, oldSegments); !bytes.Equal(got, want) {
		t.Fatalf("old segments changed:\n got %s\nwant %s", got, want)
	}
	segment := full[len(oldSegments)]
	if segment.Segment == nil || *segment.Segment != (SummarySegment{Sequence: wantSeq, SourceMessageCount: wantCount}) {
		t.Fatalf("new segment metadata = %+v, want sequence %d count %d", segment.Segment, wantSeq, wantCount)
	}
	if got, want := segment.Content, segmentHeader(wantSeq)+wantSummary; got != want {
		t.Fatalf("new segment content = %q, want %q", got, want)
	}
	if scan := scanSegmentChain(full); scan.Status != chainValid || len(scan.Segments) != wantSeq {
		t.Fatalf("scanned chain status = %v segments = %d, want valid with %d segments", scan.Status, len(scan.Segments), wantSeq)
	}
	if prefix := latest.SummaryPrefix; len(prefix) > 0 && !bytes.Equal(mustJSON(t, prefix), mustJSON(t, full[:len(prefix)])) {
		t.Fatalf("persisted summary prefix = %s, want the leading conversation messages %s", mustJSON(t, prefix), mustJSON(t, full[:len(prefix)]))
	}
	lines := segmentDecisionLines(t, logPath)
	want := fmt.Sprintf("segment_decision action=append reason=append sequence=%d", wantSeq)
	if len(lines) == 0 || !strings.HasPrefix(lines[len(lines)-1], want) {
		t.Fatalf("segment_decision lines = %v, want the last to start with %q", lines, want)
	}
}

// seamAssertAppend runs the compaction after a seam and asserts it appended a
// segment instead of rebasing the chain.
func seamAssertAppend(t *testing.T, req RunRequest, state RunState, oldSegments []Message, wantSeq, wantCount int, wantSummary string) RunState {
	t.Helper()
	state = seamCompact(t, req, state, wantSeq)
	seamAssertChain(t, req.CompactionLogPath, state, oldSegments, wantSeq, wantCount, wantSummary)
	return state
}

// TestSegmentSeamRunSeedAppends crosses the new-run seam: a run seeded from the
// persisted lineage through initializeRunState flattens the summaries into
// Messages, and its first compaction still appends.
func TestSegmentSeamRunSeedAppends(t *testing.T) {
	fixture := seamBuildChain(t, oneSummaryProvider("seam segment one facts", "seam segment two facts"))
	req := segmentedCompactionRequest(oneSummaryProvider("seam resumed facts"), seamChainPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "seam-seed.log")
	req.SourceConversation = fixture.lineage.FullMessages()
	req.SeedLineage = fixture.lineage

	state := initializeRunState(req)
	if got, want := len(state.Lineage.Generations), 1; got != want {
		t.Fatalf("seeded run generations = %d, want %d flat generation", got, want)
	}
	generation := state.Lineage.Generations[0]
	if got, want := generation.ID, 1; got != want {
		t.Fatalf("seeded run generation ID = %d, want %d", got, want)
	}
	if len(generation.SummaryPrefix) != 0 {
		t.Fatalf("seeded run summary prefix = %d messages, want the summaries flattened into Messages", len(generation.SummaryPrefix))
	}
	if got, want := mustJSON(t, generation.Messages[:len(fixture.prefix)]), mustJSON(t, fixture.prefix); !bytes.Equal(got, want) {
		t.Fatalf("seeded run messages = %s, want the persisted chain %s", got, want)
	}

	state = seamAppend(state, seamTurn(6)...)
	seamAssertAppend(t, req, state, fixture.prefix[:2], 3, 2, "seam resumed facts")
}

// TestSegmentSeamFollowUpShapeAppends crosses the delegation follow_up seam:
// buildContinuationRequest passes the prior conversation with no SeedLineage, so
// the summaries only exist inside Messages.
func TestSegmentSeamFollowUpShapeAppends(t *testing.T) {
	fixture := seamBuildChain(t, oneSummaryProvider("seam segment one facts", "seam segment two facts"))
	req := segmentedCompactionRequest(oneSummaryProvider("seam follow up facts"), seamChainPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "seam-follow-up.log")
	// Mirrors buildContinuationRequest: replay-safe prior messages plus the new
	// user message, and no SeedLineage.
	req.SourceConversation = append(ReplaySafeConversation(fixture.lineage.FullMessages()),
		Message{Role: MessageRoleUser, Content: "seam follow up request"})
	req.Prompt.Conversation = nil

	state := initializeRunState(req)
	if got, want := len(state.Lineage.Generations), 1; got != want {
		t.Fatalf("follow-up run generations = %d, want %d flat generation", got, want)
	}
	generation := state.Lineage.Generations[0]
	if len(generation.SummaryPrefix) != 0 {
		t.Fatalf("follow-up run summary prefix = %d messages, want the summaries flattened into Messages", len(generation.SummaryPrefix))
	}
	if got, want := mustJSON(t, generation.Messages[:len(fixture.prefix)]), mustJSON(t, fixture.prefix); !bytes.Equal(got, want) {
		t.Fatalf("follow-up run messages = %s, want the persisted chain %s", got, want)
	}
	if last := generation.Messages[len(generation.Messages)-1]; last.Role != MessageRoleUser || last.Content != "seam follow up request" {
		t.Fatalf("follow-up conversation ends with %+v, want the follow-up request", last)
	}

	state = seamAppend(state, Message{Role: MessageRoleAssistant, Content: "seam follow up reply"})
	state = seamAppend(state, seamTurn(6)...)
	seamAssertAppend(t, req, state, fixture.prefix[:2], 3, 4, "seam follow up facts")
}

// TestSegmentSeamManualCompactionAppends crosses the manual-compaction seam:
// CompactWithLineage rebuilds the run state from currentConv, so the chain is
// only visible in Messages, and the compaction it performs appends.
func TestSegmentSeamManualCompactionAppends(t *testing.T) {
	fixture := seamBuildChain(t, oneSummaryProvider("seam segment one facts", "seam segment two facts"))
	req := segmentedCompactionRequest(oneSummaryProvider("seam manual facts"), seamChainPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "seam-manual.log")
	current := append(fixture.lineage.FullMessages(), seamTurn(6)...)

	compacted, cleared, applied, err := (&Runner{}).CompactWithLineage(context.Background(), req, fixture.lineage, current)
	if err != nil {
		t.Fatalf("CompactWithLineage() error = %v", err)
	}
	if !applied {
		t.Fatal("CompactWithLineage() applied = false, want true")
	}
	if cleared {
		t.Fatal("CompactWithLineage() cleared = true, want false for a stateless summary")
	}
	if len(compacted) >= len(current) {
		t.Fatalf("compacted conversation len = %d, want fewer than %d", len(compacted), len(current))
	}
	seamAssertChain(t, req.CompactionLogPath, stateFromMessages(compacted), fixture.prefix[:2], 3, 2, "seam manual facts")
}

// TestSegmentSeamEmergencyRebasesThenAppends crosses the emergency seam:
// emergency compaction always rebases to sequence 1, and the next normal
// compaction still appends, because the rebuilt chain starts at 1 again.
func TestSegmentSeamEmergencyRebasesThenAppends(t *testing.T) {
	fixture := seamBuildChain(t, oneSummaryProvider("seam segment one facts", "seam segment two facts"))
	providerStub := oneSummaryProvider(
		strings.Repeat("seam normal overflow ", 1200),
		"seam emergency facts",
		"seam after emergency facts",
	)
	req := segmentedCompactionRequest(providerStub, seamChainPolicy())
	req.CompactionLogPath = filepath.Join(t.TempDir(), "seam-emergency.log")
	req.ModelBudget.ContextSize = 7500
	req.ModelBudget.SummaryMaxTokens = 64
	req.ModelBudget.NormalSummaryMaxTokens = 64
	req.ModelBudget.EmergencySummaryMaxTokens = 16

	state := stateFromMessages(fixture.lineage.FullMessages())
	state = seamAppend(state, seamTurns(6, 3)...)
	state = seamCompact(t, req, state, 1)

	latest := state.Lineage.Generations[len(state.Lineage.Generations)-1]
	prefix := latest.SummaryPrefix
	if len(prefix) == 0 || prefix[0].Segment == nil || *prefix[0].Segment != (SummarySegment{Sequence: 1, SourceMessageCount: 4}) {
		t.Fatalf("emergency prefix = %#v, want a sequence 1 segment covering four messages", prefix)
	}
	if got, want := prefix[0].Content, segmentHeader(1)+"seam emergency facts"; got != want {
		t.Fatalf("emergency segment content = %q, want %q", got, want)
	}
	lines := segmentDecisionLines(t, req.CompactionLogPath)
	wantEmergency := "segment_decision action=rebase reason=emergency sequence=1"
	if len(lines) != 1 || !strings.HasPrefix(lines[0], wantEmergency) {
		t.Fatalf("segment_decision lines = %v, want one line starting with %q", lines, wantEmergency)
	}

	req.ModelBudget = segmentedCompactionRequest(providerStub, seamChainPolicy()).ModelBudget
	state = seamAppend(state, seamTurns(9, 3)...)
	state = seamAssertAppend(t, req, state, prefix[:1], 2, 2, "seam after emergency facts")
	if lines := segmentDecisionLines(t, req.CompactionLogPath); len(lines) != 2 {
		t.Fatalf("segment_decision lines = %v, want the emergency rebase then the append", lines)
	}
}
