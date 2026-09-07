package lsp

import (
	"context"
	"errors"
)

// session is one live connection to a language server process.
type session interface {
	Definition(ctx context.Context, file string, line, col int) ([]Location, error)
	References(ctx context.Context, file string, line, col int, includeDecl bool) ([]Location, error)
	DidOpen(ctx context.Context, file, languageID, text string, version int32) error
	DidClose(ctx context.Context, file string) error
	Diagnostics() <-chan PublishedDiagnostics
	Progress() <-chan ProgressEvent
	Exited() <-chan struct{}
	Close(ctx context.Context) error
}

// errServerExited is returned when the server exits mid-request.
var errServerExited = errors.New("language server exited")
