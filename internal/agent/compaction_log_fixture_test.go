package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// segmentDecisionLogLine mirrors the record grammar of the bench parser in
// scripts/compaction-bench-parse.mjs, so a log format change fails here before
// it breaks the bench.
var segmentDecisionLogLine = regexp.MustCompile(`^segment_decision action=(append|rebase|plain) reason=([a-z][a-z-]*) sequence=(\d+) chain_tokens=(\d+) projected_post_tokens=(\d+) budget=(\d+)$`)

// TestLogSegmentDecisionMatchesBenchFixture pins LogSegmentDecision's line
// format to the shared bench fixture: every record the bench parser accepts is
// rebuilt from its parsed fields and must log back byte-identically, and each
// reason names a declared decision reason. The fixture's embedded, trailing,
// unknown-action and indented lines are decoys the whole-line parser drops.
func TestLogSegmentDecisionMatchesBenchFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "compaction-bench", "compaction-log.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	reasons := map[string]bool{
		segmentReasonAppend:               true,
		segmentReasonChainStart:           true,
		segmentReasonInvalidChain:         true,
		segmentReasonSplitInChain:         true,
		segmentReasonRetentionUnmapped:    true,
		segmentReasonStrippedView:         true,
		segmentReasonEmptySpan:            true,
		segmentReasonEmptySource:          true,
		segmentReasonEmergency:            true,
		segmentReasonMaxSegments:          true,
		segmentReasonChainTokens:          true,
		segmentReasonPostCompactionTokens: true,
		segmentReasonNoProgress:           true,
		segmentReasonRetry:                true,
	}
	atoi := func(field string) int {
		t.Helper()
		value, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("parse fixture count %q: %v", field, err)
		}
		return value
	}

	var decisions []segmentDecision
	var want []string
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		match := segmentDecisionLogLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if !reasons[match[2]] {
			t.Fatalf("fixture reason %q is not a declared segment decision reason", match[2])
		}
		decisions = append(decisions, segmentDecision{
			Rebase:              match[1] != "append",
			Plain:               match[1] == "plain",
			Reason:              match[2],
			Sequence:            atoi(match[3]),
			ChainTokens:         atoi(match[4]),
			ProjectedPostTokens: atoi(match[5]),
			Budget:              atoi(match[6]),
		})
		want = append(want, line)
	}
	if len(decisions) == 0 {
		t.Fatal("fixture has no segment_decision record the bench parser accepts")
	}

	logPath := filepath.Join(t.TempDir(), "compaction.log")
	logger, err := NewCompactionLogger(logPath)
	if err != nil {
		t.Fatalf("NewCompactionLogger: %v", err)
	}
	for _, decision := range decisions {
		if err := logger.LogSegmentDecision(decision); err != nil {
			t.Fatalf("LogSegmentDecision(%+v): %v", decision, err)
		}
	}
	if err := logger.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if wantText := strings.Join(want, "\n") + "\n"; string(got) != wantText {
		t.Errorf("logged decisions = %q, want %q", got, wantText)
	}
}
