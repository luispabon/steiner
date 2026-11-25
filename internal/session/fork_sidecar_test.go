package session

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/prompt"
)

// forkMarker renders the elision marker compaction writes for an elided result
// body. The literal format is shared with internal/agent and deliberately
// duplicated here so these tests do not depend on the compaction package.
func forkMarker(name, path string) string {
	return "[Tool output omitted from active context: recall " + name + ", read " + path + "; for long lines use bash head -c/tail -c]"
}

// forkSidecarParent saves a parent session whose summary carries its archive
// note, whose tool message carries its elision marker, and whose ordinary user
// text repeats the parent paths that must survive the fork untouched.
func forkSidecarParent(t *testing.T, store *Store) (Session, agent.RecallEntry, string, string) {
	t.Helper()
	entry := agent.RecallEntry{Kind: "tool", Tool: "bash", CallID: "call-1", Body: strings.Repeat("archived body line\n", 8)}
	parent := Session{
		ID:        "parent-session",
		Title:     "Parent",
		Model:     "model",
		CreatedAt: time.Unix(1000, 0).UTC(),
		UpdatedAt: time.Unix(1000, 0).UTC(),
	}
	if err := store.Save(parent); err != nil {
		t.Fatalf("save parent: %v", err)
	}
	parentHistory, err := store.AppendHistory(parent.ID, historyHeader, "archived turn\n\n")
	if err != nil {
		t.Fatalf("append parent history: %v", err)
	}
	parentRecall, err := store.WriteRecall(context.Background(), parent.ID, entry)
	if err != nil {
		t.Fatalf("write parent recall: %v", err)
	}
	note, err := NewHistoryArchive(store, func() string { return parent.ID }).Note([]agent.Message{{
		Role: agent.MessageRoleTool, Name: entry.Tool, ToolCallID: entry.CallID, Content: entry.Body,
	}})
	if err != nil {
		t.Fatalf("render history note: %v", err)
	}
	name := agent.RecallName(entry)
	envelope := agent.RenderSubAgentResultEnvelope(agent.SubAgentCompletion{
		ParentCallID: "call-2",
		AgentID:      "agent-1",
		AgentType:    "code",
		Status:       "done",
		Body:         forkMarker(name, parentRecall),
	})
	parent.Lineage = agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:            1,
			SummaryPrefix: []agent.Message{{Role: agent.MessageRoleSummary, Content: "Earlier work summary.\n\n" + note}},
			Messages: []agent.Message{
				{Role: agent.MessageRoleTool, Name: entry.Tool, ToolCallID: entry.CallID, Content: forkMarker(name, parentRecall)},
				{Role: agent.MessageRoleUser, Content: "keep " + parentHistory + " and " + parentRecall + " as typed"},
				{Role: agent.MessageRoleUser, Source: agent.MessageSourceSubAgentResult, Content: envelope},
			},
		}},
		NextGenerationID: 2,
	}
	return parent, entry, parentHistory, parentRecall
}

func assertFileBytes(t *testing.T, gotPath, wantPath string) {
	t.Helper()
	got, err := os.ReadFile(gotPath)
	if err != nil {
		t.Fatalf("read %s: %v", gotPath, err)
	}
	want, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("read %s: %v", wantPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s content = %q, want a copy of %s", gotPath, got, wantPath)
	}
}

func assertFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode != want {
		t.Errorf("%s mode = %o, want %o", path, mode, want)
	}
}

