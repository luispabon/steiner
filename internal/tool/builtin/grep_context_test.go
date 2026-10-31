package builtin

import (
	"math"
	"strings"
	"testing"
)

func TestGrepTool_ContextUsesRealLines(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{"hello.txt": "hello\n"})

	result := runGrepForTest(t, toolDef, map[string]any{
		"pattern":       "hello",
		"output_mode":   "content",
		"after_context": 1,
	})
	if !strings.Contains(result.Output, "1:1: hello") {
		t.Fatalf("Output = %q, want the match row", result.Output)
	}
	if lines := strings.Split(result.Output, "\n"); len(lines) != 2 {
		t.Fatalf("Output = %q, want header and match row only, got %d lines", result.Output, len(lines))
	}
	if strings.Contains(result.Output, "\n2:") {
		t.Fatalf("Output = %q, want no synthetic trailing context line", result.Output)
	}
}

func TestGrepTool_MaxIntContextKeepsMatch(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{"match.txt": "before\nneedle\nafter\n"})

	tests := []struct {
		name  string
		input map[string]any
	}{
		{
			name:  "after context only",
			input: map[string]any{"pattern": "needle", "output_mode": "content", "after_context": math.MaxInt},
		},
		{
			name:  "before and after context",
			input: map[string]any{"pattern": "needle", "output_mode": "content", "before_context": math.MaxInt, "after_context": math.MaxInt},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runGrepForTest(t, toolDef, tt.input)
			if !strings.Contains(result.Output, "2:1: needle") {
				t.Fatalf("Output = %q, want the match row", result.Output)
			}
		})
	}
}

func TestMergeGrepWindowsSaturatesContext(t *testing.T) {
	tests := []struct {
		name                      string
		lineNumbers               []int
		totalLines, before, after int
		want                      []grepWindow
	}{
		{
			name:        "both contexts saturate",
			lineNumbers: []int{2},
			totalLines:  4,
			before:      math.MaxInt,
			after:       math.MaxInt,
			want:        []grepWindow{{start: 1, end: 4}},
		},
		{
			name:        "after context saturates",
			lineNumbers: []int{2},
			totalLines:  4,
			after:       math.MaxInt,
			want:        []grepWindow{{start: 2, end: 4}},
		},
		{
			name:        "before context saturates",
			lineNumbers: []int{2},
			totalLines:  4,
			before:      math.MaxInt,
			want:        []grepWindow{{start: 1, end: 2}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeGrepWindows(tt.lineNumbers, tt.totalLines, tt.before, tt.after)
			if len(got) != len(tt.want) {
				t.Fatalf("windows = %#v, want %#v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("windows = %#v, want %#v", got, tt.want)
				}
			}
		})
	}
}
