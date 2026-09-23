package builtin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/diagnostics"
	"github.com/luispabon/steiner/internal/tool"
)

// runMutateCapture runs a mutate call with an explicit diagnostics capture
// level (and optional read lookup) in the handler context, so tests can drive
// the Stage C/D feature paths directly.
func runMutateCapture(t *testing.T, root string, capture tool.DiagnosticsCapture, observed func(string) bool, lookup tool.FileReadLookup, input map[string]any) *MutateResult {
	t.Helper()
	policy := tool.NewPathPolicy(root, config.PathsConfig{ProjectRootOnly: true})
	env := Env{WorkDir: root, PathPolicy: &policy, FileObserved: observed}
	toolDef := NewMutateTool(env)
	ctx := tool.WithDiagnosticsCapture(context.Background(), capture)
	if lookup != nil {
		ctx = tool.WithFileReadLookup(ctx, lookup)
	}
	result, err := toolDef.Handler(ctx, input)
	if err != nil {
		t.Fatalf("mutate Handler() error = %v", err)
	}
	got, ok := result.(*MutateResult)
	if !ok {
		t.Fatalf("mutate Handler() result = %T, want *MutateResult", result)
	}
	return got
}

func TestComputeMatchFailure_Features(t *testing.T) {
	cases := []struct {
		name  string
		in    matchFeatureInput
		check func(t *testing.T, f tool.MatchFailure)
	}{
		{
			name: "uniform indent shift",
			in:   matchFeatureInput{old: "\t\tfoo\n\t\tbar\n", content: []byte("\tfoo\n\tbar\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.WhitespaceKind != "indent_uniform" {
					t.Errorf("ws_kind = %q, want indent_uniform", f.WhitespaceKind)
				}
				if f.IndentDeltaMax != 1 {
					t.Errorf("indent_delta_max = %d, want 1", f.IndentDeltaMax)
				}
				if f.OldLines != 2 || f.NonBlankLines != 2 || f.FileLines != 2 {
					t.Errorf("lines = old %d nonblank %d file %d, want 2/2/2", f.OldLines, f.NonBlankLines, f.FileLines)
				}
				if f.MatchCount != 0 {
					t.Errorf("match_count = %d, want 0", f.MatchCount)
				}
			},
		},
		{
			name: "nonuniform indent shift",
			in:   matchFeatureInput{old: "\t\tfoo\n\tbar\n", content: []byte("\tfoo\n\t\t\tbar\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.WhitespaceKind != "indent_nonuniform" {
					t.Errorf("ws_kind = %q, want indent_nonuniform", f.WhitespaceKind)
				}
				if f.IndentDeltaMax != 2 {
					t.Errorf("indent_delta_max = %d, want 2", f.IndentDeltaMax)
				}
			},
		},
		{
			name: "tabs vs spaces",
			in:   matchFeatureInput{old: "\tfoo\n\tbar\n", content: []byte("    foo\n    bar\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if !f.TabsVsSpaces {
					t.Error("tabs_vs_spaces = false, want true")
				}
				if f.WhitespaceKind != "indent_uniform" {
					t.Errorf("ws_kind = %q, want indent_uniform", f.WhitespaceKind)
				}
				if f.IndentDeltaMax != 3 {
					t.Errorf("indent_delta_max = %d, want 3", f.IndentDeltaMax)
				}
			},
		},
		{
			name: "trailing whitespace only",
			in:   matchFeatureInput{old: "foo\n", content: []byte("foo   \n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.WhitespaceKind != "trailing" {
					t.Errorf("ws_kind = %q, want trailing", f.WhitespaceKind)
				}
				if f.ExactPrefixLines != -1 {
					t.Errorf("exact_prefix_lines = %d, want -1", f.ExactPrefixLines)
				}
				if f.TrimPrefixLines != 1 {
					t.Errorf("trim_prefix_lines = %d, want 1", f.TrimPrefixLines)
				}
			},
		},
		{
			name: "blank line count",
			in:   matchFeatureInput{old: "a\n\nb\n", content: []byte("a b\n\n\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.WhitespaceKind != "blank_lines" {
					t.Errorf("ws_kind = %q, want blank_lines", f.WhitespaceKind)
				}
				if f.LinesFound != 0 {
					t.Errorf("lines_found = %d, want 0", f.LinesFound)
				}
			},
		},
		{
			name: "crlf file with lf old_string",
			in:   matchFeatureInput{old: "foo\nbar\n", content: []byte("foo\r\nbar\r\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if !f.CRLFMismatch {
					t.Error("crlf_mismatch = false, want true")
				}
				if f.WhitespaceKind != "trailing" {
					t.Errorf("ws_kind = %q, want trailing", f.WhitespaceKind)
				}
			},
		},
		{
			name: "double escaped newline",
			in:   matchFeatureInput{old: `foo\nbar`, content: []byte("foo\nbar\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if !f.UnescapeMatches {
					t.Error("unescape_matches = false, want true")
				}
				if f.WhitespaceKind != "none" {
					t.Errorf("ws_kind = %q, want none", f.WhitespaceKind)
				}
			},
		},
		{
			name: "grep prefixed lines",
			in:   matchFeatureInput{old: "12: foo\n13: bar\n", content: []byte("unrelated\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if !f.LinePrefix {
					t.Error("line_prefix = false, want true")
				}
				if f.LinesFound != 0 {
					t.Errorf("lines_found = %d, want 0", f.LinesFound)
				}
			},
		},
		{
			name: "lines present but reordered",
			in:   matchFeatureInput{old: "foo\nbar\nbaz\n", content: []byte("bar\nfoo\nbaz\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.LinesFound != 3 || f.NonBlankLines != 3 {
					t.Errorf("lines_found = %d nonblank = %d, want 3/3", f.LinesFound, f.NonBlankLines)
				}
				if f.LongestRun != 1 {
					t.Errorf("longest_run = %d, want 1 (no two old lines are adjacent in the file)", f.LongestRun)
				}
				if f.ExactPrefixLines != 1 {
					t.Errorf("exact_prefix_lines = %d, want 1", f.ExactPrefixLines)
				}
			},
		},
		{
			name: "fabricated text",
			in:   matchFeatureInput{old: "totally different\n", content: []byte("hello world\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.LinesFound != 0 || f.LongestRun != 0 {
					t.Errorf("lines_found = %d longest_run = %d, want 0/0", f.LinesFound, f.LongestRun)
				}
				if f.ExactPrefixLines != -1 || f.TrimPrefixLines != -1 {
					t.Errorf("prefix = %d/%d, want -1/-1", f.ExactPrefixLines, f.TrimPrefixLines)
				}
				if f.LocusLine != 0 {
					t.Errorf("locus_line = %d, want 0", f.LocusLine)
				}
				if f.WhitespaceKind != "none" {
					t.Errorf("ws_kind = %q, want none", f.WhitespaceKind)
				}
			},
		},
		{
			name: "partial recall",
			in:   matchFeatureInput{old: "alpha\nGAMMA\n", content: []byte("alpha\nbeta\n")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.LinesFound != 1 {
					t.Errorf("lines_found = %d, want 1", f.LinesFound)
				}
				if f.LongestRun != 1 {
					t.Errorf("longest_run = %d, want 1", f.LongestRun)
				}
				if f.ExactPrefixLines != 1 {
					t.Errorf("exact_prefix_lines = %d, want 1", f.ExactPrefixLines)
				}
			},
		},
		{
			name: "exact prefix equals old lines (trailing newline mismatch)",
			in:   matchFeatureInput{old: "foo\nbar\n", content: []byte("foo\nbar")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ExactPrefixLines != 2 || f.OldLines != 2 {
					t.Errorf("exact_prefix_lines = %d old_lines = %d, want 2/2", f.ExactPrefixLines, f.OldLines)
				}
				if f.MatchCount != 0 {
					t.Errorf("match_count = %d, want 0", f.MatchCount)
				}
			},
		},
		{
			name: "matches original after earlier op in call",
			in:   matchFeatureInput{old: "alpha", content: []byte("beta"), original: []byte("alpha"), touched: true},
			check: func(t *testing.T, f tool.MatchFailure) {
				if !f.MatchesOriginal {
					t.Error("matches_original = false, want true")
				}
			},
		},
		{
			name: "read state unknown",
			in:   matchFeatureInput{old: "x", content: []byte("y")},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "unknown" || f.TurnsSinceRead != -1 {
					t.Errorf("read_state = %q turns = %d, want unknown/-1", f.ReadState, f.TurnsSinceRead)
				}
			},
		},
		{
			name: "read state never read",
			in:   matchFeatureInput{old: "x", content: []byte("y"), read: tool.FileReadState{Known: true}},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "never_read" || f.TurnsSinceRead != -1 {
					t.Errorf("read_state = %q turns = %d, want never_read/-1", f.ReadState, f.TurnsSinceRead)
				}
			},
		},
		{
			name: "read state pruned",
			in:   matchFeatureInput{old: "x", content: []byte("y"), read: tool.FileReadState{Known: true, Pruned: true}},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "pruned" {
					t.Errorf("read_state = %q, want pruned", f.ReadState)
				}
			},
		},
		{
			name: "read state self mutated wins over external change",
			in:   matchFeatureInput{old: "x", content: []byte("y"), read: tool.FileReadState{Known: true, Observed: true, MutatedSinceRead: true, ChangedSinceRead: true, TurnsSinceRead: 3}},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "self_mutated" || f.TurnsSinceRead != 3 {
					t.Errorf("read_state = %q turns = %d, want self_mutated/3", f.ReadState, f.TurnsSinceRead)
				}
			},
		},
		{
			name: "read state external change",
			in:   matchFeatureInput{old: "x", content: []byte("y"), read: tool.FileReadState{Known: true, Observed: true, ChangedSinceRead: true, TurnsSinceRead: 2}},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "external_change" || f.TurnsSinceRead != 2 {
					t.Errorf("read_state = %q turns = %d, want external_change/2", f.ReadState, f.TurnsSinceRead)
				}
			},
		},
		{
			name: "read state unchanged",
			in:   matchFeatureInput{old: "x", content: []byte("y"), read: tool.FileReadState{Known: true, Observed: true, TurnsSinceRead: 1}},
			check: func(t *testing.T, f tool.MatchFailure) {
				if f.ReadState != "unchanged" || f.TurnsSinceRead != 1 {
					t.Errorf("read_state = %q turns = %d, want unchanged/1", f.ReadState, f.TurnsSinceRead)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, computeMatchFailure(tc.in))
		})
	}
}

