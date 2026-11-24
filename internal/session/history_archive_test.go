package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// archiveRecallNoteText returns the note Note produces when source holds result
// bodies, naming the session recall directory dir.
func archiveRecallNoteText(path, dir string) string {
	return archiveNoteText(path) + " Result bodies may be stored under " + dir + "; an archived line that names a recall file points at the file that holds that body."
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
	want := archiveTestHeader + renderHistory(first, nil) + "\n\n" + renderHistory(second, nil) + "\n\n"
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
	for _, entry := range entries {
		if entry.Name() != indexLockFile {
			t.Errorf("store directory has unexpected entry %q after rejected archive calls", entry.Name())
		}
	}
}

// TestHistoryArchiveNoteKeepsToolOnlySourceAndAppendSkipsMissingRecall proves a
// tool-only source still gets a note before its recall file exists, while Append
// leaves the history file untouched when that file is missing. The note's
// archive path becomes true only when a pointer renders.
func TestHistoryArchiveNoteKeepsToolOnlySourceAndAppendSkipsMissingRecall(t *testing.T) {
	source := []agent.Message{{Role: agent.MessageRoleTool, Content: "large tool output"}}
	if got := renderHistory(source, nil); got != "" {
		t.Fatalf("test source renders %q without recall files, want blank", got)
	}

	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })
	historyPath, err := store.HistoryPath("sess")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	recallDir, err := store.RecallDir("sess")
	if err != nil {
		t.Fatalf("RecallDir failed: %v", err)
	}
	note, err := archive.Note(source)
	if err != nil {
		t.Fatalf("Note failed: %v", err)
	}
	if want := archiveRecallNoteText(historyPath, recallDir); note != want {
		t.Errorf("Note() = %q, want %q", note, want)
	}
	if err := archive.Append(context.Background(), source); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	if _, err := os.Stat(historyPath); !os.IsNotExist(err) {
		t.Errorf("Append wrote the archive file without a recall file: stat err = %v", err)
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
	if want := archiveTestHeader + renderHistory(firstSource, nil) + "\n\n"; firstGot != want {
		t.Errorf("first archive = %q, want %q", firstGot, want)
	}
	secondGot, err := store.ReadHistory("second")
	if err != nil {
		t.Fatalf("ReadHistory(second) failed: %v", err)
	}
	if want := archiveTestHeader + renderHistory(secondSource, nil) + "\n\n"; secondGot != want {
		t.Errorf("second archive = %q, want %q", secondGot, want)
	}
}

// TestHistoryArchiveNoteMentionsRecallDirectory checks that a source with result
// bodies names the session recall directory, while a plain exchange does not.
func TestHistoryArchiveNoteMentionsRecallDirectory(t *testing.T) {
	store := newArchiveTestStore(t)
	archive := NewHistoryArchive(store, func() string { return "sess" })
	historyPath, err := store.HistoryPath("sess")
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	recallDir, err := store.RecallDir("sess")
	if err != nil {
		t.Fatalf("RecallDir failed: %v", err)
	}
	tests := []struct {
		name   string
		source []agent.Message
		want   string
	}{
		{name: "plain exchange", source: archiveSource("hello"), want: archiveNoteText(historyPath)},
		{name: "tool result", source: []agent.Message{{Role: agent.MessageRoleTool, Name: "bash", Content: "output"}}, want: archiveRecallNoteText(historyPath, recallDir)},
		{name: "sub-agent envelope", source: []agent.Message{{Role: agent.MessageRoleUser, Content: subAgentEnvelope()}}, want: archiveRecallNoteText(historyPath, recallDir)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			note, err := archive.Note(tt.source)
			if err != nil {
				t.Fatalf("Note failed: %v", err)
			}
			if note != tt.want {
				t.Errorf("Note() = %q, want %q", note, tt.want)
			}
		})
	}
}