func TestForkSidecarsCopiesAndRewritesInheritedReferences(t *testing.T) {
	store := newRecallTestStore(t)
	parent, entry, parentHistory, parentRecall := forkSidecarParent(t, store)
	forked, err := Fork(parent)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	forkHistory, err := store.HistoryPath(forked.ID)
	if err != nil {
		t.Fatalf("fork HistoryPath failed: %v", err)
	}
	forkRecall, err := store.RecallPath(forked.ID, agent.RecallName(entry))
	if err != nil {
		t.Fatalf("fork RecallPath failed: %v", err)
	}

	got, err := store.ForkSidecars(parent.ID, forked)
	if err != nil {
		t.Fatalf("ForkSidecars failed: %v", err)
	}
	if got.ID != forked.ID {
		t.Errorf("fork id = %q, want %q", got.ID, forked.ID)
	}
	if got.PromptCacheKey != parent.CacheKey() {
		t.Errorf("fork prompt cache key = %q, want the parent's %q", got.PromptCacheKey, parent.CacheKey())
	}

	assertFileBytes(t, forkHistory, parentHistory)
	assertFileBytes(t, forkRecall, parentRecall)
	assertFileMode(t, forkHistory, 0o600)
	assertFileMode(t, forkRecall, 0o600)
	assertFileMode(t, filepath.Dir(forkHistory), 0o700)
	assertFileMode(t, filepath.Dir(forkRecall), 0o700)

	latest := got.Lineage.Generations[len(got.Lineage.Generations)-1]
	summary := latest.SummaryPrefix[0].Content
	if !strings.Contains(summary, forkHistory) {
		t.Errorf("summary note does not name the fork history %q: %q", forkHistory, summary)
	}
	if strings.Contains(summary, parentHistory) {
		t.Errorf("summary note still names the parent history %q: %q", parentHistory, summary)
	}

	if want := forkMarker(agent.RecallName(entry), forkRecall); latest.Messages[0].Content != want {
		t.Errorf("tool marker = %q, want %q", latest.Messages[0].Content, want)
	}
	if want := parent.Lineage.Generations[0].Messages[1].Content; latest.Messages[1].Content != want {
		t.Errorf("user text = %q, want it unchanged as %q", latest.Messages[1].Content, want)
	}
	resultContent := latest.Messages[2].Content
	parsed, ok := agent.ParseSubAgentResultEnvelope(resultContent)
	if !ok {
		t.Fatalf("rewritten sub-agent envelope is not parseable: %q", resultContent)
	}
	parentResult, ok := agent.ParseSubAgentResultEnvelope(parent.Lineage.Generations[0].Messages[2].Content)
	if !ok {
		t.Fatalf("parent sub-agent envelope is not parseable: %q", parent.Lineage.Generations[0].Messages[2].Content)
	}
	if want := strings.ReplaceAll(parentResult.Inner, parentRecall, forkRecall); parsed.Inner != want {
		t.Errorf("envelope inner = %q, want %q", parsed.Inner, want)
	}
	if strings.Contains(resultContent, parentRecall) {
		t.Errorf("rewritten envelope still names the parent recall path %q", parentRecall)
	}
	if spans := prompt.ResultEnvelopeSpans(resultContent); len(spans) != 1 || spans[0].Start != 0 || spans[0].End != len(resultContent) {
		t.Errorf("envelope spans = %+v, want one span covering the message", spans)
	}

	if err := store.Save(got); err != nil {
		t.Fatalf("save fork: %v", err)
	}
	loaded, err := store.Load(got.ID)
	if err != nil {
		t.Fatalf("load fork: %v", err)
	}
	loadedLatest := loaded.Lineage.Generations[len(loaded.Lineage.Generations)-1]
	if !strings.Contains(loadedLatest.SummaryPrefix[0].Content, forkHistory) {
		t.Errorf("reloaded summary note lost the fork history path: %q", loadedLatest.SummaryPrefix[0].Content)
	}
	if !strings.Contains(loadedLatest.Messages[0].Content, forkRecall) {
		t.Errorf("reloaded tool marker lost the fork recall path: %q", loadedLatest.Messages[0].Content)
	}

	if err := store.Delete(parent.ID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	for _, path := range []string{forkHistory, forkRecall} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("fork path %s does not resolve after the parent is deleted: %v", path, err)
		}
	}
}

