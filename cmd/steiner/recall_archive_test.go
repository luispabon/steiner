package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/session"
)

// TestNewRecallArchiveNilWithoutSessionStore keeps the archive nil when the
// runtime has no session store, instead of wrapping a nil store in a non-nil
// interface.
func TestNewRecallArchiveNilWithoutSessionStore(t *testing.T) {
	if got := newRecallArchive(nil, func() string { return "sess" }); got != nil {
		t.Fatalf("newRecallArchive(nil) = %#v, want nil", got)
	}
}

// TestRecallArchiveFollowsSession proves the adapter resolves the session on
// every call rather than capturing it when it is built.
func TestRecallArchiveFollowsSession(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	sessionID := "session-one"
	archive := newRecallArchive(store, func() string { return sessionID })
	entry := agent.RecallEntry{Kind: "tool", Tool: "read", CallID: "call-1", Body: "recalled body"}
	name := agent.RecallName(entry)

	firstPath, err := archive.Write(context.Background(), entry)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	wantFirst, err := store.RecallPath("session-one", name)
	if err != nil {
		t.Fatalf("RecallPath(first session) error = %v", err)
	}
	if firstPath != wantFirst {
		t.Errorf("Write() path = %q, want %q", firstPath, wantFirst)
	}
	if got := archive.Path(name); got != wantFirst {
		t.Errorf("Path() = %q, want %q", got, wantFirst)
	}

	sessionID = "session-two"
	wantSecond, err := store.RecallPath("session-two", name)
	if err != nil {
		t.Fatalf("RecallPath(second session) error = %v", err)
	}
	if got := archive.Path(name); got != wantSecond {
		t.Errorf("Path() after session change = %q, want %q", got, wantSecond)
	}
	secondPath, err := archive.Write(context.Background(), entry)
	if err != nil {
		t.Fatalf("Write() after session change error = %v", err)
	}
	if secondPath != wantSecond {
		t.Errorf("Write() after session change = %q, want %q", secondPath, wantSecond)
	}
	for _, path := range []string{firstPath, secondPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("recall file %q missing: %v", path, err)
		}
	}
	if filepath.Dir(firstPath) == filepath.Dir(secondPath) {
		t.Errorf("both writes landed in %q, want separate session directories", filepath.Dir(firstPath))
	}
}

// TestRecallArchivePathInvalidNameReturnsEmpty keeps Path() from naming a file
// Write would reject.
func TestRecallArchivePathInvalidNameReturnsEmpty(t *testing.T) {
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("session.NewStore() error = %v", err)
	}
	archive := newRecallArchive(store, func() string { return "sess" })
	for _, name := range []string{"", "..", "../escape", "a/b"} {
		if got := archive.Path(name); got != "" {
			t.Errorf("Path(%q) = %q, want empty", name, got)
		}
	}
}
