package builtin

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestGrepSearch_NormalizesCRLFAndBOM(t *testing.T) {
	tests := []struct {
		name       string
		content    []byte
		pattern    string
		wantLine   string
		wantNumber int
	}{
		{
			name:       "crlf first line anchor",
			content:    []byte("foo\r\nbar\r\n"),
			pattern:    `^foo$`,
			wantLine:   "foo",
			wantNumber: 1,
		},
		{
			name:       "crlf second line anchor",
			content:    []byte("foo\r\nbar\r\n"),
			pattern:    `^bar$`,
			wantLine:   "bar",
			wantNumber: 2,
		},
		{
			name:       "utf8 bom before first line anchor",
			content:    append([]byte{0xef, 0xbb, 0xbf}, []byte("package foo\n")...),
			pattern:    `^package`,
			wantLine:   "package foo",
			wantNumber: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "input.txt")
			if err := os.WriteFile(path, tt.content, 0o644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			results, err := grepSearch(context.Background(), grepSearchParams{
				root:        path,
				displayPath: "input.txt",
				pattern:     tt.pattern,
			})
			if err != nil {
				t.Fatalf("grepSearch: %v", err)
			}
			if len(results) != 1 {
				t.Fatalf("len(results) = %d, want 1", len(results))
			}
			matches := results[0].matches
			if len(matches) != 1 {
				t.Fatalf("len(matches) = %d, want 1: %#v", len(matches), matches)
			}
			if matches[0].lineNumber != tt.wantNumber {
				t.Errorf("lineNumber = %d, want %d", matches[0].lineNumber, tt.wantNumber)
			}
			if matches[0].line != tt.wantLine {
				t.Errorf("line = %q, want %q", matches[0].line, tt.wantLine)
			}
			if matches[0].column != 1 {
				t.Errorf("column = %d, want 1", matches[0].column)
			}
		})
	}
}

func TestGrepSearch_MultilineAnchorsAndInlineOverrides(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		pattern   string
		wantLines []int
	}{
		{
			name:      "line anchors match later lines",
			content:   "func a\nbody\nfunc b\n",
			pattern:   `^func`,
			wantLines: []int{1, 3},
		},
		{
			name:      "caret anchors match every real line",
			content:   "alpha\nbeta\n",
			pattern:   `^`,
			wantLines: []int{1, 2},
		},
		{
			name:      "dollar anchors match every real line",
			content:   "alpha\nbeta\n",
			pattern:   `$`,
			wantLines: []int{1, 2},
		},
		{
			name:      "inline flag disables multiline anchors",
			content:   "func a\nbody\nfunc b\n",
			pattern:   `(?-m)^func`,
			wantLines: []int{1},
		},
		{
			name:      "dot matches newlines by default",
			content:   "alpha\nbeta",
			pattern:   `alpha.beta`,
			wantLines: []int{1, 2},
		},
		{
			name:    "inline flag disables dotall",
			content: "alpha\nbeta",
			pattern: `alpha(?-s).beta`,
		},
		{
			name:      "zero width match at eof marks final line",
			content:   "alpha\nbeta",
			pattern:   `\z`,
			wantLines: []int{2},
		},
		{
			name:    "zero width match at eof ignores synthetic trailing line",
			content: "alpha\nbeta\n",
			pattern: `\z`,
		},
		{
			name:      "crlf anchors across lines",
			content:   "foo\r\nbar\r\n",
			pattern:   "foo$\nbar",
			wantLines: []int{1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "input.txt")
			if err := os.WriteFile(path, []byte(tt.content), 0o644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			results, err := grepSearch(context.Background(), grepSearchParams{
				root:        path,
				displayPath: "input.txt",
				pattern:     tt.pattern,
				multiline:   true,
			})
			if err != nil {
				t.Fatalf("grepSearch: %v", err)
			}

			var got []int
			if len(results) == 1 {
				for _, match := range results[0].matches {
					got = append(got, match.lineNumber)
				}
			}
			if len(got) != len(tt.wantLines) {
				t.Fatalf("matched lines = %v, want %v", got, tt.wantLines)
			}
			for i := range got {
				if got[i] != tt.wantLines[i] {
					t.Fatalf("matched lines = %v, want %v", got, tt.wantLines)
				}
			}
		})
	}
}

