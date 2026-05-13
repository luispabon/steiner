package output

import "github.com/alecthomas/chroma/v2"

const defaultPreviewLineLimit = 120

// PreviewFormatKind identifies the structured preview document format.
type PreviewFormatKind string

const (
	// PreviewFormatKindFile renders a file body preview.
	PreviewFormatKindFile PreviewFormatKind = "file"
	// PreviewFormatKindEditDiff renders a diff-style preview.
	PreviewFormatKindEditDiff PreviewFormatKind = "edit_diff"
)

// PreviewLineKind identifies the semantic kind of a preview line.
type PreviewLineKind string

const (
	// PreviewLineKindText is a plain text preview line.
	PreviewLineKindText PreviewLineKind = "text"
	// PreviewLineKindHeader is a structural header line.
	PreviewLineKindHeader PreviewLineKind = "header"
	// PreviewLineKindContext is an unchanged diff context line.
	PreviewLineKindContext PreviewLineKind = "context"
	// PreviewLineKindAdded is an added diff line.
	PreviewLineKindAdded PreviewLineKind = "added"
	// PreviewLineKindRemoved is a removed diff line.
	PreviewLineKindRemoved PreviewLineKind = "removed"
	// PreviewLineKindTruncated indicates content was omitted due to limits.
	PreviewLineKindTruncated PreviewLineKind = "truncated"
)

// PreviewSpan carries one syntax-highlighted token span for a preview line.
type PreviewSpan struct {
	Type chroma.TokenType
	Text string
}

// PreviewLine represents one rendered preview line with optional styling spans.
type PreviewLine struct {
	Kind   PreviewLineKind
	Prefix string
	Spans  []PreviewSpan
}

// PreviewDocument is the structured preview payload rendered by the TUI/output.
type PreviewDocument struct {
	Kind      PreviewFormatKind
	Path      string
	Language  string
	Lines     []PreviewLine
	Truncated bool
	LineLimit int
}

// PreviewSyntax describes the detected syntax highlighting for a preview.
type PreviewSyntax struct {
	Lexer    chroma.Lexer
	Language string
}
