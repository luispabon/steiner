package session

import (
	"context"
	"os"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// archiveTestHeader is the header Append writes when an archive is created.
const archiveTestHeader = "# Conversation history\n\nArchived by compaction, oldest first.\n\n"

func archiveSource(text string) []agent.Message {
	return []agent.Message{{Role: agent.MessageRoleUser, Content: text}}
}

func archiveNoteText(path string) string {
	return agent.HistoryNoteMarker + " Conversation before this summary is archived at " + path + " (markdown, oldest first, tool results omitted). If a detail you need is missing from this summary, grep that file and read the matching lines."
}

func newArchiveTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

func TestHistoryArchiveNoteNamesArchiveWithoutWriting(t *testing.T) {
	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })
	path, err := store.HistoryPath("sess")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}

	note, err := archive.Note(archiveSource("hello"))
	if err != nil {
		t.Fatalf("Note failed: %v", err)
	}
	if want := archiveNoteText(path); note != want {
		t.Errorf("Note() = %q, want %q", note, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Note wrote the archive file: stat err = %v", err)
	}
}

func TestHistoryArchiveNoteBlankSourceReturnsEmpty(t *testing.T) {
	tests := []struct {
		name   string
		source []agent.Message
	}{
		{name: "nil", source: nil},
		{name: "empty", source: []agent.Message{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newArchiveTestStore(t)
			archive := NewHistoryArchive(store, func() string { return "sess" })
			note, err := archive.Note(tt.source)
			if err != nil {
				t.Fatalf("Note failed: %v", err)
			}
			if note != "" {
				t.Errorf("Note() = %q, want empty", note)
			}
			path, err := store.HistoryPath("sess")
			if err != nil {
				t.Fatalf("HistoryPath failed: %v", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("Note wrote the archive file: stat err = %v", err)
			}
		})
	}
}

func TestHistoryArchiveAppendWritesHeaderOnceInOrder(t *testing.T) {
	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })
	first := archiveSource("first request")
	second := archiveSource("second request")

	if err := archive.Append(context.Background(), first); err != nil {
		t.Fatalf("Append(first) failed: %v", err)
	}
	if err := archive.Append(context.Background(), second); err != nil {
		t.Fatalf("Append(second) failed: %v", err)
	}

	got, err := store.ReadHistory("sess")
	if err != nil {
		t.Fatalf("ReadHistory failed: %v", err)
	}
	want := archiveTestHeader + renderHistory(first) + "\n\n" + renderHistory(second) + "\n\n"
	if got != want {
		t.Errorf("archive = %q, want %q", got, want)
	}
}

func TestHistoryArchiveAppendBlankSourceLeavesFileUntouched(t *testing.T) {
	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })

	if err := archive.Append(context.Background(), nil); err != nil {
		t.Fatalf("Append(nil) failed: %v", err)
	}
	path, err := store.HistoryPath("sess")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Append(nil) wrote the archive file: stat err = %v", err)
	}
}

func TestHistoryArchiveRejectsEmptySessionID(t *testing.T) {
	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "" })
	const wantErr = "history archive: no session id"

	if _, err := archive.Note(archiveSource("hello")); err == nil || err.Error() != wantErr {
		t.Errorf("Note() error = %v, want %q", err, wantErr)
	}
	if err := archive.Append(context.Background(), archiveSource("hello")); err == nil || err.Error() != wantErr {
		t.Errorf("Append() error = %v, want %q", err, wantErr)
	}
	entries, err := os.ReadDir(store.dir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("store directory has %d entries after rejected archive calls, want 0", len(entries))
	}
}

func TestHistoryArchiveNonEmptySourceRenderingBlank(t *testing.T) {
	// A tool result is non-empty but renderHistory omits its body, so the
	// source renders blank.
	source := []agent.Message{{Role: agent.MessageRoleTool, Content: "large tool output"}}
	if got := renderHistory(source); got != "" {
		t.Fatalf("test source renders %q, want blank", got)
	}

	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })
	note, err := archive.Note(source)
	if err != nil {
		t.Fatalf("Note failed: %v", err)
	}
	if note != "" {
		t.Errorf("Note() = %q, want empty", note)
	}
	if err := archive.Append(context.Background(), source); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	path, err := store.HistoryPath("sess")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Append wrote the archive file for blank rendering: stat err = %v", err)
	}
}

func TestHistoryArchiveFollowsCurrentSessionID(t *testing.T) {
	store := newArchiveTestStore(t)
	current := "first"
	archive := NewHistoryArchive(store, func() string { return current })
	firstSource := archiveSource("for first")
	secondSource := archiveSource("for second")

	if err := archive.Append(context.Background(), firstSource); err != nil {
		t.Fatalf("Append(first) failed: %v", err)
	}

	current = "second"
	secondPath, err := store.HistoryPath("second")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	note, err := archive.Note(secondSource)
	if err != nil {
		t.Fatalf("Note failed: %v", err)
	}
	if want := archiveNoteText(secondPath); note != want {
		t.Errorf("Note() after switch = %q, want %q", note, want)
	}
	if err := archive.Append(context.Background(), secondSource); err != nil {
		t.Fatalf("Append(second) failed: %v", err)
	}

	firstGot, err := store.ReadHistory("first")
	if err != nil {
		t.Fatalf("ReadHistory(first) failed: %v", err)
	}
	if want := archiveTestHeader + renderHistory(firstSource) + "\n\n"; firstGot != want {
		t.Errorf("first archive = %q, want %q", firstGot, want)
	}
	secondGot, err := store.ReadHistory("second")
	if err != nil {
		t.Fatalf("ReadHistory(second) failed: %v", err)
	}
	if want := archiveTestHeader + renderHistory(secondSource) + "\n\n"; secondGot != want {
		t.Errorf("second archive = %q, want %q", secondGot, want)
	}
}