func TestInReadRange(t *testing.T) {
	observed := tool.FileReadState{Known: true, Observed: true, StartLine: 5, EndLine: 10}
	cases := []struct {
		name  string
		locus int
		read  tool.FileReadState
		want  string
	}{
		{"locus zero", 0, observed, "unknown"},
		{"not observed", 7, tool.FileReadState{Known: true}, "unknown"},
		{"not known", 7, tool.FileReadState{}, "unknown"},
		{"inside", 7, observed, "yes"},
		{"outside", 3, observed, "no"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := inReadRange(tc.locus, tc.read); got != tc.want {
				t.Errorf("inReadRange(%d) = %q, want %q", tc.locus, got, tc.want)
			}
		})
	}
}

func TestComputeMatchFailure_OldLineCap(t *testing.T) {
	old := strings.Repeat("line\n", maxFeatureOldLines+1)
	f := computeMatchFailure(matchFeatureInput{old: old, content: []byte("line\n")})
	if f.OldLines != maxFeatureOldLines+1 {
		t.Errorf("old_lines = %d, want %d", f.OldLines, maxFeatureOldLines+1)
	}
	if !f.Truncated {
		t.Error("truncated = false, want true when old lines exceed the cap")
	}
}

func TestComputeMatchFailure_FileByteCap(t *testing.T) {
	content := strings.Repeat("a\n", (maxFeatureFileBytes/2)+8)
	f := computeMatchFailure(matchFeatureInput{old: "a\n", content: []byte(content)})
	if !f.Truncated {
		t.Error("truncated = false, want true when content exceeds the byte cap")
	}
	if f.ExactPrefixLines != -1 || f.TrimPrefixLines != -1 {
		t.Errorf("prefix = %d/%d, want -1/-1", f.ExactPrefixLines, f.TrimPrefixLines)
	}
	if f.OldBytes != 2 || f.OldLines != 1 {
		t.Errorf("old_bytes = %d old_lines = %d, want 2/1 (cheap features still computed)", f.OldBytes, f.OldLines)
	}
}

