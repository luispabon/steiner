package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// languageIDMap maps file extensions to LSP language IDs.
var languageIDMap = map[string]string{
	".go":  "go",
	".ts":  "typescript",
	".tsx": "typescriptreact",
	".js":  "javascript",
	".py":  "python",
	".rs":  "rust",
}

// languageIDFor derives an LSP language ID from a file path.
// It uses the languageIDMap if the extension is known, otherwise strips the dot
// from the extension.
func languageIDFor(file string) string {
	ext := strings.ToLower(filepath.Ext(file))
	if lid, ok := languageIDMap[ext]; ok {
		return lid
	}
	// Fall back to the extension without the dot.
	if len(ext) > 1 {
		return ext[1:]
	}
	return ext
}

// withDocument opens a document, invokes fn, and closes it.
// The file is read from disk, opened with the server at version 1, and closed
// after fn returns (or errors). DidClose is best-effort and its error is ignored.
func withDocument(ctx context.Context, s session, file string, fn func() error) error {
	textBytes, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read document: %w", err)
	}

	// DidOpen
	lid := languageIDFor(file)
	if err := s.DidOpen(ctx, file, lid, string(textBytes), 1); err != nil {
		return fmt.Errorf("did open: %w", err)
	}

	// Invoke the function.
	fnErr := fn()

	// DidClose is best-effort; close is attempted even if fn errored.
	// Use a fresh context so this isn't constrained by the request timeout.
	closeCtx := context.WithoutCancel(ctx)
	_ = s.DidClose(closeCtx, file) // best-effort, error intentionally ignored

	if fnErr != nil {
		return fnErr
	}
	return nil
}
