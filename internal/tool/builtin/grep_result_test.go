package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

// newGrepBudgetTool writes files into a temp workspace and returns a grep tool
// rooted there.
func newGrepBudgetTool(t *testing.T, files map[string]string) tool.ToolDef {
	t.Helper()
	tmpDir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	return NewGrepTool(Env{WorkDir: tmpDir, PathPolicy: &policy})
}

func runGrepForTest(t *testing.T, toolDef tool.ToolDef, input map[string]any) GrepResult {
	t.Helper()
	resultI, err := toolDef.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("grep handler: %v", err)
	}
	result, ok := resultI.(GrepResult)
	if !ok {
		t.Fatalf("result type = %T, want GrepResult", resultI)
	}
	return result
}

// renderedLineCount counts the lines of rendered grep output.
func renderedLineCount(output string) int {
	if output == "" {
		return 0
	}
	return strings.Count(output, "\n") + 1
}

// matchedLineNumbers returns the line numbers of rendered content lines whose
// text contains needle. Context lines are rendered as "N: text" and match
// lines as "N:C: text", so the number is the part before the first colon.
func matchedLineNumbers(t *testing.T, output, needle string) []int {
	t.Helper()
	var numbers []int
	for _, line := range strings.Split(output, "\n") {
		idx := strings.Index(line, ": ")
		if idx <= 0 || !strings.Contains(line[idx+2:], needle) {
			continue
		}
		n, err := strconv.Atoi(strings.SplitN(line[:idx], ":", 2)[0])
		if err != nil {
			t.Fatalf("parse line number from %q: %v", line, err)
		}
		numbers = append(numbers, n)
	}
	return numbers
}

func TestGrepTool_ContentPagesCoverEveryResultWithinBudget(t *testing.T) {
	var b strings.Builder
	var want []int
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "needle %d\n", i)
		want = append(want, i)
	}
	toolDef := newGrepBudgetTool(t, map[string]string{"many.txt": b.String()})

	var got []int
	offset := 0
	for page := 0; ; page++ {
		if page > 10 {
			t.Fatal("pagination did not finish within 10 pages")
		}
		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"head_limit":  500,
			"offset":      offset,
		})
		if n := renderedLineCount(result.Output); n > grepRenderedLineBudget {
			t.Fatalf("page at offset %d rendered %d lines, want <= %d", offset, n, grepRenderedLineBudget)
		}
		if result.Matches != 300 {
			t.Fatalf("page at offset %d Matches = %d, want 300", offset, result.Matches)
		}
		pageMatches := matchedLineNumbers(t, result.Output, "needle")
		if result.Shown != len(pageMatches) {
			t.Fatalf("page at offset %d Shown = %d, want %d shown matches", offset, result.Shown, len(pageMatches))
		}
		if offset == 0 && result.Shown != grepRenderedLineBudget-1 {
			t.Fatalf("first page Shown = %d, want %d (header takes one line of the budget)", result.Shown, grepRenderedLineBudget-1)
		}
		got = append(got, pageMatches...)
		if result.NextOffset == 0 {
			break
		}
		if result.NextOffset != offset+result.Shown {
			t.Fatalf("NextOffset = %d, want %d (first unshown result)", result.NextOffset, offset+result.Shown)
		}
		offset = result.NextOffset
	}
	if !slices.Equal(got, want) {
		t.Fatalf("matched lines across pages = %d entries, want %d in order without duplicates", len(got), len(want))
	}
}

