package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

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
