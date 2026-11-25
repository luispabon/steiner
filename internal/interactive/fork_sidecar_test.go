package interactive

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
)

// errForkTestSave is the error the fake store returns for a fork save.
var errForkTestSave = errors.New("simulated save failure")

// failingForkStore copies fork sidecars through the real store while Save fails
// for every session except allowID, and records what Save observed.
type failingForkStore struct {
	*session.Store
	mu            sync.Mutex
	allowID       string
	failAfterSave bool
	savedIDs      []string
	forkDirAtSave bool
	discards      []string
	discardErr    error
}

func (s *failingForkStore) Save(ses session.Session) error {
	s.mu.Lock()
	s.savedIDs = append(s.savedIDs, ses.ID)
	allow := ses.ID == s.allowID
	// A session saved through the store and then reported as failed simulates a
	// store update that fails after indexing, such as an eviction failure.
	failAfterSave := s.failAfterSave && !allow
	s.mu.Unlock()
	if history, err := s.HistoryPath(ses.ID); err == nil {
		if info, err := os.Stat(filepath.Dir(history)); err == nil && info.IsDir() {
			s.mu.Lock()
			s.forkDirAtSave = true
			s.mu.Unlock()
		}
	}
	if allow || failAfterSave {
		if err := s.Store.Save(ses); err != nil {
			return err
		}
		if failAfterSave {
			return errForkTestSave
		}
		return nil
	}
	return errForkTestSave
}

func (s *failingForkStore) DiscardForkSidecars(forkID string) error {
	s.mu.Lock()
	s.discards = append(s.discards, forkID)
	err := s.discardErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Store.DiscardForkSidecars(forkID)
}

func (s *failingForkStore) forkSnapshot(t *testing.T) (string, bool, []string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.savedIDs) == 0 {
		t.Fatal("no Save calls recorded")
	}
	return s.savedIDs[len(s.savedIDs)-1], s.forkDirAtSave, append([]string(nil), s.discards...)
}

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

// forkTestReports joins the content of every context report event.
func forkTestReports(events []output.Event) string {
	var joined strings.Builder
	for _, event := range events {
		report, ok := event.Payload.(output.ContextReportEvent)
		if !ok {
			continue
		}
		joined.WriteString(report.Content)
		joined.WriteString("\n")
	}
	return joined.String()
}

func TestHandleForkSavedSessionSaveFailureRemovesForkSidecars(t *testing.T) {
	tests := []struct {
		name        string
		discardErr  error
		wantDirGone bool
	}{
		{name: "removes the copied sidecars", wantDirGone: true},
		{name: "keeps the directory when the discard fails", discardErr: errors.New("simulated discard failure")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := newForkTestStore(t)
			parentID := "saved-parent"
			lineage, _, _, _ := forkTestSidecars(t, base, parentID)
			now := time.Now().UTC()
			parent := session.Session{ID: parentID, Title: "Saved Parent", Model: "test-model", CreatedAt: now, UpdatedAt: now, Lineage: lineage}
			if err := base.Save(parent); err != nil {
				t.Fatalf("save parent: %v", err)
			}
			store := &failingForkStore{Store: base, discardErr: tt.discardErr}
			var events []output.Event
			s := testNewSession(t, Dependencies{
				SessionStore: store,
				BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
				Config:       guardTestConfig(),
			})
			originalID := s.SessionID()

			err := s.handleForkSavedSession(context.Background(), parentID)
			if !errors.Is(err, errForkTestSave) {
				t.Fatalf("handleForkSavedSession() = %v, want the Save error", err)
			}
			if got := s.SessionID(); got != originalID {
				t.Errorf("session switched to %q after a failed save, want %q", got, originalID)
			}
			forkedID, forkDirAtSave, discards := store.forkSnapshot(t)
			if forkedID == parentID {
				t.Fatal("the fork save never ran")
			}
			if !forkDirAtSave {
				t.Fatal("the fork had no sidecar directory before Save, so nothing was copied")
			}
			if len(discards) != 1 || discards[0] != forkedID {
				t.Errorf("discarded %v, want [%s]", discards, forkedID)
			}
			history, err := store.HistoryPath(forkedID)
			if err != nil {
				t.Fatalf("fork HistoryPath failed: %v", err)
			}
			_, statErr := os.Stat(filepath.Dir(history))
			if tt.wantDirGone && !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("fork session directory survived a failed save: %v", statErr)
			}
			if !tt.wantDirGone && statErr != nil {
				t.Errorf("fork session directory removed although the discard failed: %v", statErr)
			}

			reports := forkTestReports(events)
			if !strings.Contains(reports, errForkTestSave.Error()) {
				t.Errorf("reports do not name the save failure: %q", reports)
			}
			if tt.discardErr != nil {
				if !strings.Contains(reports, tt.discardErr.Error()) {
					t.Errorf("reports do not name the discard failure: %q", reports)
				}
			} else if strings.Contains(reports, "discard fork sidecars") {
				t.Errorf("reports name a discard failure that did not happen: %q", reports)
			}
		})
	}
}