func TestGrepTool_ContextLinesCountTowardBudget(t *testing.T) {
	var b strings.Builder
	var want []int
	for i := 1; i <= 450; i++ {
		if i%3 == 2 {
			b.WriteString("needle\n")
			want = append(want, i)
		} else {
			b.WriteString("other\n")
		}
	}
	toolDef := newGrepBudgetTool(t, map[string]string{"ctx.txt": b.String()})

	var got []int
	offset := 0
	for page := 0; ; page++ {
		if page > 20 {
			t.Fatal("pagination did not finish within 20 pages")
		}
		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"context":     1,
			"head_limit":  500,
			"offset":      offset,
		})
		if n := renderedLineCount(result.Output); n > grepRenderedLineBudget {
			t.Fatalf("page at offset %d rendered %d lines, want <= %d", offset, n, grepRenderedLineBudget)
		}
		pageMatches := matchedLineNumbers(t, result.Output, "needle")
		if result.Shown != len(pageMatches) {
			t.Fatalf("page at offset %d Shown = %d, want %d (context lines are not results)", offset, result.Shown, len(pageMatches))
		}
		got = append(got, pageMatches...)
		if result.NextOffset == 0 {
			break
		}
		if result.NextOffset != offset+result.Shown {
			t.Fatalf("NextOffset = %d, want %d", result.NextOffset, offset+result.Shown)
		}
		offset = result.NextOffset
	}
	if !slices.Equal(got, want) {
		t.Fatalf("matched lines across pages = %v, want %v", got, want)
	}
}

func TestGrepTool_OversizedResultIsExcerptedAndAdvances(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		switch i {
		case 300:
			b.WriteString("needle target\n")
		case 590:
			b.WriteString("needle again\n")
		default:
			fmt.Fprintf(&b, "filler %d\n", i)
		}
	}
	toolDef := newGrepBudgetTool(t, map[string]string{"big.txt": b.String()})
	input := func(offset int) map[string]any {
		return map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"context":     250,
			"offset":      offset,
		}
	}

	first := runGrepForTest(t, toolDef, input(0))
	if n := renderedLineCount(first.Output); n > grepRenderedLineBudget {
		t.Fatalf("first page rendered %d lines, want <= %d", n, grepRenderedLineBudget)
	}
	if !strings.Contains(first.Output, "300:1: needle target") {
		t.Fatalf("first page Output missing the oversized match line: %q", first.Output[:min(len(first.Output), 200)])
	}
	if !strings.Contains(first.Output, "<truncated output shown=198 total=501>") {
		t.Fatalf("first page Output missing excerpt marker")
	}
	if first.Shown != 1 || first.NextOffset != 1 || first.Matches != 2 {
		t.Fatalf("first page Shown, NextOffset, Matches = %d, %d, %d; want 1, 1, 2", first.Shown, first.NextOffset, first.Matches)
	}

	second := runGrepForTest(t, toolDef, input(first.NextOffset))
	if !strings.Contains(second.Output, "590:1: needle again") {
		t.Fatalf("second page Output = %q, want the next result", second.Output)
	}
	if second.Shown != 1 || second.NextOffset != 0 {
		t.Fatalf("second page Shown, NextOffset = %d, %d; want 1, 0", second.Shown, second.NextOffset)
	}
}

func TestGrepTool_FilesAndCountModesHonorLineBudget(t *testing.T) {
	files := make(map[string]string, 250)
	for i := 0; i < 250; i++ {
		files[fmt.Sprintf("f%03d.txt", i)] = "needle\n"
	}
	toolDef := newGrepBudgetTool(t, files)

	for _, mode := range []string{"files_with_matches", "count"} {
		t.Run(mode, func(t *testing.T) {
			result := runGrepForTest(t, toolDef, map[string]any{
				"pattern":     "needle",
				"output_mode": mode,
				"head_limit":  500,
			})
			if n := renderedLineCount(result.Output); n != grepRenderedLineBudget {
				t.Fatalf("rendered %d lines, want %d", n, grepRenderedLineBudget)
			}
			if result.Shown != grepRenderedLineBudget || result.NextOffset != grepRenderedLineBudget {
				t.Fatalf("Shown, NextOffset = %d, %d; want %d, %d", result.Shown, result.NextOffset, grepRenderedLineBudget, grepRenderedLineBudget)
			}
			if result.Matches != 250 {
				t.Fatalf("Matches = %d, want 250", result.Matches)
			}
		})
	}
}

var grepEmittedRow = regexp.MustCompile(`^(\d+):\d+: `)

// grepEmittedResults returns "file:line" for each content row rendered with a
// match column. Context rows have no column, so they are not results.
func grepEmittedResults(output string) []string {
	var got []string
	file := ""
	for _, line := range strings.Split(output, "\n") {
		if name, ok := strings.CutPrefix(line, "## "); ok {
			file = name
			continue
		}
		if m := grepEmittedRow.FindStringSubmatch(line); m != nil {
			got = append(got, file+":"+m[1])
		}
	}
	return got
}

