package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"

	"github.com/luispabon/steiner/internal/config"
)

// singleLocation builds a one-location definition answer pointing at uriPath.
func singleLocation(uriPath string) *protocol.Location {
	return &protocol.Location{
		URI: uri.URI(uriPath),
		Range: protocol.Range{
			Start: protocol.Position{Line: 0, Character: 0},
			End:   protocol.Position{Line: 0, Character: 5},
		},
	}
}

// waitForMethod blocks until the fake server has received method.
func waitForMethod(t *testing.T, fs *fakeServer, method string) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for {
		for _, recorded := range fs.recorded() {
			if recorded == method {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", method)
		}
		time.Sleep(time.Millisecond)
	}
}

// countMethod returns how many times the fake server received method.
func countMethod(fs *fakeServer, method string) int {
	count := 0
	for _, recorded := range fs.recorded() {
		if recorded == method {
			count++
		}
	}
	return count
}

// TestDefinitionsReachesServerAfterRelatedFileEdit checks that repeating a
// query after an external edit to a related file reaches the language server
// again instead of serving an answer derived from the pre-edit package state.
func TestDefinitionsReachesServerAfterRelatedFileEdit(t *testing.T) {
	t.Parallel()
	fs := newFakeServer()
	fs.definitionResult = singleLocation("file:///old_target.go")

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Use() { target() }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	related := filepath.Join(tmpdir, "related.go")
	if err := os.WriteFile(related, []byte("package main\n\nfunc target() {}\n"), 0o644); err != nil {
		t.Fatalf("write related file: %v", err)
	}

	m := symbolTestManager(t, sess, tmpdir, testFile)

	first, err := m.Definitions(ctx, testFile, 3, 15)
	if err != nil {
		t.Fatalf("Definitions (before edit): %v", err)
	}
	if len(first.Locations) != 1 || first.Locations[0].File != "/old_target.go" {
		t.Fatalf("Definitions (before edit): got %+v, want the old target", first.Locations)
	}

	// External edit to a related file in the same package. The queried file
	// is untouched, so the server's answer changes with no edit to the file
	// the query names.
	if err := os.WriteFile(related, []byte("package main\n\nfunc target() int { return 0 }\n"), 0o644); err != nil {
		t.Fatalf("write related file: %v", err)
	}
	fs.definitionResult = singleLocation("file:///new_target.go")

	second, err := m.Definitions(ctx, testFile, 3, 15)
	if err != nil {
		t.Fatalf("Definitions (after edit): %v", err)
	}
	if len(second.Locations) != 1 || second.Locations[0].File != "/new_target.go" {
		t.Fatalf("Definitions (after edit): got %+v, want the new target", second.Locations)
	}

	if calls := countMethod(fs, "textDocument/definition"); calls != 2 {
		t.Errorf("textDocument/definition called %d times, want 2 (every query must reach the server)", calls)
	}
}

// TestDefinitionsEditDuringRequestReturnsFreshResult checks the request-time
// edit race: an edit made while a query is in flight must not leave an answer
// behind that a later identical query would serve.
func TestDefinitionsEditDuringRequestReturnsFreshResult(t *testing.T) {
	t.Parallel()
	fs := newFakeServer()
	fs.definitionResult = singleLocation("file:///before.go")
	fs.stallDefinition()

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()

	sess, _, err := startFakeSession(ctx, t, fs, nil)
	if err != nil {
		t.Fatalf("startFakeSession: %v", err)
	}

	tmpdir := t.TempDir()
	testFile := filepath.Join(tmpdir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Use() { target() }\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	m := symbolTestManager(t, sess, tmpdir, testFile)

	type outcome struct {
		result Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := m.Definitions(ctx, testFile, 3, 15)
		done <- outcome{result: res, err: err}
	}()

	// Edit the queried file while its definition request is in flight.
	waitForMethod(t, fs, "textDocument/definition")
	if err := os.WriteFile(testFile, []byte("package main\n\nfunc Use() { newtarget() }\n"), 0o644); err != nil {
		t.Fatalf("write file during request: %v", err)
	}
	fs.releaseHolds()

	first := <-done
	if first.err != nil {
		t.Fatalf("Definitions (in flight): %v", first.err)
	}
	if len(first.result.Locations) != 1 || first.result.Locations[0].File != "/before.go" {
		t.Fatalf("Definitions (in flight): got %+v, want the pre-edit answer", first.result.Locations)
	}

	// The next query for the edited file must reach the server and return its
	// post-edit answer, not the answer produced before the edit.
	fs.definitionResult = singleLocation("file:///after.go")
	second, err := m.Definitions(ctx, testFile, 3, 15)
	if err != nil {
		t.Fatalf("Definitions (after edit): %v", err)
	}
	if len(second.Locations) != 1 || second.Locations[0].File != "/after.go" {
		t.Fatalf("Definitions (after edit): got %+v, want the post-edit answer", second.Locations)
	}

	if calls := countMethod(fs, "textDocument/definition"); calls != 2 {
		t.Errorf("textDocument/definition called %d times, want 2 (every query must reach the server)", calls)
	}
}

// TestDiagnosticsRepeatedCallReachesServer checks that every diagnostics query
// runs its own collect cycle instead of returning an earlier result.
func TestDiagnosticsRepeatedCallReachesServer(t *testing.T) {
	t.Parallel()
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

	cfg := config.LSPConfig{
		MaxResults:        100,
		DiagnosticsWindow: config.MustDuration("1s"),
		Servers: map[string]config.LSPServerConfig{
			"go": {Enabled: true, FileExtensions: []string{".go"}},
		},
	}
	m := NewManager(cfg, tmpdir, nil, ManagerOptions{WarnFn: func(string) {}})
	t.Cleanup(func() { _ = m.Close() })

	root := resolveRoot(testFile, tmpdir, nil)
	m.sessions = map[sessionKey]*entry{{server: "go", root: root}: {state: ServerState{Status: ServerStatusReady}, session: sess}}

	var opens atomic.Int32
	fs.onDidOpen = func(ctx context.Context, params *protocol.DidOpenTextDocumentParams) {
		message := fmt.Sprintf("error %d", opens.Add(1))
		go func() {
			time.Sleep(5 * time.Millisecond)
			fs.notifyDiagnostics(context.WithoutCancel(ctx), t, &protocol.PublishDiagnosticsParams{
				URI: params.TextDocument.URI,
				Diagnostics: []protocol.Diagnostic{{
					Range: protocol.Range{
						Start: protocol.Position{Line: 0, Character: 0},
						End:   protocol.Position{Line: 0, Character: 5},
					},
					Severity: protocol.DiagnosticSeverityError,
					Message:  protocol.String(message),
				}},
			})
		}()
	}

	for call := 1; call <= 2; call++ {
		result, err := m.Diagnostics(ctx, testFile)
		if err != nil {
			t.Fatalf("Diagnostics call %d: %v", call, err)
		}
		want := fmt.Sprintf("error %d", call)
		if len(result.Items) != 1 || result.Items[0].Message != want {
			t.Fatalf("Diagnostics call %d: got %+v, want one item with message %q", call, result.Items, want)
		}
	}

	if opensCount := countMethod(fs, "textDocument/didOpen"); opensCount != 2 {
		t.Errorf("textDocument/didOpen called %d times, want 2 (every query must run a collect cycle)", opensCount)
	}
}