// assertForkKeptAfterIndexedSaveFailure checks that a handler kept a fork whose
// Save failed after indexing it: the session switched to the fork, the fork is
// still loadable with its sidecars, and a session-health warning names the save
// failure.
func assertForkKeptAfterIndexedSaveFailure(t *testing.T, store *failingForkStore, events []output.Event, s *Session, parentID string) {
	t.Helper()
	forkedID := s.SessionID()
	if forkedID == parentID {
		t.Fatalf("session kept the parent id %q after the fork save", parentID)
	}
	if _, err := store.Load(forkedID); err != nil {
		t.Errorf("fork is not loadable after the failed store update: %v", err)
	}
	history, err := store.HistoryPath(forkedID)
	if err != nil {
		t.Fatalf("fork HistoryPath failed: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(history)); err != nil {
		t.Errorf("fork session directory removed although the fork was saved: %v", err)
	}

	found := false
	for _, event := range events {
		health, ok := event.Payload.(output.ContextSessionHealthEvent)
		if !ok || health.Severity != "warning" {
			continue
		}
		for _, note := range health.Notes {
			if strings.Contains(note, errForkTestSave.Error()) {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no session_health warning naming the save failure: %+v", events)
	}
	if reports := forkTestReports(events); strings.Contains(reports, "save fork failed") || strings.Contains(reports, "save failed") {
		t.Errorf("reports still report the fork as failed: %q", reports)
	}
}

func TestHandleForkSessionKeepsForkWhenSaveFailsAfterIndexing(t *testing.T) {
	store := &failingForkStore{Store: newForkTestStore(t), failAfterSave: true}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		SessionStore: store,
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		Config:       guardTestConfig(),
	})
	parentID := s.SessionID()
	store.mu.Lock()
	store.allowID = parentID
	store.mu.Unlock()
	lineage, _, _, _ := forkTestSidecars(t, store.Store, parentID)
	s.mu.Lock()
	s.sessionTitle = "Parent Session"
	s.lineage = lineage
	s.mu.Unlock()

	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v, want nil for an indexed fork", err)
	}
	assertForkKeptAfterIndexedSaveFailure(t, store, events, s, parentID)
}

func TestHandleForkSavedSessionKeepsForkWhenSaveFailsAfterIndexing(t *testing.T) {
	base := newForkTestStore(t)
	parentID := "saved-parent"
	lineage, _, _, _ := forkTestSidecars(t, base, parentID)
	now := time.Now().UTC()
	parent := session.Session{ID: parentID, Title: "Saved Parent", Model: "test-model", CreatedAt: now, UpdatedAt: now, Lineage: lineage}
	if err := base.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}
	store := &failingForkStore{Store: base, failAfterSave: true}
	var events []output.Event
	s := testNewSession(t, Dependencies{
		SessionStore: store,
		BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
		Config:       guardTestConfig(),
	})
	originalID := s.SessionID()

	if err := s.handleForkSavedSession(context.Background(), parentID); err != nil {
		t.Fatalf("handleForkSavedSession() = %v, want nil for an indexed fork", err)
	}
	if s.SessionID() == originalID {
		t.Fatalf("session kept the pre-fork id %q", originalID)
	}
	assertForkKeptAfterIndexedSaveFailure(t, store, events, s, parentID)
}

func TestHandleForkSessionSaveFailureRemovesForkSidecars(t *testing.T) {
	tests := []struct {
		name        string
		discardErr  error
		wantDirGone bool
	}{
		{name: "removes the copied sidecars", wantDirGone: true},
		{name: "keeps the directory when the discard fails", discardErr: errors.New("simulated discard failure")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &failingForkStore{Store: newForkTestStore(t), discardErr: tt.discardErr}
			var events []output.Event
			s := testNewSession(t, Dependencies{
				SessionStore: store,
				BaseEvents:   output.SinkFunc(func(event output.Event) { events = append(events, event) }),
				Config:       guardTestConfig(),
			})
			parentID := s.SessionID()
			store.mu.Lock()
			store.allowID = parentID
			store.mu.Unlock()
			lineage, _, _, _ := forkTestSidecars(t, store.Store, parentID)
			s.mu.Lock()
			s.sessionTitle = "Parent Session"
			s.lineage = lineage
			s.mu.Unlock()

			err := s.handleForkSession(context.Background())
			if !errors.Is(err, errForkTestSave) {
				t.Fatalf("handleForkSession() = %v, want the Save error", err)
			}
			if got := s.SessionID(); got != parentID {
				t.Errorf("session switched to %q after a failed save, want %q", got, parentID)
			}
			forkedID, forkDirAtSave, discards := store.forkSnapshot(t)
			if forkedID == parentID {
				t.Fatal("the fork save never ran")
			}
			if !forkDirAtSave {
				t.Fatal("the fork had no sidecar directory before Save, so nothing was copied")
			}
			if len(discards) != 1 || discards[0] != forkedID {
				t.Errorf("discarded %v, want [%s]", discards, forkedID)
			}
			history, err := store.HistoryPath(forkedID)
			if err != nil {
				t.Fatalf("fork HistoryPath failed: %v", err)
			}
			_, statErr := os.Stat(filepath.Dir(history))
			if tt.wantDirGone && !errors.Is(statErr, fs.ErrNotExist) {
				t.Errorf("fork session directory survived a failed save: %v", statErr)
			}
			if !tt.wantDirGone && statErr != nil {
				t.Errorf("fork session directory removed although the discard failed: %v", statErr)
			}

			reports := forkTestReports(events)
			if !strings.Contains(reports, errForkTestSave.Error()) {
				t.Errorf("reports do not name the save failure: %q", reports)
			}
			if tt.discardErr != nil {
				if !strings.Contains(reports, tt.discardErr.Error()) {
					t.Errorf("reports do not name the discard failure: %q", reports)
				}
			} else if strings.Contains(reports, "discard fork sidecars") {
				t.Errorf("reports name a discard failure that did not happen: %q", reports)
			}
		})
	}
}