func TestGrepTool_OverlappingOversizedWindowsOnlyEmitSelectedMatches(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 400; i++ {
		if i == 100 || i == 180 {
			b.WriteString("needle\n")
		} else {
			fmt.Fprintf(&b, "filler %d\n", i)
		}
	}
	toolDef := newGrepBudgetTool(t, map[string]string{"big.txt": b.String()})
	input := func(offset int) map[string]any {
		return map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"context":     100,
			"offset":      offset,
		}
	}

	first := runGrepForTest(t, toolDef, input(0))
	if n := renderedLineCount(first.Output); n > grepRenderedLineBudget {
		t.Fatalf("first page rendered %d lines, want <= %d", n, grepRenderedLineBudget)
	}
	if got := grepEmittedResults(first.Output); !slices.Equal(got, []string{"big.txt:100"}) {
		t.Fatalf("first page emitted %v, want [big.txt:100]", got)
	}
	if !strings.Contains(first.Output, "\n180: needle\n") {
		t.Fatal("first page should keep line 180 as plain context")
	}
	if first.Shown != 1 || first.NextOffset != 1 || first.Matches != 2 {
		t.Fatalf("first page Shown, NextOffset, Matches = %d, %d, %d; want 1, 1, 2", first.Shown, first.NextOffset, first.Matches)
	}

	second := runGrepForTest(t, toolDef, input(first.NextOffset))
	if n := renderedLineCount(second.Output); n > grepRenderedLineBudget {
		t.Fatalf("second page rendered %d lines, want <= %d", n, grepRenderedLineBudget)
	}
	if got := grepEmittedResults(second.Output); !slices.Equal(got, []string{"big.txt:180"}) {
		t.Fatalf("second page emitted %v, want [big.txt:180]", got)
	}
	if !strings.Contains(second.Output, "\n100: needle\n") {
		t.Fatal("second page should keep line 100 as plain context")
	}
	if second.Shown != 1 || second.NextOffset != 0 {
		t.Fatalf("second page Shown, NextOffset = %d, %d; want 1, 0", second.Shown, second.NextOffset)
	}
}

func TestGrepTool_LineNumbersFalseExcerptKeepsMatchAndMarker(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		if i == 300 {
			b.WriteString("needle target\n")
		} else {
			fmt.Fprintf(&b, "filler %d\n", i)
		}
	}
	toolDef := newGrepBudgetTool(t, map[string]string{"big.txt": b.String()})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":      "needle",
		"output_mode":  "content",
		"context":      250,
		"line_numbers": false,
	})
	if n := renderedLineCount(result.Output); n > grepRenderedLineBudget {
		t.Fatalf("page rendered %d lines, want <= %d", n, grepRenderedLineBudget)
	}
	if !strings.Contains(result.Output, "\nneedle target\n") {
		t.Fatal("excerpt should keep the plain match line")
	}
	if !strings.Contains(result.Output, "<truncated output shown=198 total=501>") {
		t.Fatal("excerpt should end with the truncation marker")
	}
	if got := grepEmittedResults(result.Output); len(got) != 0 {
		t.Fatalf("line_numbers false emitted rows %v, want none", got)
	}
	if result.Shown != 1 || result.NextOffset != 0 {
		t.Fatalf("Shown, NextOffset = %d, %d; want 1, 0", result.Shown, result.NextOffset)
	}
}