// TestHistoryArchiveRoundTripPointersMatchRecallIdentity runs the real order of
// a compaction: Note before any recall file exists, then the recall writes, then
// Append. The archive must carry one pointer per result body, each naming the
// file agent.RecallName derives from the body compaction archives, and the
// envelope boundaries must stay separate.
func TestHistoryArchiveRoundTripPointersMatchRecallIdentity(t *testing.T) {
	store := newArchiveTestStore(t)
	const id = "sess"
	archive := NewHistoryArchive(store, func() string { return id })

	firstEnvelope := subAgentEnvelope()
	secondEnvelope := agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{
		AgentID:          "a2",
		AgentType:        "explore",
		Status:           "completed",
		ParentCallID:     "s2",
		ObjectivePreview: "Check the helpers",
		TurnCount:        1,
		Body:             "Found one more helper.",
	})
	toolBody := `{"ok":true,"result":{"note":"unique tool body"}}`
	source := []agent.Message{
		{Role: agent.MessageRoleAssistant, Turn: 7, ToolCalls: []agent.ToolCall{
			{ID: "c9", Name: "bash", Arguments: map[string]any{"command": "go test ./..."}},
		}},
		{Role: agent.MessageRoleTool, Turn: 7, ToolCallID: "c9", Name: "bash", Content: toolBody},
		{Role: agent.MessageRoleUser, Turn: 8, Content: firstEnvelope + "\n\n" + secondEnvelope + "\n\nCarry on.", Images: []agent.ImageBlock{{ID: "img-9"}}},
	}

	recallDir, err := store.RecallDir(id)
	if err != nil {
		t.Fatalf("RecallDir failed: %v", err)
	}
	note, err := archive.Note(source)
	if err != nil {
		t.Fatalf("Note failed: %v", err)
	}
	if !strings.Contains(note, recallDir) {
		t.Fatalf("Note() = %q, want the recall directory %q", note, recallDir)
	}
	if _, err := os.Stat(recallDir); !os.IsNotExist(err) {
		t.Fatalf("Note created the recall directory: stat err = %v", err)
	}

	entries := writeSourceRecallFiles(t, store, id, source)
	if len(entries) != 3 {
		t.Fatalf("recall entries = %d, want 3", len(entries))
	}
	if err := archive.Append(context.Background(), source); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	got, err := store.ReadHistory(id)
	if err != nil {
		t.Fatalf("ReadHistory failed: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := agent.RecallName(entry)
		names = append(names, name)
		if !strings.Contains(got, name) {
			t.Errorf("archive does not name recall file %q:\n%s", name, got)
		}
		path, err := store.RecallPath(id, name)
		if err != nil {
			t.Fatalf("RecallPath(%q) failed: %v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("recall file %q missing: %v", name, err)
		}
		if !info.Mode().IsRegular() {
			t.Errorf("recall file %q mode = %v, want a regular file", name, info.Mode())
		}
	}
	if names[0] == names[1] || names[1] == names[2] || names[0] == names[2] {
		t.Errorf("recall names collide: %q", names)
	}

	want := archiveTestHeader +
		"### assistant · turn 7\n\n- **bash** go test ./... ✓\n\n" +
		"[tool result omitted: recall " + names[0] + "]\n\n" +
		"### user · turn 8\n\n" +
		"[sub-agent result omitted: recall " + names[1] + "]\n" +
		"[sub-agent result omitted: recall " + names[2] + "]\n\n" +
		"Carry on.\n\n[image: img-9]\n\n"
	if got != want {
		t.Errorf("archive = %q, want %q", got, want)
	}
	for _, body := range []string{"unique tool body", "Found two more call sites.", "Found one more helper."} {
		if strings.Contains(got, body) {
			t.Errorf("archive carries result body %q:\n%s", body, got)
		}
	}
}

// TestHistoryArchiveAppendTreatsSymlinkedRecallFileAsMissing checks that only a
// regular recall file earns a pointer; a symlink at that path keeps the omitted
// fallback.
func TestHistoryArchiveAppendTreatsSymlinkedRecallFileAsMissing(t *testing.T) {
	store := newArchiveTestStore(t)
	const id = "sess"
	archive := NewHistoryArchive(store, func() string { return id })
	source := []agent.Message{{Role: agent.MessageRoleUser, Turn: 2, Content: subAgentEnvelope()}}
	entries := recallEntriesForSource(t, source)
	if len(entries) != 1 {
		t.Fatalf("recall entries = %d, want 1", len(entries))
	}
	path, err := store.RecallPath(id, agent.RecallName(entries[0]))
	if err != nil {
		t.Fatalf("RecallPath failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	target := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(target, []byte("body"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatalf("Symlink failed: %v", err)
	}

	if err := archive.Append(context.Background(), source); err != nil {
		t.Fatalf("Append failed: %v", err)
	}
	got, err := store.ReadHistory(id)
	if err != nil {
		t.Fatalf("ReadHistory failed: %v", err)
	}
	want := archiveTestHeader + "### user · turn 2\n\n[sub-agent result omitted]\n\n"
	if got != want {
		t.Errorf("archive = %q, want %q", got, want)
	}
}
