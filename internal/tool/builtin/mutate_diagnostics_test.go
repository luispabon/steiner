package builtin

import (
	"strings"
	"testing"
)

func TestNormalizedWhitespaceMatchExists(t *testing.T) {
	tests := []struct {
		name    string
		content string
		old     string
		want    bool
	}{
		{name: "tab vs space", content: "\tindented", old: "    indented", want: true},
		{name: "mixed whitespace", content: "\t  mixed\ttabs", old: "mixed tabs", want: true},
		{name: "different text", content: "alpha beta", old: "gamma delta", want: false},
		{name: "empty old string", content: "alpha beta", old: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizedWhitespaceMatchExists([]byte(tt.content), tt.old)
			if got != tt.want {
				t.Fatalf("normalizedWhitespaceMatchExists(%q, %q) = %v, want %v", tt.content, tt.old, got, tt.want)
			}
		})
	}
}

func TestDiagnosticAnchorCandidates(t *testing.T) {
	t.Run("single line returns trimmed line and field spans", func(t *testing.T) {
		candidates := diagnosticAnchorCandidates("  hello world  ")
		found := false
		for _, c := range candidates {
			if c == "hello world" {
				found = true
			}
		}
		if !found {
			t.Fatalf("candidates %v missing trimmed line", candidates)
		}
	})

	t.Run("multi-line returns candidates per line", func(t *testing.T) {
		candidates := diagnosticAnchorCandidates("alpha beta\ngamma delta")
		hasAlpha := false
		hasGamma := false
		for _, c := range candidates {
			if c == "alpha beta" {
				hasAlpha = true
			}
			if c == "gamma delta" {
				hasGamma = true
			}
		}
		if !hasAlpha || !hasGamma {
			t.Fatalf("candidates %v missing expected lines", candidates)
		}
	})

	t.Run("short tokens filtered", func(t *testing.T) {
		candidates := diagnosticAnchorCandidates("ab cd")
		for _, c := range candidates {
			if len(c) < 4 {
				t.Fatalf("candidate %q is shorter than 4 chars", c)
			}
		}
	})

	t.Run("no duplicates", func(t *testing.T) {
		candidates := diagnosticAnchorCandidates("hello hello hello")
		seen := make(map[string]int)
		for _, c := range candidates {
			seen[c]++
		}
		for c, n := range seen {
			if n > 1 {
				t.Fatalf("duplicate candidate %q appears %d times", c, n)
			}
		}
	})
}

func TestFindDiagnosticAnchor(t *testing.T) {
	t.Run("exact substring found", func(t *testing.T) {
		content := []byte("line one\nline two\nline three\n")
		start, end, lineNum, _, ok := findDiagnosticAnchor(content, "line two")
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if lineNum != 2 {
			t.Fatalf("lineNum = %d, want 2", lineNum)
		}
		if string(content[start:end]) != "line two" {
			t.Fatalf("content[start:end] = %q, want %q", string(content[start:end]), "line two")
		}
	})

	t.Run("longest candidate wins", func(t *testing.T) {
		content := []byte("alpha beta gamma\ndelta\n")
		_, _, lineNum, preview, ok := findDiagnosticAnchor(content, "alpha beta gamma\ndelta")
		if !ok {
			t.Fatal("ok = false, want true")
		}
		if lineNum < 1 {
			t.Fatalf("lineNum = %d, want >= 1", lineNum)
		}
		if len(preview) == 0 {
			t.Fatal("preview is empty")
		}
	})

	t.Run("no match returns ok=false", func(t *testing.T) {
		content := []byte("hello world\n")
		_, _, _, _, ok := findDiagnosticAnchor(content, "zzz qqq rrr")
		if ok {
			t.Fatal("ok = true, want false")
		}
	})
}

func TestExtractNormalizedMatch(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		old         string
		wantText    string
		wantLineNum int
		wantOk      bool
	}{
		{
			name:        "single-line tab vs space match",
			content:     "\tindented line\n",
			old:         "    indented line",
			wantText:    "\tindented line",
			wantLineNum: 1,
			wantOk:      true,
		},
		{
			name:        "multi-line match",
			content:     "header\n\talpha\n\tbeta\nfooter\n",
			old:         "    alpha\n    beta",
			wantText:    "\talpha\n\tbeta",
			wantLineNum: 2,
			wantOk:      true,
		},
		{
			name:    "no match returns ok=false",
			content: "hello world\n",
			old:     "goodbye world",
			wantOk:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matchedText, lineNum, ok := extractNormalizedMatch([]byte(tt.content), tt.old)
			if ok != tt.wantOk {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if matchedText != tt.wantText {
				t.Fatalf("matchedText = %q, want %q", matchedText, tt.wantText)
			}
			if lineNum != tt.wantLineNum {
				t.Fatalf("lineNum = %d, want %d", lineNum, tt.wantLineNum)
			}
		})
	}
}

