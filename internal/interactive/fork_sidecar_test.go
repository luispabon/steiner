package interactive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// forkTestMarker renders the elision marker compaction writes for an elided
// result body.
func forkTestMarker(name, path string) string {
	return "[Tool output omitted from active context: recall " + name + ", read " + path + "; for long lines use bash head -c/tail -c]"
}

// forkTestSidecars writes a history archive and a recall file for id and
// returns a lineage naming both in a summary note and an elision marker.
func forkTestSidecars(t *testing.T, store *session.Store, id string) (agent.ConversationLineage, agent.RecallEntry, string, string) {
	t.Helper()
	entry := agent.RecallEntry{Kind: "tool", Tool: "bash", CallID: "call-1", Body: strings.Repeat("sidecar body line\n", 8)}
	historyPath, err := store.AppendHistory(id, "# Conversation history\n\n", "archived turn\n\n")
	if err != nil {
		t.Fatalf("AppendHistory failed: %v", err)
	}
	recallPath, err := store.WriteRecall(context.Background(), id, entry)
	if err != nil {
		t.Fatalf("WriteRecall failed: %v", err)
	}
	note, err := session.NewHistoryArchive(store, func() string { return id }).Note([]agent.Message{{
		Role: agent.MessageRoleTool, Name: entry.Tool, ToolCallID: entry.CallID, Content: entry.Body,
	}})
	if err != nil {
		t.Fatalf("render history note: %v", err)
	}
	lineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:            1,
			SummaryPrefix: []agent.Message{{Role: agent.MessageRoleSummary, Content: "Earlier work.\n\n" + note}},
			Messages: []agent.Message{
				{Role: agent.MessageRoleTool, Name: entry.Tool, ToolCallID: entry.CallID, Content: forkTestMarker(agent.RecallName(entry), recallPath)},
			},
		}},
		NextGenerationID: 2,
	}
	return lineage, entry, historyPath, recallPath
}

func newForkTestStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

func assertForkFileCopy(t *testing.T, gotPath, wantPath string) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("read fork file %s: %v", gotPath, err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read parent file %s: %v", wantPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("fork file %s = %q, want a copy of %s", gotPath, got, wantPath)
	}
}

// assertForkConversationNames checks that conversation names want in a summary
// note or message and never names unwanted.
func assertForkConversationNames(t *testing.T, conversation []agent.Message, want, unwanted string) {
	t.Helper()
	var joined strings.Builder
	for _, msg := range conversation {
		joined.WriteString(msg.Content)
		joined.WriteString("\n")
	}
	if !strings.Contains(joined.String(), want) {
		t.Errorf("fork conversation does not name %q: %q", want, joined.String())
	}
	if unwanted != "" && strings.Contains(joined.String(), unwanted) {
		t.Errorf("fork conversation still names %q: %q", unwanted, joined.String())
	}
}

func TestHandleForkSessionCopiesSidecars(t *testing.T) {
	store := newForkTestStore(t)
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})
	parentID := s.SessionID()
	lineage, entry, parentHistory, parentRecall := forkTestSidecars(t, store, parentID)
	s.mu.Lock()
	s.sessionTitle = "Parent Session"
	s.lineage = lineage
	s.mu.Unlock()

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v, want nil", err)
	}
	forkedID := s.SessionID()
	if forkedID == parentID {
		t.Fatal("fork kept the parent session id")
	}
	forkHistory, err := store.HistoryPath(forkedID)
	if err != nil {
		t.Fatalf("fork HistoryPath failed: %v", err)
	}
	forkRecall, err := store.RecallPath(forkedID, agent.RecallName(entry))
	if err != nil {
		t.Fatalf("fork RecallPath failed: %v", err)
	}
	assertForkFileCopy(t, forkHistory, parentHistory)
	assertForkFileCopy(t, forkRecall, parentRecall)
	assertForkConversationNames(t, s.Conversation(), forkHistory, parentHistory)
	assertForkConversationNames(t, s.Conversation(), forkRecall, parentRecall)

	if err := store.Delete(parentID); err != nil {
		t.Fatalf("Delete parent failed: %v", err)
	}
	for _, path := range []string{forkHistory, forkRecall} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("fork path %s does not resolve after the parent is deleted: %v", path, err)
		}
	}
}