func TestComputeMatchFailure_PositionCap(t *testing.T) {
	content := strings.Repeat("x\n", maxPositionsPerLine+1)
	f := computeMatchFailure(matchFeatureInput{old: "x\n", content: []byte(content)})
	if !f.Truncated {
		t.Error("truncated = false, want true when a line has more positions than the cap")
	}
	if f.LongestRun != 1 {
		t.Errorf("longest_run = %d, want 1", f.LongestRun)
	}
}

// TestComputeMatchFailure_BoundedOnRepetitiveFile proves the computation stays
// bounded on a 4 MiB file of 400 repetitive lines, where a naive scan would
// explode on positions.
func TestComputeMatchFailure_BoundedOnRepetitiveFile(t *testing.T) {
	content := strings.Repeat("}\n", 2<<20)
	old := strings.Repeat("}\n", 400)
	f := computeMatchFailure(matchFeatureInput{old: old, content: []byte(content)})
	if !f.Truncated {
		t.Error("truncated = false, want true when the position cap is hit")
	}
	if f.OldLines != 400 {
		t.Errorf("old_lines = %d, want 400", f.OldLines)
	}
}

func TestBuildMatchSample_TruncatesUTF8(t *testing.T) {
	old := strings.Repeat("é", 5000)
	sample := buildMatchSample("dir/file.txt", old, []byte("unrelated\n"), 0)
	if !sample.OldTruncated {
		t.Error("old_truncated = false, want true for a >4 KiB old_string")
	}
	if !utf8.ValidString(sample.OldString) {
		t.Error("old_string truncation split a UTF-8 rune")
	}
	if len(sample.OldString) > maxMatchSampleBytes {
		t.Errorf("old_string length = %d, want <= %d", len(sample.OldString), maxMatchSampleBytes)
	}
}

