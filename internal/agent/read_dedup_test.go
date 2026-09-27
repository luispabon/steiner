package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestDedupReadResult(t *testing.T) {
	base := builtin.ReadResult{Path: "/tmp/read-dedup.txt", StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: "hash-a", NextOffset: 9, Output: "one\ntwo\n"}
	makeContent := func(result builtin.ReadResult) string {
		t.Helper()
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	priorRead := func(result builtin.ReadResult, turn int) Message {
		return Message{Role: MessageRoleTool, Name: "read", Turn: turn, Content: makeContent(result)}
	}
	wantAnnotation := fileUnchangedAnnotationPrefix + " 4: lines 1-2 of 2 in " + base.Path + "]"

	tests := []struct {
		name       string
		current    string
		prior      []Message
		wantAction string
		wantReason string
		wantTurn   int
		wantOutput string
	}{
		{
			name:       "identical earlier full read",
			current:    makeContent(base),
			prior:      []Message{priorRead(base, 4)},
			wantAction: "annotated",
			wantReason: "unchanged since turn 4",
			wantTurn:   4,
		},
		{
			name:       "newest matching read wins",
			current:    makeContent(base),
			prior:      []Message{priorRead(base, 4), priorRead(base, 2)},
			wantAction: "annotated",
			wantReason: "unchanged since turn 4",
			wantTurn:   4,
		},
		{
			name:       "hash mismatch",
			current:    makeContent(base),
			prior:      []Message{priorRead(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: "other"}, 4)},
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "range mismatch",
			current:    makeContent(base),
			prior:      []Message{priorRead(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 1, TotalLines: 2, FileHash: base.FileHash}, 4)},
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "empty hash",
			current:    makeContent(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 2, TotalLines: 2, Output: base.Output}),
			prior:      []Message{priorRead(base, 4)},
			wantAction: "full",
			wantReason: "no file_hash",
		},
		{
			name:       "line cap",
			current:    makeContent(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: base.FileHash, Output: "clip" + builtin.LineTruncationMarker}),
			prior:      []Message{priorRead(base, 4)},
			wantAction: "full",
			wantReason: "line-capped read",
		},
		{
			name:       "only prior stub",
			current:    makeContent(base),
			prior:      []Message{priorRead(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: base.FileHash, Output: wantAnnotation}, 4)},
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "missing copy",
			current:    makeContent(base),
			prior:      []Message{{Role: MessageRoleTool, Name: "grep", Turn: 4, Content: makeContent(base)}},
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "normalized path match",
			current:    makeContent(builtin.ReadResult{Path: "/tmp/./read-dedup.txt", StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: base.FileHash, Output: base.Output}),
			prior:      []Message{priorRead(base, 4)},
			wantAction: "annotated",
			wantReason: "unchanged since turn 4",
			wantTurn:   4,
		},
		{
			name:       "invalid result",
			current:    "not json",
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "annotation input stays full",
			current:    makeContent(builtin.ReadResult{Path: base.Path, StartLine: 1, EndLine: 2, TotalLines: 2, FileHash: base.FileHash, Output: wantAnnotation}),
			prior:      []Message{priorRead(base, 4)},
			wantAction: "full",
			wantReason: "no earlier full copy",
		},
		{
			name:       "first read",
			current:    makeContent(base),
			wantAction: "full",
			wantReason: "first read",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, outcome := dedupReadResult(test.current, 8, test.prior)
			if outcome.Action != test.wantAction || outcome.Reason != test.wantReason || outcome.PreviousTurn != test.wantTurn {
				t.Fatalf("outcome = %+v, want action=%q reason=%q previous turn=%d", outcome, test.wantAction, test.wantReason, test.wantTurn)
			}
			if test.wantAction == "full" {
				if got != test.current {
					t.Fatalf("content changed for full result: got %q, want %q", got, test.current)
				}
				return
			}
			var result builtin.ReadResult
			if err := json.Unmarshal([]byte(got), &result); err != nil {
				t.Fatalf("unmarshal deduplicated result: %v", err)
			}
			want := fileUnchangedAnnotationPrefix + " " + strings.TrimPrefix(test.wantReason, "unchanged since turn ") + ": lines 1-2 of 2 in " + result.Path + "]"
			if result.Output != want {
				t.Errorf("output = %q, want %q", result.Output, want)
			}
			if result.FileHash != base.FileHash || result.StartLine != base.StartLine || result.EndLine != base.EndLine || result.TotalLines != base.TotalLines || result.NextOffset != base.NextOffset && result.NextOffset != 0 {
				t.Errorf("fields changed while annotating: %+v", result)
			}
			if strings.Contains(result.Output, builtin.LineTruncationMarker) {
				t.Errorf("annotated output includes truncation marker: %q", result.Output)
			}
		})
	}
}
