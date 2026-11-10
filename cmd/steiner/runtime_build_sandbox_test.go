package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/output"
)

func TestSandboxTmpWarningHandlerEmitsSessionHealthWarning(t *testing.T) {
	var events []output.Event
	handler := sandboxTmpWarningHandler(output.SinkFunc(func(event output.Event) {
		events = append(events, event)
	}))

	handler("sandbox /tmp was lost")

	if len(events) != 1 {
		t.Fatalf("events = %#v, want one session_health diagnostic", events)
	}
	if events[0].Type != output.EventTypeContextDiagnostics {
		t.Errorf("event type = %q, want %q", events[0].Type, output.EventTypeContextDiagnostics)
	}
	payload, ok := events[0].Payload.(output.ContextSessionHealthEvent)
	if !ok {
		t.Fatalf("payload = %T, want ContextSessionHealthEvent", events[0].Payload)
	}
	if got := output.ContextDiagnosticKind(payload); got != "session_health" {
		t.Errorf("kind = %q, want session_health", got)
	}
	if payload.Scope != output.SessionHealthScopeSandboxTmp {
		t.Errorf("scope = %q, want %q", payload.Scope, output.SessionHealthScopeSandboxTmp)
	}
	if got := output.ContextDiagnosticSeverity(payload); got != "warning" {
		t.Errorf("severity = %q, want warning", got)
	}
	if len(payload.Notes) != 1 || payload.Notes[0] != "sandbox /tmp was lost" {
		t.Errorf("notes = %#v, want the handler message", payload.Notes)
	}
}

func TestCreateSandboxTmpDirRetriesCollision(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	// The collided ID belongs to another session: its contents must survive.
	existing := filepath.Join(parent, "0101010101010101")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	marker := filepath.Join(existing, "marker")
	if err := os.WriteFile(marker, []byte("live"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	restore := stubSandboxTmpRand(t, [][]byte{bytes.Repeat([]byte{0x01}, 8), bytes.Repeat([]byte{0x02}, 8)})
	defer restore()

	got, err := createSandboxTmpDir(parent)
	if err != nil {
		t.Fatalf("createSandboxTmpDir: %v", err)
	}
	want := filepath.Join(parent, "0202020202020202")
	if got != want {
		t.Fatalf("createSandboxTmpDir = %q, want %q", got, want)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected the collided directory to survive: %v", err)
	}
}

func TestCreateSandboxTmpDirGivesUpAfterCollisions(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.Mkdir(filepath.Join(parent, "0303030303030303"), 0o755); err != nil {
		t.Fatalf("mkdir colliding dir: %v", err)
	}

	restore := stubSandboxTmpRand(t, [][]byte{bytes.Repeat([]byte{0x03}, 8)})
	defer restore()

	if _, err := createSandboxTmpDir(parent); err == nil {
		t.Fatal("expected an error once every attempt collided")
	}
}

func TestCreateSandboxTmpDirRejectsSymlinkedGeneratedID(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, ".steiner", "tmp", "sandbox-tmp")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	target := t.TempDir()
	linked := filepath.Join(parent, "0404040404040404")
	if err := os.Symlink(target, linked); err != nil {
		t.Fatalf("symlink generated id: %v", err)
	}

	restore := stubSandboxTmpRand(t, [][]byte{bytes.Repeat([]byte{0x04}, 8)})
	defer restore()

	_, err := createSandboxTmpDir(parent)
	if err == nil {
		t.Fatal("expected a hard error for a symlink at the generated id")
	}
	if !strings.Contains(err.Error(), "is not a real directory") {
		t.Fatalf("error = %v, want unsafe sandbox tmp dir", err)
	}
	// The symlink is neither followed nor removed.
	info, statErr := os.Lstat(linked)
	if statErr != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected the symlink to survive, info=%v err=%v", info, statErr)
	}
}

// stubSandboxTmpRand replaces the ID generator with a sequence of fixed IDs,
// repeating the last one once the sequence is exhausted.
func stubSandboxTmpRand(t *testing.T, ids [][]byte) func() {
	t.Helper()
	prev := sandboxTmpRandRead
	calls := 0
	sandboxTmpRandRead = func(b []byte) (int, error) {
		idx := calls
		if idx >= len(ids) {
			idx = len(ids) - 1
		}
		calls++
		return copy(b, ids[idx]), nil
	}
	return func() { sandboxTmpRandRead = prev }
}