func TestMatchFailure_CaptureLevels(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	observed := func(string) bool { return true }
	input := map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "NOPE", "new_string": "x"},
		},
	}

	cases := []struct {
		name       string
		capture    tool.DiagnosticsCapture
		wantMatch  bool
		wantSample bool
	}{
		{"off", tool.DiagnosticsCaptureOff, false, false},
		{"scalars", tool.DiagnosticsCaptureScalars, true, false},
		{"bodies", tool.DiagnosticsCaptureBodies, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runMutateCapture(t, root, tc.capture, observed, nil, input)
			failures := got.FailedOps()
			if len(failures) != 1 {
				t.Fatalf("FailedOps() = %v, want one entry", failures)
			}
			if (failures[0].Match != nil) != tc.wantMatch {
				t.Errorf("Match nil = %v, want present = %v", failures[0].Match == nil, tc.wantMatch)
			}
			if (failures[0].Sample != nil) != tc.wantSample {
				t.Errorf("Sample nil = %v, want present = %v", failures[0].Sample == nil, tc.wantSample)
			}
		})
	}
}

func TestMatchSample_PerCallCap(t *testing.T) {
	root := t.TempDir()
	var operations []any
	for i := 0; i < maxDetailedMutateFailures+1; i++ {
		name := "f" + string(rune('a'+i)) + ".txt"
		if err := os.WriteFile(filepath.Join(root, name), []byte("content\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		operations = append(operations, map[string]any{"type": "replace", "path": name, "old_string": "NOPE", "new_string": "x"})
	}
	got := runMutateCapture(t, root, tool.DiagnosticsCaptureBodies, func(string) bool { return true }, nil, map[string]any{"operations": operations})
	failures := got.FailedOps()
	if len(failures) != maxDetailedMutateFailures+1 {
		t.Fatalf("FailedOps() = %d entries, want %d", len(failures), maxDetailedMutateFailures+1)
	}
	samples := 0
	for _, f := range failures {
		if f.Match == nil {
			t.Error("a failure is missing Match; features must be recorded for every failure")
		}
		if f.Sample != nil {
			samples++
		}
	}
	if samples != maxDetailedMutateFailures {
		t.Errorf("samples = %d, want %d (per-call cap)", samples, maxDetailedMutateFailures)
	}
}

func TestMutatePlanner_MatchFeaturesAndReasonStable(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("foo foo\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	input := map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "nope", "new_string": "x"},
			map[string]any{"type": "replace", "path": "b.txt", "old_string": "foo", "new_string": "y"},
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "hello", "new_string": "z"},
		},
	}
	observed := func(string) bool { return true }

	off := runMutateCapture(t, root, tool.DiagnosticsCaptureOff, observed, nil, input)
	scalars := runMutateCapture(t, root, tool.DiagnosticsCaptureScalars, observed, nil, input)

	if off.Output != scalars.Output {
		t.Errorf("capture changed model-facing output:\n off: %q\n scalars: %q", off.Output, scalars.Output)
	}

	offFailures := off.FailedOps()
	scalarsFailures := scalars.FailedOps()
	if len(offFailures) != 2 || len(scalarsFailures) != 2 {
		t.Fatalf("failed ops = %d/%d, want 2/2", len(offFailures), len(scalarsFailures))
	}
	for i, f := range offFailures {
		if f.Match != nil {
			t.Errorf("off failure %d has Match; nothing should be computed at capture Off", i)
		}
	}
	wantReasons := []string{ReasonNoMatch, ReasonAmbiguousMatch}
	for i, f := range scalarsFailures {
		if f.Match == nil {
			t.Errorf("scalars failure %d is missing Match", i)
		}
		if f.Sample != nil {
			t.Errorf("scalars failure %d has Sample; samples are capture Bodies only", i)
		}
		if f.Reason != wantReasons[i] {
			t.Errorf("failure %d reason = %q, want %q", i, f.Reason, wantReasons[i])
		}
	}
}

