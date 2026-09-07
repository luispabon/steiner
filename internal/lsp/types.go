package lsp

// Location represents a position in a source file, using 1-based line and column.
type Location struct {
	File      string
	Line      int
	Column    int
	EndLine   int
	EndColumn int
}

// Diagnostic represents a diagnostic message from the language server.
type Diagnostic struct {
	File     string
	Line     int
	Column   int
	Severity string // "error", "warning", "information", "hint"
	Source   string
	Message  string
	Code     string
}

// PublishedDiagnostics represents a diagnostics notification from the server.
type PublishedDiagnostics struct {
	File    string
	Version *int32
	Items   []Diagnostic
}

// ProgressEvent represents a $/progress work-done notification.
type ProgressEvent struct {
	Token   string // unique token per progress sequence
	Kind    string // "begin", "report", "end"
	Message string
}
