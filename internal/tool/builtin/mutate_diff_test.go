package builtin

import (
	"fmt"
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

func TestComputeLineDiffEdgeCases(t *testing.T) {
	t.Run("both empty", func(t *testing.T) {
		edits := computeLineDiff(nil, nil)
		if len(edits) != 0 {
			t.Fatalf("len = %d, want 0", len(edits))
		}
	})

	t.Run("single line to different single line", func(t *testing.T) {
		edits := computeLineDiff([]string{"old"}, []string{"new"})
		del, ins := 0, 0
		for _, e := range edits {
			switch e.Op {
			case diffDelete:
				del++
			case diffInsert:
				ins++
			}
		}
		if del != 1 || ins != 1 {
			t.Fatalf("del=%d ins=%d, want 1/1", del, ins)
		}
	})

	t.Run("insertion only no deletions", func(t *testing.T) {
		a := []string{"a", "c"}
		b := []string{"a", "b", "c"}
		edits := computeLineDiff(a, b)
		del, ins, eq := 0, 0, 0
		for _, e := range edits {
			switch e.Op {
			case diffDelete:
				del++
			case diffInsert:
				ins++
			case diffEqual:
				eq++
			}
		}
		if del != 0 || ins != 1 || eq != 2 {
			t.Fatalf("del=%d ins=%d eq=%d, want 0/1/2", del, ins, eq)
		}
	})

	t.Run("deletion only no insertions", func(t *testing.T) {
		a := []string{"a", "b", "c"}
		b := []string{"a", "c"}
		edits := computeLineDiff(a, b)
		del, ins, eq := 0, 0, 0
		for _, e := range edits {
			switch e.Op {
			case diffDelete:
				del++
			case diffInsert:
				ins++
			case diffEqual:
				eq++
			}
		}
		if del != 1 || ins != 0 || eq != 2 {
			t.Fatalf("del=%d ins=%d eq=%d, want 1/0/2", del, ins, eq)
		}
	})

	t.Run("multiple scattered changes", func(t *testing.T) {
		a := []string{"a", "b", "c", "d", "e"}
		b := []string{"A", "b", "C", "d", "E"}
		edits := computeLineDiff(a, b)
		del, ins := 0, 0
		for _, e := range edits {
			switch e.Op {
			case diffDelete:
				del++
			case diffInsert:
				ins++
			}
		}
		if del != 3 || ins != 3 {
			t.Fatalf("del=%d ins=%d, want 3/3", del, ins)
		}
	})
}

func TestFormatUnifiedHunksEdgeCases(t *testing.T) {
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
			name:      "both empty",
			a:         nil,
			b:         nil,
			wantEmpty: true,
		},
		{
			name:         "change at first line",
			a:            []string{"old", "b", "c", "d", "e", "f", "g", "h"},
			b:            []string{"new", "b", "c", "d", "e", "f", "g", "h"},
			wantHunks:    1,
			wantContains: []string{"@@ -1,4 +1,4 @@", "-old", "+new"},
		},
		{
			name:         "change at last line",
			a:            []string{"a", "b", "c", "d", "e", "f", "g", "old"},
			b:            []string{"a", "b", "c", "d", "e", "f", "g", "new"},
			wantHunks:    1,
			wantContains: []string{"-old", "+new"},
		},
		{
			name: "three separate hunks",
			a: func() []string {
				l := make([]string, 50)
				for i := range l {
					l[i] = "ctx"
				}
				l[2] = "old1"
				l[24] = "old2"
				l[47] = "old3"
				return l
			}(),
			b: func() []string {
				l := make([]string, 50)
				for i := range l {
					l[i] = "ctx"
				}
				l[2] = "new1"
				l[24] = "new2"
				l[47] = "new3"
				return l
			}(),
			wantHunks:    3,
			wantContains: []string{"-old1", "+new1", "-old2", "+new2", "-old3", "+new3"},
		},
		{
			name: "hunks at exact merge boundary",
			a: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "ctx"
				}
				l[3] = "old1"
				l[10] = "old2"
				return l
			}(),
			b: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "ctx"
				}
				l[3] = "new1"
				l[10] = "new2"
				return l
			}(),
			wantHunks:    1,
			wantContains: []string{"-old1", "+new1", "-old2", "+new2"},
		},
		{
			name: "hunks just past merge boundary stay separate",
			a: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "ctx"
				}
				l[2] = "old1"
				l[10] = "old2"
				return l
			}(),
			b: func() []string {
				l := make([]string, 20)
				for i := range l {
					l[i] = "ctx"
				}
				l[2] = "new1"
				l[10] = "new2"
				return l
			}(),
			wantHunks:    2,
			wantContains: []string{"-old1", "+new1", "-old2", "+new2"},
		},
		{
			name:            "insertion only adds lines mid-file",
			a:               []string{"a", "b", "c"},
			b:               []string{"a", "x", "y", "b", "c"},
			wantHunks:       1,
			wantContains:    []string{"+x", "+y"},
			wantNotContains: []string{"-a", "-b", "-c"},
		},
		{
			name:            "deletion only removes lines mid-file",
			a:               []string{"a", "x", "y", "b", "c"},
			b:               []string{"a", "b", "c"},
			wantHunks:       1,
			wantContains:    []string{"-x", "-y"},
			wantNotContains: []string{"+a", "+b", "+c"},
		},
		{
			name:         "context clamped at file start",
			a:            []string{"old", "b"},
			b:            []string{"new", "b"},
			wantHunks:    1,
			wantContains: []string{"@@ -1,2 +1,2 @@", "-old", "+new", " b"},
		},
		{
			name:         "context clamped at file end",
			a:            []string{"a", "old"},
			b:            []string{"a", "new"},
			wantHunks:    1,
			wantContains: []string{" a", "-old", "+new"},
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

func TestUnifiedTextDiffLargeFileSingleChange(t *testing.T) {
	lines := make([]string, 273)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	before := strings.Join(lines, "\n") + "\n"
	lines[136] = "MODIFIED"
	after := strings.Join(lines, "\n") + "\n"

	got := unifiedTextDiff("README.md", before, after)
	if got == "" {
		t.Fatal("expected non-empty diff")
	}
	hunkCount := strings.Count(got, "@@ -")
	if hunkCount != 1 {
		t.Fatalf("hunk count = %d, want 1\ndiff:\n%s", hunkCount, got)
	}
	diffLines := strings.Split(got, "\n")
	if len(diffLines) > 15 {
		t.Fatalf("diff has %d lines for 1-line change in 273-line file, want <= 15\ndiff:\n%s", len(diffLines), got)
	}
	if !strings.Contains(got, "-line 137") {
		t.Fatal("diff missing deleted line")
	}
	if !strings.Contains(got, "+MODIFIED") {
		t.Fatal("diff missing inserted line")
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