func TestMutatePlanner_MatchesOriginalAcrossOps(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got := runMutateCapture(t, root, tool.DiagnosticsCaptureScalars, func(string) bool { return true }, nil, map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "alpha", "new_string": "beta"},
			map[string]any{"type": "replace", "path": "a.txt", "old_string": "alpha", "new_string": "gamma"},
		},
	})
	failures := got.FailedOps()
	if len(failures) != 1 || failures[0].Match == nil {
		t.Fatalf("FailedOps() = %v, want one failure with Match", failures)
	}
	if !failures[0].Match.MatchesOriginal {
		t.Error("matches_original = false, want true when an earlier op changed the targeted text")
	}
}

func TestMutateToolStream_Privacy(t *testing.T) {
	run := func(t *testing.T, captureBodies bool) string {
		t.Helper()
		root := t.TempDir()
		diagDir := t.TempDir()
		writer, err := diagnostics.New(diagnostics.Options{Dir: diagDir, Streams: diagnostics.Streams{Tool: true}, CaptureBodies: captureBodies})
		if err != nil {
			t.Fatalf("diagnostics.New() error = %v", err)
		}
		t.Cleanup(func() { _ = writer.Close() })

		if err := os.WriteFile(filepath.Join(root, "secret_file.txt"), []byte("hello world\n"), 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		policy := tool.NewPathPolicy(root, config.PathsConfig{})
		toolDef := NewMutateTool(Env{WorkDir: root, PathPolicy: &policy, FileObserved: func(string) bool { return true }})
		executor := tool.NewExecutor(tool.NewRegistry(toolDef), config.Config{}, nil, root, "", tool.Unsandboxed{}).WithDiagnostics(writer)
		if _, err := executor.Execute(context.Background(), "mutate", "call-1", map[string]any{
			"operations": []any{
				map[string]any{"type": "replace", "path": "secret_file.txt", "old_string": "SENSITIVE_OLD_STRING", "new_string": "x"},
			},
		}); err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(diagDir, "tool.jsonl"))
		if err != nil {
			t.Fatalf("read tool.jsonl: %v", err)
		}
		return string(raw)
	}

	scalars := run(t, false)
	if !strings.Contains(scalars, `"match"`) {
		t.Errorf("scalars record has no match field:\n%s", scalars)
	}
	for _, leak := range []string{"secret_file", "SENSITIVE_OLD_STRING", `"sample"`} {
		if strings.Contains(scalars, leak) {
			t.Errorf("scalars record leaked %q:\n%s", leak, scalars)
		}
	}

	bodies := run(t, true)
	if !strings.Contains(bodies, `"sample"`) {
		t.Errorf("bodies record has no sample field:\n%s", bodies)
	}
	for _, want := range []string{"secret_file.txt", "SENSITIVE_OLD_STRING"} {
		if !strings.Contains(bodies, want) {
			t.Errorf("bodies record is missing %q:\n%s", want, bodies)
		}
	}
}

