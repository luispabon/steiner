package builtin

import (
	"strings"
	"testing"
)

func TestComputeLineDiff(t *testing.T) {
	t.Run("identical", func(t *testing.T) {
		a := []string{"a", "b", "c"}
		edits := computeLineDiff(a, a)
		for _, e := range edits {
			if e.Op != diffEqual {
				t.Fatalf("expected all equal, got op=%d line=%q", e.Op, e.Line)
			}
		}
	})

	t.Run("empty to content", func(t *testing.T) {
		edits := computeLineDiff(nil, []string{"x", "y"})
		if len(edits) != 2 {
			t.Fatalf("len = %d, want 2", len(edits))
		}
		for _, e := range edits {
			if e.Op != diffInsert {
				t.Fatalf("expected insert, got %d", e.Op)
			}
		}
	})

	t.Run("content to empty", func(t *testing.T) {
		edits := computeLineDiff([]string{"x", "y"}, nil)
		if len(edits) != 2 {
			t.Fatalf("len = %d, want 2", len(edits))
		}
		for _, e := range edits {
			if e.Op != diffDelete {
				t.Fatalf("expected delete, got %d", e.Op)
			}
		}
	})

	t.Run("single substitution", func(t *testing.T) {
		a := []string{"a", "b", "c"}
		b := []string{"a", "X", "c"}
		edits := computeLineDiff(a, b)
		deleteCount, insertCount, equalCount := 0, 0, 0
		for _, e := range edits {
			switch e.Op {
			case diffDelete:
				deleteCount++
			case diffInsert:
				insertCount++
			case diffEqual:
				equalCount++
			}
		}
		if deleteCount != 1 || insertCount != 1 || equalCount != 2 {
			t.Fatalf("delete=%d insert=%d equal=%d, want 1/1/2", deleteCount, insertCount, equalCount)
		}
	})
}

func TestFormatUnifiedHunks(t *testing.T) {
	tests := []struct {
		name            string
		a               []string
		b               []string
		wantEmpty       bool
		wantHunks       int
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:      "no change",
			a:         []string{"a", "b", "c"},
			b:         []string{"a", "b", "c"},
			wantEmpty: true,
		},
		{
			name:         "full file creation",
			a:            nil,
			b:            []string{"line1", "line2", "line3"},
			wantHunks:    1,
			wantContains: []string{"--- path\n+++ path\n", "@@ -", "+line1", "+line2", "+line3"},
		},
		{
			name:         "full file deletion",
			a:            []string{"line1", "line2", "line3"},
			b:            nil,
			wantHunks:    1,
			wantContains: []string{"--- path\n+++ path\n", "@@ -", "-line1", "-line2", "-line3"},
		},
		{
			name: "single line change mid file",
			a: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "line"
				}
				l[9] = "old"
				return l
			}(),
			b: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "line"
				}
				l[9] = "new"
				return l
			}(),
			wantHunks:    1,
			wantContains: []string{"-old", "+new"},
		},
		{
			name: "adjacent changes merged into one hunk",
			a: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "line"
				}
				l[4] = "old5"
				l[6] = "old7"
				return l
			}(),
			b: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "line"
				}
				l[4] = "new5"
				l[6] = "new7"
				return l
			}(),
			wantHunks:    1,
			wantContains: []string{"-old5", "+new5", "-old7", "+new7"},
		},
		{
			name: "distant changes produce two hunks",
			a: func() []string {
				l := make([]string, 100)
				for i := range l {
					l[i] = "line"
				}
				l[4] = "old5"
				l[49] = "old50"
				return l
			}(),
			b: func() []string {
				l := make([]string, 100)
				for i := range l {
					l[i] = "line"
				}
				l[4] = "new5"
				l[49] = "new50"
				return l
			}(),
			wantHunks:    2,
			wantContains: []string{"-old5", "+new5", "-old50", "+new50"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatUnifiedHunks("path", tc.a, tc.b, 3)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("expected empty string, got %q", got)
				}
				return
			}
			if got == "" {
				t.Fatal("expected non-empty diff output")
			}
			if tc.wantHunks > 0 {
				hunkCount := strings.Count(got, "@@ -")
				if hunkCount != tc.wantHunks {
					t.Fatalf("hunk count = %d, want %d\ndiff:\n%s", hunkCount, tc.wantHunks, got)
				}
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Fatalf("diff missing %q\ndiff:\n%s", want, got)
				}
			}
			for _, notwant := range tc.wantNotContains {
				if strings.Contains(got, notwant) {
					t.Fatalf("diff should not contain %q\ndiff:\n%s", notwant, got)
				}
			}
		})
	}
}

func TestUnifiedTextDiffCRLF(t *testing.T) {
	before := "line1\r\nline2\r\nold\r\nline4\r\n"
	after := "line1\r\nline2\r\nnew\r\nline4\r\n"
	got := unifiedTextDiff("test.txt", before, after)
	if got == "" {
		t.Fatal("expected non-empty diff for CRLF input")
	}
	if !strings.Contains(got, "--- test.txt") {
		t.Fatalf("missing header in diff: %q", got)
	}
	if !strings.Contains(got, "-old") {
		t.Fatalf("missing -old in diff: %q", got)
	}
	if !strings.Contains(got, "+new") {
		t.Fatalf("missing +new in diff: %q", got)
	}
}

func TestUnifiedTextDiffNoChange(t *testing.T) {
	content := "a\nb\nc\n"
	got := unifiedTextDiff("file.txt", content, content)
	if got != "" {
		t.Fatalf("expected empty diff for identical content, got %q", got)
	}
}