func TestHandleForkSavedSessionCopiesSidecars(t *testing.T) {
	store := newForkTestStore(t)
	parentID := "saved-parent"
	lineage, entry, parentHistory, parentRecall := forkTestSidecars(t, store, parentID)
	now := time.Now().UTC()
	parent := session.Session{ID: parentID, Title: "Saved Parent", Model: "test-model", CreatedAt: now, UpdatedAt: now, Lineage: lineage}
	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: guardTestConfig()})

	if err := s.handleForkSavedSession(context.Background(), parentID); err != nil {
		t.Fatalf("handleForkSavedSession() = %v, want nil", err)
	}
	forkedID := s.SessionID()
	if forkedID == parentID {
		t.Fatal("fork kept the parent session id")
	}
	forkHistory, err := store.HistoryPath(forkedID)
	if err != nil {
		t.Fatalf("fork HistoryPath failed: %v", err)
	}
	forkRecall, err := store.RecallPath(forkedID, agent.RecallName(entry))
	if err != nil {
		t.Fatalf("fork RecallPath failed: %v", err)
	}
	assertForkFileCopy(t, forkHistory, parentHistory)
	assertForkFileCopy(t, forkRecall, parentRecall)
	assertForkConversationNames(t, s.Conversation(), forkHistory, parentHistory)
	assertForkConversationNames(t, s.Conversation(), forkRecall, parentRecall)
}

func TestHandleForkSessionSidecarCopyFailureWarnsAndKeepsParentPaths(t *testing.T) {
	store := newForkTestStore(t)
	var events []output.Event
	s := testNewSession(t, Dependencies{
		SessionStore: store,
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		Config:       guardTestConfig(),
	})
	parentID := s.SessionID()
	lineage, _, parentHistory, parentRecall := forkTestSidecars(t, store, parentID)
	s.mu.Lock()
	s.sessionTitle = "Parent Session"
	s.lineage = lineage
	s.mu.Unlock()

	// A symlinked recall file fails the sidecar copy after the history copy.
	recallDir, err := store.RecallDir(parentID)
	if err != nil {
		t.Fatalf("RecallDir failed: %v", err)
	}
	entries, err := os.ReadDir(recallDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("read recall dir = %v, %v; want one recall file", entries, err)
	}
	recallFile := filepath.Join(recallDir, entries[0].Name())
	if err := os.Remove(recallFile); err != nil {
		t.Fatalf("remove recall file: %v", err)
	}
	target := filepath.Join(t.TempDir(), "elsewhere.txt")
	if err := os.WriteFile(target, []byte("body"), 0o600); err != nil {
		t.Fatalf("seed symlink target: %v", err)
	}
	if err := os.Symlink(target, recallFile); err != nil {
		t.Fatalf("symlink recall file: %v", err)
	}

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v, want nil on a sidecar copy failure", err)
	}
	forkedID := s.SessionID()
	if forkedID == parentID {
		t.Fatal("fork kept the parent session id")
	}
	if forkHistory, err := store.HistoryPath(forkedID); err != nil {
		t.Fatalf("fork HistoryPath failed: %v", err)
	} else if _, err := os.Stat(forkHistory); err != nil {
		t.Errorf("fork history was not copied: %v", err)
	}
	assertForkConversationNames(t, s.Conversation(), parentHistory, "")
	assertForkConversationNames(t, s.Conversation(), parentRecall, "")

	found := false
	for _, event := range events {
		health, ok := event.Payload.(output.ContextSessionHealthEvent)
		if !ok || health.Severity != "warning" {
			continue
		}
		for _, note := range health.Notes {
			if strings.Contains(note, "copy fork sidecars") {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no session_health warning for the failed sidecar copy: %+v", events)
	}
}
