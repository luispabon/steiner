package output

import "github.com/alecthomas/chroma/v2"

const defaultPreviewLineLimit = 120

type PreviewFormatKind string

const (
	PreviewFormatKindFile     PreviewFormatKind = "file"
	PreviewFormatKindEditDiff PreviewFormatKind = "edit_diff"
)

type PreviewLineKind string

const (
	PreviewLineKindText      PreviewLineKind = "text"
	PreviewLineKindHeader    PreviewLineKind = "header"
	PreviewLineKindContext   PreviewLineKind = "context"
	PreviewLineKindAdded     PreviewLineKind = "added"
	PreviewLineKindRemoved   PreviewLineKind = "removed"
	PreviewLineKindTruncated PreviewLineKind = "truncated"
)

type PreviewSpan struct {
	Type chroma.TokenType
	Text string
}

type PreviewLine struct {
	Kind   PreviewLineKind
	Prefix string
	Spans  []PreviewSpan
}

type PreviewDocument struct {
	Kind      PreviewFormatKind
	Path      string
	Language  string
	Lines     []PreviewLine
	Truncated bool
	LineLimit int
}

type PreviewSyntax struct {
	Lexer    chroma.Lexer
	Language string
}