func TestMutateGolden_ModelFacingUnchanged(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	write("ws.txt", "alpha\n\tindented\n")
	write("amb.txt", "foo foo\n")
	write("hash.txt", "content\n")
	write("unread.txt", "content\n")

	observed := func(path string) bool { return !strings.HasSuffix(path, "unread.txt") }
	input := map[string]any{
		"operations": []any{
			map[string]any{"type": "replace", "path": "ws.txt", "old_string": "alpha\n\t\tindented\n", "new_string": "x"},
			map[string]any{"type": "replace", "path": "amb.txt", "old_string": "foo", "new_string": "bar"},
			map[string]any{"type": "replace", "path": "hash.txt", "old_string": "content", "new_string": "y", "file_hash": "deadbeef"},
			map[string]any{"type": "replace", "path": "unread.txt", "old_string": "content", "new_string": "z"},
		},
	}

	off := runMutateCapture(t, root, tool.DiagnosticsCaptureOff, observed, nil, input)
	bodies := runMutateCapture(t, root, tool.DiagnosticsCaptureBodies, observed, nil, input)

	if off.Output != bodies.Output {
		t.Errorf("output changed across capture level:\n off: %q\n bodies: %q", off.Output, bodies.Output)
	}
	offJSON, err := json.Marshal(off)
	if err != nil {
		t.Fatalf("marshal off result: %v", err)
	}
	bodiesJSON, err := json.Marshal(bodies)
	if err != nil {
		t.Fatalf("marshal bodies result: %v", err)
	}
	if string(offJSON) != string(bodiesJSON) {
		t.Errorf("JSON envelope changed across capture level:\n off: %s\n bodies: %s", offJSON, bodiesJSON)
	}

	def := NewMutateTool(Env{})
	if def.Description != mutateGoldenDescription {
		t.Errorf("mutate description changed:\n got: %q\nwant: %q", def.Description, mutateGoldenDescription)
	}
	schema, err := json.Marshal(def.ParameterSchema)
	if err != nil {
		t.Fatalf("marshal mutate schema: %v", err)
	}
	if string(schema) != mutateGoldenSchemaJSON {
		t.Errorf("mutate schema changed:\n got: %s\nwant: %s", schema, mutateGoldenSchemaJSON)
	}
}

// mutateGoldenDescription is the model-facing mutate description copied from
// main before the instrumentation landed. It must not change.
const mutateGoldenDescription = "Apply structured file edits via op types: create, write (create or overwrite), replace, delete_file, move. Supports file_hash for staleness detection on existing targets — pass the hash from read/grep to fail fast if the file changed. replace against an existing file is rejected unless you read it earlier this session or pass file_hash — read the file first, or supply the hash from a read/grep result. move rejects destination collisions instead of overwriting. For create, write, and move: parent directories must exist for workspace paths (steiner auto-creates only within its own sandbox tmpdir); create it first (e.g. with bash mkdir -p), then retry. Use assert_present/assert_absent on any operation whose effect cannot otherwise be verified — assertions abort the full batch if they fail, confirming the edit landed where intended. If a batch has multiple failing operations, every failure is reported together in one result and nothing is applied — fix all of them before resending. Use mutate for all file edits; do not use bash, sed, cat, write, edit, or apply_patch for file mutations."

// mutateGoldenSchemaJSON is the model-facing mutate schema copied from main
// before the instrumentation landed. It must not change.
const mutateGoldenSchemaJSON = `{"additionalProperties":false,"properties":{"operations":{"description":"Ordered list of file mutations. Operations are evaluated sequentially against an in-memory snapshot, so later operations see earlier edits in the same batch, but no filesystem writes are committed until the full batch has been planned. On partial failure, operations_skipped reports how many were never attempted.","items":{"additionalProperties":false,"properties":{"assert_absent":{"description":"Strings that must be absent from the post-operation file content. Use assertions to confirm edits landed where you intended; assertion failures abort the full batch before commit.","items":{"type":"string"},"type":"array"},"assert_present":{"description":"Strings that must appear in the post-operation file content. Use assertions to confirm edits landed where you intended (especially for replace operations whose old_string might be ambiguous); assertion failures abort the full batch before commit.","items":{"type":"string"},"type":"array"},"content":{"description":"File content for create and write","type":"string"},"file_hash":{"description":"8-char hex hash from read/grep result. When provided, it is validated against the initial disk snapshot captured when the batch starts, not after earlier in-memory operations. It only applies to existing files; missing targets fail explicitly instead of being silently accepted.","type":"string"},"from":{"description":"Source path for move","type":"string"},"new_string":{"description":"Replacement text","type":"string"},"old_string":{"description":"Exact text to replace. Whitespace must match the file exactly, including leading indentation depth (most common mismatch: tab vs space nesting level). Copy directly from read output rather than reconstructing from memory.","type":"string"},"path":{"description":"Target path for create, write, replace, and delete_file","type":"string"},"replace_all":{"default":false,"description":"Replace all occurrences for replace","type":"boolean"},"to":{"description":"Destination path for move. The destination must not already exist; move never overwrites.","type":"string"},"type":{"description":"Operation type. Required fields per type ([optional] in brackets): create: path, content. write: path, content. replace: path, old_string, new_string [replace_all]. delete_file: path. move: from, to. Most types also accept [assert_present, assert_absent, file_hash]; exceptions: create accepts asserts but not file_hash; delete_file accepts file_hash but not asserts.","enum":["create","write","replace","delete_file","move"],"type":"string"}},"required":["type"],"type":"object"},"minItems":1,"type":"array"}},"required":["operations"],"type":"object"}`