func TestBuildNoMatchDiagnostics(t *testing.T) {
	const absPath = "/tmp/work/note.txt"
	t.Run("no anchor found", func(t *testing.T) {
		content := []byte("hello world\n")
		out := buildNoMatchDiagnostics("edit", content, "nonexistent text", absPath)
		if !strings.Contains(out, "edit: no match for old_string") {
			t.Fatalf("output = %q, want no-match prefix", out)
		}
		if !strings.Contains(out, absPath) {
			t.Fatalf("output = %q, want absolute path %q in header", out, absPath)
		}
		if !strings.HasPrefix(strings.SplitN(out, "\n", 2)[0], "edit: no match for old_string in "+absPath) {
			t.Fatalf("first line of output must be the abs-path header, got %q", out)
		}
		if !strings.Contains(out, "no nearby exact anchor found") {
			t.Fatalf("output = %q, want anchor diagnostic", out)
		}
		if !strings.Contains(out, "suggestion: reread a slightly wider region") {
			t.Fatalf("output = %q, want reread suggestion", out)
		}
	})

	t.Run("whitespace mismatch", func(t *testing.T) {
		content := []byte("alpha beta\n")
		out := buildNoMatchDiagnostics("edit", content, "alpha   beta", absPath)
		if !strings.Contains(out, absPath) {
			t.Fatalf("output = %q, want absolute path %q", out, absPath)
		}
		if !strings.Contains(out, "normalized whitespace match exists") {
			t.Fatalf("output = %q, want whitespace diagnostic", out)
		}
		if !strings.Contains(out, "nearest anchor at line 1") {
			t.Fatalf("output = %q, want anchor diagnostic", out)
		}
		if !strings.Contains(out, "context:") {
			t.Fatalf("output = %q, want context preview", out)
		}
		if !strings.Contains(out, "retry with old_string set to the file text shown above") {
			t.Fatalf("output = %q, want whitespace-specific suggestion", out)
		}
	})

	t.Run("tab vs space with line_replace suggestion", func(t *testing.T) {
		content := []byte("check:\n\tgo test ./...\n\tgo vet ./...\n")
		out := buildNoMatchDiagnostics("edit", content, "check:\n    go test ./...\n    go vet ./...\n", absPath)
		if !strings.Contains(out, absPath) {
			t.Fatalf("output = %q, want absolute path %q", out, absPath)
		}
		if !strings.Contains(out, "normalized whitespace match exists") {
			t.Fatalf("output = %q, want whitespace diagnostic", out)
		}
		if !strings.Contains(out, "nearest anchor at line") {
			t.Fatalf("output = %q, want anchor diagnostic", out)
		}
		if !strings.Contains(out, "line_replace") {
			t.Fatalf("output = %q, want line_replace suggestion", out)
		}
	})
}

func TestBuildAmbiguousDiagnostics(t *testing.T) {
	const absPath = "/tmp/work/note.txt"
	t.Run("shows occurrence count and context", func(t *testing.T) {
		content := []byte("hello\nworld\nhello\nworld\nhello\n")
		out := buildAmbiguousDiagnostics("edit", content, "hello", 3, absPath)
		if !strings.Contains(out, "ambiguous match") {
			t.Fatalf("output = %q, want ambiguous match", out)
		}
		if !strings.Contains(out, absPath) {
			t.Fatalf("output = %q, want absolute path %q in header", out, absPath)
		}
		if !strings.HasPrefix(strings.SplitN(out, "\n", 2)[0], "edit: ambiguous match for old_string in "+absPath+" (found 3 occurrences)") {
			t.Fatalf("first line of output must be the abs-path header, got %q", out)
		}
		if !strings.Contains(out, "3 occurrences") {
			t.Fatalf("output = %q, want 3 occurrences", out)
		}
		if !strings.Contains(out, "closest occurrence at line") {
			t.Fatalf("output = %q, want closest occurrence at line", out)
		}
		if !strings.Contains(out, "context:") {
			t.Fatalf("output = %q, want context preview", out)
		}
	})

	t.Run("two occurrences", func(t *testing.T) {
		content := []byte("hello\nhello\nworld\n")
		out := buildAmbiguousDiagnostics("edit", content, "hello", 2, absPath)
		if !strings.Contains(out, "ambiguous match for old_string") {
			t.Fatalf("output = %q, want ambiguous match message", out)
		}
		if !strings.Contains(out, absPath) {
			t.Fatalf("output = %q, want absolute path %q", out, absPath)
		}
		if !strings.Contains(out, "2 occurrences") {
			t.Fatalf("output = %q, want occurrence count", out)
		}
	})
}

func TestLineNumberAt(t *testing.T) {
	content := []byte("line one\nline two\nline three\n")
	tests := []struct {
		offset   int
		wantLine int
	}{
		{0, 1},
		{8, 1},  // at the '\n'
		{9, 2},  // start of line two
		{17, 2}, // at the second '\n'
		{18, 3}, // start of line three
	}
	for _, tt := range tests {
		got := lineNumberAt(content, tt.offset)
		if got != tt.wantLine {
			t.Errorf("lineNumberAt(content, %d) = %d, want %d", tt.offset, got, tt.wantLine)
		}
	}
}

func TestTruncatePreviewLine(t *testing.T) {
	tests := []struct {
		s     string
		limit int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
		{"hello", 0, "hello"},
	}
	for _, tt := range tests {
		got := truncatePreviewLine(tt.s, tt.limit)
		if got != tt.want {
			t.Errorf("truncatePreviewLine(%q, %d) = %q, want %q", tt.s, tt.limit, got, tt.want)
		}
	}
}

func TestPreviewContext(t *testing.T) {
	content := []byte("line one\nline two\nline three\n")

	t.Run("middle line highlighted", func(t *testing.T) {
		lines := previewContext(content, 2, "line two")
		found := false
		for _, l := range lines {
			if strings.Contains(l, "> ") && strings.Contains(l, "line two") {
				found = true
			}
		}
		if !found {
			t.Fatalf("previewContext lines %v missing highlighted line two", lines)
		}
	})

	t.Run("returns surrounding context", func(t *testing.T) {
		lines := previewContext(content, 2, "line two")
		if len(lines) < 2 {
			t.Fatalf("previewContext returned %d lines, want at least 2", len(lines))
		}
	})
}
