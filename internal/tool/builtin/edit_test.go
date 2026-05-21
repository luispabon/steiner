package builtin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/tool"
)

func TestEditTool(t *testing.T) {
	tmpDir := t.TempDir()
	policy := tool.NewPathPolicy(tmpDir, config.PathsConfig{})
	env := Env{WorkDir: tmpDir, PathPolicy: &policy}
	toolDef := NewEditTool(env)
	ctx := context.Background()

	run := func(t *testing.T, fileName string, content []byte, input map[string]any) (*MutationResult, []byte) {
		t.Helper()
		fullPath := filepath.Join(tmpDir, fileName)
		if err := os.WriteFile(fullPath, content, 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		resultI, err := toolDef.Handler(ctx, input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		result, ok := resultI.(*MutationResult)
		if !ok {
			t.Fatalf("result type = %T, want *MutationResult", resultI)
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			t.Fatalf("read test file: %v", err)
		}
		return result, data
	}

	t.Run("single exact replacement", func(t *testing.T) {
		result, data := run(t, "single.txt", []byte("hello world\nfoo bar\nbaz qux\n"), map[string]any{
			"path":       "single.txt",
			"old_string": "foo bar",
			"new_string": "replaced",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if result.Path != "single.txt" {
			t.Fatalf("Path = %q, want %q", result.Path, "single.txt")
		}
		if got, want := string(data), "hello world\nreplaced\nbaz qux\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: replaced 1 occurrence") {
			t.Fatalf("Output = %q, want replaced prefix", result.Output)
		}
	})

	t.Run("no match", func(t *testing.T) {
		result, data := run(t, "nomatch.txt", []byte("hello world\n"), map[string]any{
			"path":       "nomatch.txt",
			"old_string": "nonexistent text",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "hello world\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: no match for old_string") {
			t.Fatalf("Output = %q, want no-match prefix", result.Output)
		}
		if !strings.Contains(result.Output, "no nearby exact anchor found") {
			t.Fatalf("Output = %q, want anchor diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "suggestion: reread a slightly wider region") {
			t.Fatalf("Output = %q, want reread suggestion", result.Output)
		}
	})

	t.Run("whitespace_only_mismatch", func(t *testing.T) {
		result, data := run(t, "whitespace.txt", []byte("alpha beta\n"), map[string]any{
			"path":       "whitespace.txt",
			"old_string": "alpha   beta",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "alpha beta\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "normalized whitespace match exists") {
			t.Fatalf("Output = %q, want whitespace diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "nearest anchor at line 1") {
			t.Fatalf("Output = %q, want anchor diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "context:") {
			t.Fatalf("Output = %q, want context preview", result.Output)
		}
		if !strings.Contains(result.Output, "suggestion: use line_replace with line") {
			t.Fatalf("Output = %q, want whitespace-specific suggestion", result.Output)
		}
	})

	t.Run("tab_vs_space_mismatch", func(t *testing.T) {
		result, _ := run(t, "tabspace.txt", []byte("check:\n\tgo test ./...\n\tgo vet ./...\n"), map[string]any{
			"path":       "tabspace.txt",
			"old_string": "check:\n    go test ./...\n    go vet ./...\n",
			"new_string": "check:\n\tgo test -race ./...\n",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if !strings.Contains(result.Output, "normalized whitespace match exists") {
			t.Fatalf("Output = %q, want whitespace diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "nearest anchor at line") {
			t.Fatalf("Output = %q, want anchor diagnostic", result.Output)
		}
		if !strings.Contains(result.Output, "line_replace") {
			t.Fatalf("Output = %q, want line_replace suggestion", result.Output)
		}
	})

	t.Run("multiline_whitespace_mismatch", func(t *testing.T) {
		result, _ := run(t, "tabspace2.txt", []byte("\talpha\n\tbeta\n\tgamma\n"), map[string]any{
			"path":       "tabspace2.txt",
			"old_string": "    alpha\n    beta\n    gamma\n",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if !strings.Contains(result.Output, "normalized whitespace match exists") {
			t.Fatalf("Output = %q, want whitespace mismatch diagnostic", result.Output)
		}
	})

	t.Run("ambiguous_match_shows_context", func(t *testing.T) {
		result, _ := run(t, "ambiguous3.txt", []byte("hello\nworld\nhello\nworld\nhello\n"), map[string]any{
			"path":       "ambiguous3.txt",
			"old_string": "hello",
			"new_string": "hi",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if !strings.Contains(result.Output, "ambiguous match") {
			t.Fatalf("Output = %q, want ambiguous match", result.Output)
		}
		if !strings.Contains(result.Output, "3 occurrences") {
			t.Fatalf("Output = %q, want 3 occurrences", result.Output)
		}
		if !strings.Contains(result.Output, "closest occurrence at line") {
			t.Fatalf("Output = %q, want closest occurrence at line", result.Output)
		}
		if !strings.Contains(result.Output, "context:") {
			t.Fatalf("Output = %q, want context preview", result.Output)
		}
	})

	t.Run("ambiguous match with replace_all false", func(t *testing.T) {
		result, data := run(t, "ambiguous.txt", []byte("hello\nhello\nworld\n"), map[string]any{
			"path":       "ambiguous.txt",
			"old_string": "hello",
			"new_string": "hi",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := string(data), "hello\nhello\nworld\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "ambiguous match for old_string") {
			t.Fatalf("Output = %q, want ambiguous match message", result.Output)
		}
		if !strings.Contains(result.Output, "2 occurrences") {
			t.Fatalf("Output = %q, want occurrence count", result.Output)
		}
	})

	t.Run("ambiguous match with replace_all true", func(t *testing.T) {
		result, data := run(t, "replace_all.txt", []byte("hello\nhello\nworld\n"), map[string]any{
			"path":        "replace_all.txt",
			"old_string":  "hello",
			"new_string":  "hi",
			"replace_all": true,
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "hi\nhi\nworld\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
		if !strings.Contains(result.Output, "edit: replaced 2 occurrences") {
			t.Fatalf("Output = %q, want replaced prefix", result.Output)
		}
	})

	t.Run("trailing eof hunk ending in braces", func(t *testing.T) {
		result, data := run(t, "trailing.txt", []byte("func main() {\n\tprintln(\"hi\")\n}\n"), map[string]any{
			"path":       "trailing.txt",
			"old_string": "}\n",
			"new_string": "}\n\n// done\n",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "func main() {\n\tprintln(\"hi\")\n}\n\n// done\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("no final newline file", func(t *testing.T) {
		result, data := run(t, "nonewline.txt", []byte("alpha\nbeta"), map[string]any{
			"path":       "nonewline.txt",
			"old_string": "beta",
			"new_string": "gamma",
		})
		if !result.Mutated {
			t.Fatal("Mutated = false, want true")
		}
		if got, want := string(data), "alpha\ngamma"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("binary file rejection", func(t *testing.T) {
		result, data := run(t, "binary.bin", []byte{'a', 0, 'b', 'c'}, map[string]any{
			"path":       "binary.bin",
			"old_string": "a",
			"new_string": "z",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if !strings.Contains(result.Output, "binary") {
			t.Fatalf("Output = %q, want binary rejection", result.Output)
		}
		if got, want := string(data), string([]byte{'a', 0, 'b', 'c'}); got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})

	t.Run("empty old_string rejection", func(t *testing.T) {
		result, data := run(t, "emptyold.txt", []byte("hello world\n"), map[string]any{
			"path":       "emptyold.txt",
			"old_string": "",
			"new_string": "replaced",
		})
		if result.Mutated {
			t.Fatal("Mutated = true, want false")
		}
		if got, want := result.Output, "edit: old_string is empty"; got != want {
			t.Fatalf("Output = %q, want %q", got, want)
		}
		if got, want := string(data), "hello world\n"; got != want {
			t.Fatalf("file content = %q, want %q", got, want)
		}
	})
}

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