func TestForkSidecarsCopyFailureLeavesReferencesUnchanged(t *testing.T) {
	tests := []struct {
		name        string
		breakParent func(t *testing.T, store *Store, parent Session, entry agent.RecallEntry)
	}{
		{
			name: "history is a directory",
			breakParent: func(t *testing.T, store *Store, parent Session, _ agent.RecallEntry) {
				t.Helper()
				path, err := store.HistoryPath(parent.ID)
				if err != nil {
					t.Fatalf("HistoryPath failed: %v", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove history: %v", err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatalf("replace history with a directory: %v", err)
				}
			},
		},
		{
			name: "recall file is a symlink",
			breakParent: func(t *testing.T, store *Store, parent Session, entry agent.RecallEntry) {
				t.Helper()
				path, err := store.RecallPath(parent.ID, agent.RecallName(entry))
				if err != nil {
					t.Fatalf("RecallPath failed: %v", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove recall file: %v", err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere.txt"), path); err != nil {
					t.Fatalf("symlink recall file: %v", err)
				}
			},
		},
		{
			name: "recall directory is a symlink",
			breakParent: func(t *testing.T, store *Store, parent Session, _ agent.RecallEntry) {
				t.Helper()
				dir, err := store.RecallDir(parent.ID)
				if err != nil {
					t.Fatalf("RecallDir failed: %v", err)
				}
				aside := dir + ".aside"
				if err := os.Rename(dir, aside); err != nil {
					t.Fatalf("move recall directory: %v", err)
				}
				if err := os.Symlink(aside, dir); err != nil {
					t.Fatalf("symlink recall directory: %v", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newRecallTestStore(t)
			parent, entry, _, _ := forkSidecarParent(t, store)
			tt.breakParent(t, store, parent, entry)
			forked, err := Fork(parent)
			if err != nil {
				t.Fatalf("Fork failed: %v", err)
			}
			before := forked.Lineage.FullMessages()

			got, err := store.ForkSidecars(parent.ID, forked)
			if err == nil {
				t.Fatal("ForkSidecars succeeded despite a broken sidecar")
			}
			if !reflect.DeepEqual(got.Lineage.FullMessages(), before) {
				t.Errorf("fork content changed on copy failure: got %+v, want %+v", got.Lineage.FullMessages(), before)
			}
		})
	}
}

func TestForkSidecarsAbsentSidecarFilesRewriteNothing(t *testing.T) {
	store := newRecallTestStore(t)
	parent := Session{ID: "parent-session", Title: "Parent", Model: "model"}
	parentHistory, err := store.HistoryPath(parent.ID)
	if err != nil {
		t.Fatalf("HistoryPath failed: %v", err)
	}
	parentRecallDir, err := store.RecallDir(parent.ID)
	if err != nil {
		t.Fatalf("RecallDir failed: %v", err)
	}
	name := "tool-call-1-" + strings.Repeat("a", 64)
	note := agent.HistoryNoteMarker + " Conversation before this summary is archived at " + parentHistory + " (markdown, oldest first, tool results omitted)."
	parent.Lineage = agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:            1,
			SummaryPrefix: []agent.Message{{Role: agent.MessageRoleSummary, Content: "Earlier work summary.\n\n" + note}},
			Messages: []agent.Message{
				{Role: agent.MessageRoleTool, Name: "bash", ToolCallID: "call-1", Content: forkMarker(name, filepath.Join(parentRecallDir, name+recallFileExt))},
			},
		}},
		NextGenerationID: 2,
	}
	forked, err := Fork(parent)
	if err != nil {
		t.Fatalf("Fork failed: %v", err)
	}
	before := forked.Lineage.FullMessages()

	got, err := store.ForkSidecars(parent.ID, forked)
	if err != nil {
		t.Fatalf("ForkSidecars failed for absent sidecars: %v", err)
	}
	if !reflect.DeepEqual(got.Lineage.FullMessages(), before) {
		t.Errorf("fork content changed with nothing copied: got %+v, want %+v", got.Lineage.FullMessages(), before)
	}
	if _, err := os.Stat(filepath.Join(store.dir, forked.ID)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("fork session directory created with nothing copied: %v", err)
	}
}

func TestStoreDeleteRemovesRecallFiles(t *testing.T) {
	store := newRecallTestStore(t)
	now := time.Now().UTC()
	if err := store.Save(Session{ID: "delete-recall", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	path, err := store.WriteRecall(context.Background(), "delete-recall", recallTestEntry())
	if err != nil {
		t.Fatalf("WriteRecall failed: %v", err)
	}

	if err := store.Delete("delete-recall"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recall file survived session deletion: %v", err)
	}
}

func TestStoreEvictionRemovesRecallFiles(t *testing.T) {
	store := newRecallTestStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	if err := store.Save(Session{ID: "oldest", CreatedAt: base, UpdatedAt: base}); err != nil {
		t.Fatalf("Save(oldest) failed: %v", err)
	}
	path, err := store.WriteRecall(context.Background(), "oldest", recallTestEntry())
	if err != nil {
		t.Fatalf("WriteRecall failed: %v", err)
	}
	for i := range maxSessions {
		id := fmt.Sprintf("session-%d", i)
		updated := base.Add(time.Duration(i+1) * time.Minute)
		if err := store.Save(Session{ID: id, CreatedAt: updated, UpdatedAt: updated}); err != nil {
			t.Fatalf("Save(%s) failed: %v", id, err)
		}
	}

	if _, err := store.Load("oldest"); err == nil {
		t.Error("oldest session survived eviction")
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("recall file survived session eviction: %v", err)
	}
}