func TestGrepMarkMatchedLines_ZeroWidthEOF(t *testing.T) {
	tests := []struct {
		name   string
		lines  []string
		starts []int
		want   []bool
	}{
		{
			name:   "final non-empty line",
			lines:  []string{"alpha", "beta"},
			starts: []int{0, 6},
			want:   []bool{false, true},
		},
		{
			name:   "trailing newline synthetic line",
			lines:  []string{"alpha", ""},
			starts: []int{0, 6},
			want:   []bool{false, false},
		},
		{
			name:   "empty content",
			lines:  []string{""},
			starts: []int{0},
			want:   []bool{false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			last := len(tt.lines) - 1
			eof := tt.starts[last] + len(tt.lines[last])
			got := make([]bool, len(tt.lines))
			grepMarkMatchedLines(got, tt.lines, tt.starts, eof, eof)
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d marked = %t, want %t", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestGrepSearch_UsesSharedBinaryProbe(t *testing.T) {
	tests := []struct {
		name        string
		nulOffset   int
		wantMatched bool
	}{
		{
			name:        "nul beyond old 512 byte probe",
			nulOffset:   512,
			wantMatched: false,
		},
		{
			name:        "nul inside shared 8192 byte probe",
			nulOffset:   binaryProbeBytes - 1,
			wantMatched: false,
		},
		{
			name:        "nul past shared 8192 byte probe",
			nulOffset:   binaryProbeBytes,
			wantMatched: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			content := make([]byte, binaryProbeBytes+128)
			for i := range content {
				content[i] = 'a'
			}
			copy(content, "needle\n")
			content[tt.nulOffset] = 0
			path := filepath.Join(dir, "probe.txt")
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatalf("write probe file: %v", err)
			}

			results, err := grepSearch(context.Background(), grepSearchParams{
				root:        path,
				displayPath: "probe.txt",
				pattern:     "needle",
			})
			if err != nil {
				t.Fatalf("grepSearch: %v", err)
			}
			if got := len(results) == 1; got != tt.wantMatched {
				t.Fatalf("matched = %t, want %t", got, tt.wantMatched)
			}
		})
	}
}

func TestGrepTool_TypeFilterAliasesAndValidation(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{
		"main.go":   "needle\n",
		"script.ts": "needle\n",
		"notes.txt": "needle\n",
	})

	tests := []struct {
		name       string
		fileType   string
		wantOutput string
		wantErr    string
	}{
		{name: "canonical go", fileType: "go", wantOutput: "main.go"},
		{name: "case insensitive go", fileType: "GO", wantOutput: "main.go"},
		{name: "golang alias", fileType: "golang", wantOutput: "main.go"},
		{name: "mixed case golang alias", fileType: "GoLang", wantOutput: "main.go"},
		{name: "case insensitive typescript", fileType: "TS", wantOutput: "script.ts"},
		{name: "unknown type", fileType: "content", wantErr: "unknown type"},
		{name: "bogus type", fileType: "bogus", wantErr: "unknown type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultI, err := toolDef.Handler(context.Background(), map[string]any{
				"pattern":     "needle",
				"output_mode": "files_with_matches",
				"type":        tt.fileType,
			})
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			result, ok := resultI.(GrepResult)
			if !ok {
				t.Fatalf("result type = %T, want GrepResult", resultI)
			}
			if strings.TrimSpace(result.Output) != tt.wantOutput {
				t.Fatalf("Output = %q, want %q", result.Output, tt.wantOutput)
			}
		})
	}

	if names := grepTypeNames(); !sort.StringsAreSorted(names) {
		t.Fatalf("grepTypeNames() = %v, want sorted order", names)
	}
}

func TestGrepTool_RejectsNegativeContext(t *testing.T) {
	toolDef := newGrepBudgetTool(t, map[string]string{"file.txt": "needle\n"})

	tests := []struct {
		name  string
		input map[string]any
	}{
		{name: "negative context", input: map[string]any{"pattern": "needle", "context": -1}},
		{name: "negative before context", input: map[string]any{"pattern": "needle", "before_context": -1}},
		{name: "negative after context", input: map[string]any{"pattern": "needle", "after_context": -1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := toolDef.Handler(context.Background(), tt.input)
			if err == nil {
				t.Fatal("expected negative context error")
			}
			if !strings.Contains(err.Error(), "context") {
				t.Fatalf("error = %q, want context validation error", err)
			}
		})
	}
}