func TestGrepTool_MultiFilePagesEmitEachResultOnce(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{
		"a.txt": "needle a1\nneedle a2\nneedle a3\n",
		"b.txt": "needle b1\nneedle b2\nneedle b3\n",
	})

	var got []string
	offset := 0
	for page := 0; ; page++ {
		if page > 5 {
			t.Fatal("pagination did not finish within 5 pages")
		}
		result := runGrepForTest(t, toolDef, map[string]any{
			"pattern":     "needle",
			"output_mode": "content",
			"head_limit":  4,
			"offset":      offset,
		})
		if offset == 4 && !strings.HasPrefix(result.Output, "## b.txt\n") {
			t.Fatalf("page at offset 4 should start with its file header, got %q", result.Output)
		}
		got = append(got, grepEmittedResults(result.Output)...)
		if result.NextOffset == 0 {
			break
		}
		if result.NextOffset != offset+result.Shown {
			t.Fatalf("NextOffset = %d, want %d", result.NextOffset, offset+result.Shown)
		}
		offset = result.NextOffset
	}
	want := []string{"a.txt:1", "a.txt:2", "a.txt:3", "b.txt:1", "b.txt:2", "b.txt:3"}
	if !slices.Equal(got, want) {
		t.Fatalf("emitted %v, want %v", got, want)
	}
}

func TestGrepTool_MultilineContinuationLinesPaginateAsSelected(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{"multi.txt": "A\nB\nC\nfiller\n"})
	input := func(offset int) map[string]any {
		return map[string]any{
			"pattern":     "A\\nB\\nC",
			"multiline":   true,
			"output_mode": "content",
			"context":     1,
			"head_limit":  1,
			"offset":      offset,
		}
	}
	wants := []struct {
		offset int
		output string
		next   int
	}{
		{0, "## multi.txt\n1:1: A\n2: B", 1},
		{1, "## multi.txt\n1: A\n2:0: B\n3: C", 2},
		{2, "## multi.txt\n2: B\n3:0: C\n4: filler", 0},
	}
	for _, want := range wants {
		result := runGrepForTest(t, toolDef, input(want.offset))
		if result.Output != want.output {
			t.Fatalf("offset %d output = %q, want %q", want.offset, result.Output, want.output)
		}
		if result.Shown != 1 || result.Matches != 3 || result.NextOffset != want.next {
			t.Fatalf("offset %d Shown, Matches, NextOffset = %d, %d, %d; want 1, 3, %d", want.offset, result.Shown, result.Matches, result.NextOffset, want.next)
		}
	}
}

func TestGrepLineExcerptBounds(t *testing.T) {
	line := strings.Repeat("a", 450) + "NEEDLE" + strings.Repeat("b", 544)
	tests := []struct {
		name              string
		text              string
		column            int
		maxRunes          int
		wantContains      string
		wantHeadTruncated bool
		wantTailTruncated bool
	}{
		{
			name:         "short line is unchanged",
			text:         "NEEDLE here",
			column:       1,
			maxRunes:     400,
			wantContains: "NEEDLE here",
		},
		{
			name:              "match near the start clips the tail",
			text:              "NEEDLE" + strings.Repeat("b", 200),
			column:            1,
			maxRunes:          100,
			wantContains:      "NEEDLE",
			wantTailTruncated: true,
		},
		{
			name:              "match past the cap clips both ends",
			text:              line,
			column:            451,
			maxRunes:          100,
			wantContains:      "NEEDLE",
			wantHeadTruncated: true,
			wantTailTruncated: true,
		},
		{
			name:              "match on the last rune clips the head",
			text:              line,
			column:            1000,
			maxRunes:          100,
			wantHeadTruncated: true,
		},
		{
			name:              "no column clips the tail only",
			text:              line,
			column:            0,
			maxRunes:          100,
			wantTailTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := grepLineExcerpt(tt.text, tt.column, tt.maxRunes)
			if n := len([]rune(got)); n > tt.maxRunes {
				t.Fatalf("excerpt is %d runes, want <= %d", n, tt.maxRunes)
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Fatalf("excerpt %q does not contain %q", got, tt.wantContains)
			}
			if got := strings.HasPrefix(got, LineTruncationMarker); got != tt.wantHeadTruncated {
				t.Fatalf("head truncation marker present = %t, want %t", got, tt.wantHeadTruncated)
			}
			if got := strings.HasSuffix(got, LineTruncationMarker); got != tt.wantTailTruncated {
				t.Fatalf("tail truncation marker present = %t, want %t", got, tt.wantTailTruncated)
			}
		})
	}
}

