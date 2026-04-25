package output

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
)

func TestDetectPreviewSyntax(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		contents  string
		wantLang  string
		wantLexer string
	}{
		{
			name: "filename first",
			path: "main.py",
			contents: `package main
func main() {}
`,
			wantLang:  "python",
			wantLexer: "Python",
		},
		{
			name: "content analysis fallback",
			path: "snippet",
			contents: `package main
import "fmt"

func main() {
	fmt.Println("hi")
}
`,
			wantLang:  "go",
			wantLexer: "Go",
		},
		{
			name:      "binary-ish fallback",
			path:      "snippet.bin",
			contents:  "abc\x00def",
			wantLang:  "plain",
			wantLexer: "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectPreviewSyntax(tt.path, tt.contents)
			if got.Language != tt.wantLang {
				t.Fatalf("DetectPreviewSyntax().Language = %q, want %q", got.Language, tt.wantLang)
			}
			if got.Lexer == nil || got.Lexer.Config() == nil {
				t.Fatalf("DetectPreviewSyntax().Lexer is nil")
			}
			if gotLexer := got.Lexer.Config().Name; gotLexer != tt.wantLexer {
				t.Fatalf("DetectPreviewSyntax().Lexer.Config().Name = %q, want %q", gotLexer, tt.wantLexer)
			}
		})
	}
}

func TestFormatFilePreviewWithLimit(t *testing.T) {
	got := FormatFilePreviewWithLimit("main.go", `package main
func main() {
	fmt.Println("hi")
}
`, 10)

	if got.Kind != PreviewFormatKindFile {
		t.Fatalf("Kind = %q, want %q", got.Kind, PreviewFormatKindFile)
	}
	if got.Language != "go" {
		t.Fatalf("Language = %q, want go", got.Language)
	}
	if got.Truncated {
		t.Fatalf("Truncated = true, want false")
	}
	if len(got.Lines) != 4 {
		t.Fatalf("len(Lines) = %d, want 4", len(got.Lines))
	}
	if got.Lines[0].Kind != PreviewLineKindText {
		t.Fatalf("first line kind = %q, want %q", got.Lines[0].Kind, PreviewLineKindText)
	}
	if got.Lines[0].Prefix != "" {
		t.Fatalf("first line prefix = %q, want empty", got.Lines[0].Prefix)
	}
	if text := previewLineText(got.Lines[0]); text != "package main" {
		t.Fatalf("first line text = %q, want %q", text, "package main")
	}
	if !lineHasHighlightedSpan(got.Lines[1]) {
		t.Fatalf("expected highlighted spans on line 2")
	}
}

func TestFormatEditDiffPreviewWithLimit(t *testing.T) {
	got := FormatEditDiffPreviewWithLimit("main.go", `package main
fmt.Println("old")
`, `package main
fmt.Println("new")
`, 20)

	if got.Kind != PreviewFormatKindEditDiff {
		t.Fatalf("Kind = %q, want %q", got.Kind, PreviewFormatKindEditDiff)
	}
	if got.Language != "go" {
		t.Fatalf("Language = %q, want go", got.Language)
	}
	if got.Truncated {
		t.Fatalf("Truncated = true, want false")
	}
	if len(got.Lines) != 6 {
		t.Fatalf("len(Lines) = %d, want 6", len(got.Lines))
	}
	if got.Lines[0].Prefix != "---" || previewLineText(got.Lines[0]) != "main.go" {
		t.Fatalf("first header line = %#v, want --- main.go", got.Lines[0])
	}
	if got.Lines[1].Prefix != "+++" || previewLineText(got.Lines[1]) != "main.go" {
		t.Fatalf("second header line = %#v, want +++ main.go", got.Lines[1])
	}
	if got.Lines[2].Prefix != "@@" {
		t.Fatalf("hunk header prefix = %q, want @@", got.Lines[2].Prefix)
	}
	if got.Lines[3].Prefix != " " || previewLineText(got.Lines[3]) != "package main" {
		t.Fatalf("context line = %#v, want visible context line", got.Lines[3])
	}
	if got.Lines[4].Prefix != "-" || previewLineText(got.Lines[4]) != `fmt.Println("old")` {
		t.Fatalf("removed line = %#v, want removed payload only", got.Lines[4])
	}
	if got.Lines[5].Prefix != "+" || previewLineText(got.Lines[5]) != `fmt.Println("new")` {
		t.Fatalf("added line = %#v, want added payload only", got.Lines[5])
	}
}

func TestFormatFilePreviewTruncates(t *testing.T) {
	got := FormatFilePreviewWithLimit("notes.txt", "one\ntwo\nthree\n", 2)

	if !got.Truncated {
		t.Fatalf("Truncated = false, want true")
	}
	if len(got.Lines) != 3 {
		t.Fatalf("len(Lines) = %d, want 3", len(got.Lines))
	}
	if got.Lines[2].Kind != PreviewLineKindTruncated {
		t.Fatalf("truncation line kind = %q, want %q", got.Lines[2].Kind, PreviewLineKindTruncated)
	}
	if got.Lines[2].Prefix != "…" {
		t.Fatalf("truncation line prefix = %q, want ellipsis", got.Lines[2].Prefix)
	}
}

func previewLineText(line PreviewLine) string {
	var b strings.Builder
	for _, span := range line.Spans {
		b.WriteString(span.Text)
	}
	return b.String()
}

func lineHasHighlightedSpan(line PreviewLine) bool {
	for _, span := range line.Spans {
		if span.Type != chroma.Text {
			return true
		}
	}
	return false
}
