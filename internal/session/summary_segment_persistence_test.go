package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

// segmentedLineage returns a lineage whose latest generation holds an immutable
// two-segment chain followed by a mutable segment-free tail.
func segmentedLineage() agent.ConversationLineage {
	return agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{
			{
				ID: 1,
				Messages: []agent.Message{
					{Role: agent.MessageRoleUser, Content: "first turn"},
					{Role: agent.MessageRoleAssistant, Content: "first answer"},
				},
			},
			{
				ID: 2,
				SummaryPrefix: []agent.Message{
					{
						Role:    agent.MessageRoleSummary,
						Content: "Segment 1.\n\nfirst span",
						Segment: &agent.SummarySegment{Sequence: 1, SourceMessageCount: 2},
					},
					{
						Role:    agent.MessageRoleSummary,
						Content: "Segment 2.\n\nsecond span",
						Segment: &agent.SummarySegment{Sequence: 2, SourceMessageCount: 9},
					},
					{Role: agent.MessageRoleSummary, Content: "Segment 3. mutable tail"},
				},
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "second turn"}},
			},
		},
		NextGenerationID: 3,
	}
}

func TestStoreRoundTripsSummarySegments(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	original := Session{
		ID:        "segment-round-trip",
		CreatedAt: time.Unix(1700000000, 0).UTC(),
		UpdatedAt: time.Unix(1700000500, 0).UTC(),
		Title:     "Segment round trip",
		Model:     "test-model",
		Lineage:   segmentedLineage(),
	}
	if err := store.Save(original); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	loaded, err := store.Load(original.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !reflect.DeepEqual(loaded, original) {
		t.Fatalf("loaded session = %#v, want %#v", loaded, original)
	}

	prefix := loaded.Lineage.Generations[1].SummaryPrefix
	if len(prefix) != 3 {
		t.Fatalf("loaded summary prefix length = %d, want 3", len(prefix))
	}
	wantSegments := []agent.SummarySegment{
		{Sequence: 1, SourceMessageCount: 2},
		{Sequence: 2, SourceMessageCount: 9},
	}
	for i, want := range wantSegments {
		got := prefix[i].Segment
		if got == nil {
			t.Fatalf("loaded SummaryPrefix[%d].Segment = nil, want %+v", i, want)
		}
		if *got != want {
			t.Errorf("loaded SummaryPrefix[%d].Segment = %+v, want %+v", i, *got, want)
		}
	}
	if got := prefix[2].Segment; got != nil {
		t.Errorf("loaded tail Segment = %+v, want nil", got)
	}

	data, err := os.ReadFile(filepath.Join(dir, original.ID+sessionExt))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	if got := strings.Count(string(data), `"segment"`); got != 2 {
		t.Errorf("session file holds %d segment keys, want 2: %s", got, data)
	}
	for _, want := range []string{
		`"segment":{"sequence":1,"source_message_count":2}`,
		`"segment":{"sequence":2,"source_message_count":9}`,
	} {
		if !strings.Contains(string(data), want) {
			t.Errorf("session file does not hold %s: %s", want, data)
		}
	}
}

func TestLoadLegacySessionWithoutSegmentsRemarshalsWithoutSegmentKey(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	const legacyID = "legacy-session"
	legacyJSON := `{"id":"legacy-session","created_at":"2026-01-02T03:04:05Z","updated_at":"2026-01-02T03:04:05Z",` +
		`"title":"Legacy","model":"legacy-model","conversation":[` +
		`{"role":"user","content":"old question"},` +
		`{"role":"summary","content":"old summary"},` +
		`{"role":"assistant","content":"old answer"}]}`
	if err := os.WriteFile(filepath.Join(dir, legacyID+sessionExt), []byte(legacyJSON), 0o600); err != nil {
		t.Fatalf("write legacy session: %v", err)
	}

	loaded, err := store.Load(legacyID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	wantLineage := agent.ConversationLineage{
		Generations: []agent.ConversationGeneration{{
			ID:       1,
			Messages: legacyConversation(),
		}},
		NextGenerationID: 2,
	}
	if !reflect.DeepEqual(loaded.Lineage, wantLineage) {
		t.Fatalf("restored legacy lineage = %#v, want %#v", loaded.Lineage, wantLineage)
	}

	remarshaled, err := json.Marshal(loaded)
	if err != nil {
		t.Fatalf("marshal loaded legacy session: %v", err)
	}
	if strings.Contains(string(remarshaled), `"segment"`) {
		t.Errorf("re-marshaled legacy session holds a segment key: %s", remarshaled)
	}

	// Re-saving the restored legacy session must reload to the same lineage.
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	reloaded, err := store.Load(legacyID)
	if err != nil {
		t.Fatalf("Load after re-save failed: %v", err)
	}
	if !reflect.DeepEqual(reloaded.Lineage, wantLineage) {
		t.Errorf("reloaded legacy lineage = %#v, want %#v", reloaded.Lineage, wantLineage)
	}
}

func TestForkRewritePreservesSummarySegments(t *testing.T) {
	const (
		parentHistory = "/store/parent/history.md"
		forkHistory   = "/store/fork/history.md"
	)
	noteContent := func(seq int) string {
		return fmt.Sprintf("Segment %d body.\n%s Conversation before this summary is archived at %s\n", seq, agent.HistoryNoteMarker, parentHistory)
	}
	segmentOne := &agent.SummarySegment{Sequence: 1, SourceMessageCount: 2}
	segmentTwo := &agent.SummarySegment{Sequence: 2, SourceMessageCount: 5}
	forked := Session{
		ID:    "fork-session",
		Title: "Fork",
		Model: "test-model",
		Lineage: agent.ConversationLineage{
			Generations: []agent.ConversationGeneration{{
				ID: 2,
				SummaryPrefix: []agent.Message{
					{Role: agent.MessageRoleSummary, Content: noteContent(1), Segment: segmentOne},
					{Role: agent.MessageRoleSummary, Content: noteContent(2), Segment: segmentTwo},
					{Role: agent.MessageRoleSummary, Content: "Segment 3 tail.\n" + agent.HistoryNoteMarker + " archived at " + parentHistory + "\n"},
				},
				Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "next turn"}},
			}},
			NextGenerationID: 3,
		},
	}

	got := rewriteForkReferences(forked, forkReferenceRewrite{
		copyHistory:   true,
		parentHistory: parentHistory,
		forkHistory:   forkHistory,
	})
	prefix := got.Lineage.Generations[0].SummaryPrefix
	if len(prefix) != 3 {
		t.Fatalf("rewritten summary prefix length = %d, want 3", len(prefix))
	}
	for i, want := range []*agent.SummarySegment{segmentOne, segmentTwo} {
		if prefix[i].Segment != want {
			t.Fatalf("rewritten SummaryPrefix[%d].Segment = %+v, want the original pointer %+v", i, prefix[i].Segment, want)
		}
		if !strings.Contains(prefix[i].Content, forkHistory) {
			t.Errorf("rewritten SummaryPrefix[%d] does not name the fork history: %q", i, prefix[i].Content)
		}
		if strings.Contains(prefix[i].Content, parentHistory) {
			t.Errorf("rewritten SummaryPrefix[%d] still names the parent history: %q", i, prefix[i].Content)
		}
	}
	if got := prefix[2].Segment; got != nil {
		t.Errorf("rewritten tail Segment = %+v, want nil", got)
	}
	if !strings.Contains(prefix[2].Content, forkHistory) {
		t.Errorf("rewritten tail does not name the fork history: %q", prefix[2].Content)
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if err := store.Save(got); err != nil {
		t.Fatalf("Save failed: %v", err)
	}
	loaded, err := store.Load(got.ID)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	loadedPrefix := loaded.Lineage.Generations[0].SummaryPrefix
	wantSegments := []agent.SummarySegment{
		{Sequence: 1, SourceMessageCount: 2},
		{Sequence: 2, SourceMessageCount: 5},
	}
	for i, want := range wantSegments {
		gotSegment := loadedPrefix[i].Segment
		if gotSegment == nil || *gotSegment != want {
			t.Errorf("loaded fork SummaryPrefix[%d].Segment = %+v, want %+v", i, gotSegment, want)
		}
	}
	if got := loadedPrefix[2].Segment; got != nil {
		t.Errorf("loaded fork tail Segment = %+v, want nil", got)
	}
}
