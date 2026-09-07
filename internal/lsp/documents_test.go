package lsp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.lsp.dev/protocol"
)

func TestLanguageIDFor(t *testing.T) {
	tests := []struct {
		file   string
		expect string
	}{
		{"main.go", "go"},
		{"script.ts", "typescript"},
		{"component.tsx", "typescriptreact"},
		{"app.js", "javascript"},
		{"script.py", "python"},
		{"lib.rs", "rust"},
		{"Main.GO", "go"},      // case-insensitive
		{"unknown.xyz", "xyz"}, // unknown extension
		{"noextension", ""},    // no extension
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			got := languageIDFor(tc.file)
			if got != tc.expect {
				t.Errorf("languageIDFor(%q) = %q, want %q", tc.file, got, tc.expect)
			}
		})
	}
}

func TestWithDocument(t *testing.T) {
	// Set up a fake session.
	fs := newFakeServer()
	fs.definitionResult = &protocol.Location{
		URI: "file:///test.go",
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 5},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	// Create a test file.
	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Test successful open and close.
	fnCalled := false
	err = withDocument(ctx, sess, testFile, func() error {
		fnCalled = true
		// Verify the file was opened by checking recorded methods.
		return nil
	})
	if err != nil {
		t.Fatalf("withDocument: %v", err)
	}
	if !fnCalled {
		t.Error("withDocument function was not called")
	}

	// Wait a bit for DidClose to be processed.
	time.Sleep(50 * time.Millisecond)

	// Verify didOpen and didClose were recorded.
	methods := fs.recorded()
	hasOpen := false
	hasClose := false
	for _, m := range methods {
		if m == "textDocument/didOpen" {
			hasOpen = true
		}
		if m == "textDocument/didClose" {
			hasClose = true
		}
	}
	if !hasOpen {
		t.Error("didOpen was not recorded")
	}
	if !hasClose {
		t.Error("didClose was not recorded")
	}
}

func TestWithDocumentClosesEvenOnError(t *testing.T) {
	fs := newFakeServer()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// withDocument should close even if the function errors.
	fnErr := "test error"
	err = withDocument(ctx, sess, testFile, func() error {
		return ErrTest{message: fnErr}
	})

	if err == nil || err.Error() != fnErr {
		t.Errorf("withDocument error: got %v, want %v", err, fnErr)
	}

	// Wait a bit for DidClose to be processed.
	time.Sleep(50 * time.Millisecond)

	// Verify didClose was still recorded.
	methods := fs.recorded()
	hasClose := false
	for _, m := range methods {
		if m == "textDocument/didClose" {
			hasClose = true
			break
		}
	}
	if !hasClose {
		t.Error("didClose was not recorded despite function error")
	}
}

type ErrTest struct {
	message string
}

func (e ErrTest) Error() string {
	return e.message
}