func TestGrepTool_ContentExcerptKeepsLateMatch(t *testing.T) {
	line := strings.Repeat("filler ", 200) + "needle" + strings.Repeat(" tail", 100)
	toolDef := newGrepBudgetTool(t, map[string]string{"long.txt": line + "\n"})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	rows := strings.Split(strings.TrimRight(result.Output, "\n"), "\n")
	if len(rows) != 2 {
		t.Fatalf("Output = %d rows, want a file header and one match row", len(rows))
	}
	if rows[0] != "## long.txt" {
		t.Fatalf("header row = %q, want %q", rows[0], "## long.txt")
	}
	row := rows[1]
	if !strings.HasPrefix(row, "1:1401: "+LineTruncationMarker) {
		t.Fatalf("match row = %q, want the line:column prefix followed by a clipped head", row)
	}
	if !strings.Contains(row, "needle") {
		t.Fatalf("match row lost the late match: %q", row)
	}
	if n := len([]rune(row)); n > defaultMaxLineRunes {
		t.Fatalf("match row is %d runes, want <= %d", n, defaultMaxLineRunes)
	}
	if result.Shown != 1 || result.NextOffset != 0 {
		t.Fatalf("Shown, NextOffset = %d, %d; want 1, 0", result.Shown, result.NextOffset)
	}
}

func TestGrepTool_UnicodeExcerptKeepsLateMatch(t *testing.T) {
	line := strings.Repeat("é", 600) + "needle" + strings.Repeat("界", 600)
	toolDef := newGrepBudgetTool(t, map[string]string{"wide.txt": line + "\n"})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	if !utf8.ValidString(result.Output) {
		t.Fatalf("Output is not valid UTF-8: %q", result.Output)
	}
	row, ok := strings.CutPrefix(strings.TrimRight(result.Output, "\n"), "## wide.txt\n")
	if !ok {
		t.Fatalf("Output = %q, want a wide.txt file header first", result.Output)
	}
	if !strings.HasPrefix(row, "1:601: "+LineTruncationMarker) {
		t.Fatalf("match row = %q, want rune column 601 and a clipped head", row)
	}
	if !strings.Contains(row, "needle") {
		t.Fatalf("match row lost the late match: %q", row)
	}
	if n := len([]rune(row)); n > defaultMaxLineRunes {
		t.Fatalf("match row is %d runes, want <= %d", n, defaultMaxLineRunes)
	}
}

func TestGrepTool_ExcerptKeepsLateMatchWithoutLineNumbers(t *testing.T) {
	line := strings.Repeat("x", 1000) + "needle" + strings.Repeat("y", 500)
	toolDef := newGrepBudgetTool(t, map[string]string{"long.txt": line + "\n"})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":      "needle",
		"output_mode":  "content",
		"line_numbers": false,
	})
	output := strings.TrimRight(result.Output, "\n")
	if !strings.Contains(output, "needle") {
		t.Fatalf("Output lost the late match: %q", output)
	}
	if strings.Contains(output, "## long.txt\n1:") {
		t.Fatalf("Output = %q, want no line-number prefix", output)
	}
	if n := renderedLineCount(output); n != 2 {
		t.Fatalf("Output = %d rows, want a header and one match row", n)
	}
}

func TestGrepResultJSONEmitsZeroShown(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{"a.txt": "alpha\n"})
	result := runGrepForTest(t, toolDef, map[string]any{"pattern": "needle"})
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal grep result: %v", err)
	}
	if !strings.Contains(string(data), `"shown":0`) {
		t.Fatalf("no-match JSON = %s, want shown:0 emitted", data)
	}
}

func TestGrepTool_MatchedAnsiAndOscArePreserved(t *testing.T) {
	const ansiLine = "\x1b[31mred needle\x1b[0m"
	const oscLine = "\x1b]8;;https://example.com\x07needle link\x1b]8;;\x07"
	toolDef := newGrepBudgetTool(t, map[string]string{
		"styled.txt": "plain\n" + ansiLine + "\n" + oscLine + "\n",
	})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":     "needle",
		"output_mode": "content",
	})
	if !strings.Contains(result.Output, ansiLine) {
		t.Errorf("Output = %q, want matched ANSI line preserved", result.Output)
	}
	if !strings.Contains(result.Output, oscLine) {
		t.Errorf("Output = %q, want matched OSC line preserved", result.Output)
	}
}
