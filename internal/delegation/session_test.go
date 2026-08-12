package delegation

import (
	"fmt"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestSessionStoreSaveGetUpdate(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()
	initialConversation := []agent.Message{{Role: agent.MessageRoleUser, Content: "task"}}
	session := &ChildSession{
		Spec: DelegationSpec{AgentID: "child-1", Task: "delegate"},
		Request: agent.RunRequest{
			Limits: agent.Limits{MaxTurns: 3},
		},
		Conversation:  initialConversation,
		TurnCount:     1,
		TokenCount:    10,
		ToolCallCount: 2,
		CacheUsage:    CacheUsage{InputTokens: 1, CacheReadTokens: 2, CacheCreateTokens: 3},
	}

	store.Save(session)

	got, ok := store.Get("child-1")
	if !ok {
		t.Fatal("Get() returned false, want true")
	}
	if got != session {
		t.Fatal("Get() returned different session pointer")
	}

	updatedConversation := []agent.Message{{Role: agent.MessageRoleAssistant, Content: "done"}}
	store.Update("child-1", SessionUpdateParams{
		Conversation:  updatedConversation,
		TurnCount:     2,
		TokenCount:    20,
		ToolCallCount: 3,
		CacheUsage:    CacheUsage{InputTokens: 4, CacheReadTokens: 5, CacheCreateTokens: 6},
	})

	got, ok = store.Get("child-1")
	if !ok {
		t.Fatal("Get() after Update() returned false, want true")
	}
	if len(got.Conversation) != 1 || got.Conversation[0].Content != "done" {
		t.Fatalf("Conversation = %#v, want replacement conversation", got.Conversation)
	}
	if got.TurnCount != 3 {
		t.Fatalf("TurnCount = %d, want 3", got.TurnCount)
	}
	if got.TokenCount != 30 {
		t.Fatalf("TokenCount = %d, want 30", got.TokenCount)
	}
	if got.ToolCallCount != 5 {
		t.Fatalf("ToolCallCount = %d, want 5", got.ToolCallCount)
	}
	if got.FollowUpCount != 1 {
		t.Fatalf("FollowUpCount = %d, want 1", got.FollowUpCount)
	}
	wantCache := CacheUsage{InputTokens: 5, CacheReadTokens: 7, CacheCreateTokens: 9}
	if got.CacheUsage != wantCache {
		t.Fatalf("CacheUsage = %+v, want %+v (seeded plus update delta)", got.CacheUsage, wantCache)
	}
}

func TestSessionStoreGetUnknownID(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	if _, ok := store.Get("missing"); ok {
		t.Fatal("Get() returned true for unknown id")
	}

	store.Update("missing", SessionUpdateParams{TurnCount: 1, TokenCount: 1, ToolCallCount: 1})

	if _, ok := store.Get("missing"); ok {
		t.Fatal("Get() returned true for unknown id after Update()")
	}
}

func TestSessionStoreConcurrentAccess(t *testing.T) {
	store := NewSessionStore()

	const sessionCount = 32
	const updatesPerSession = 8

	var saveWG sync.WaitGroup
	saveWG.Add(sessionCount)
	for i := range sessionCount {
		go func(i int) {
			defer saveWG.Done()
			store.Save(&ChildSession{
				Spec: DelegationSpec{
					AgentID: fmt.Sprintf("child-%d", i),
				},
			})
		}(i)
	}
	saveWG.Wait()

	var updateWG sync.WaitGroup
	updateWG.Add(sessionCount)
	for i := range sessionCount {
		go func(i int) {
			defer updateWG.Done()
			id := fmt.Sprintf("child-%d", i)
			for j := range updatesPerSession {
				store.Update(id, SessionUpdateParams{
					Conversation:  []agent.Message{{Role: agent.MessageRoleAssistant, Content: fmt.Sprintf("turn-%d", j)}},
					TurnCount:     1,
					TokenCount:    2,
					ToolCallCount: 3,
					CacheUsage:    CacheUsage{InputTokens: 4, CacheReadTokens: 5, CacheCreateTokens: 6},
				})
			}
		}(i)
	}
	updateWG.Wait()

	for i := range sessionCount {
		id := fmt.Sprintf("child-%d", i)
		session, ok := store.Get(id)
		if !ok {
			t.Fatalf("Get(%q) returned false, want true", id)
		}
		if session.TurnCount != updatesPerSession {
			t.Fatalf("TurnCount for %q = %d, want %d", id, session.TurnCount, updatesPerSession)
		}
		if session.TokenCount != updatesPerSession*2 {
			t.Fatalf("TokenCount for %q = %d, want %d", id, session.TokenCount, updatesPerSession*2)
		}
		if session.ToolCallCount != updatesPerSession*3 {
			t.Fatalf("ToolCallCount for %q = %d, want %d", id, session.ToolCallCount, updatesPerSession*3)
		}
		if session.FollowUpCount != updatesPerSession {
			t.Fatalf("FollowUpCount for %q = %d, want %d", id, session.FollowUpCount, updatesPerSession)
		}
		wantCache := CacheUsage{
			InputTokens:       updatesPerSession * 4,
			CacheReadTokens:   updatesPerSession * 5,
			CacheCreateTokens: updatesPerSession * 6,
		}
		if session.CacheUsage != wantCache {
			t.Fatalf("CacheUsage for %q = %+v, want %+v", id, session.CacheUsage, wantCache)
		}
		if len(session.Conversation) != 1 {
			t.Fatalf("Conversation length for %q = %d, want 1", id, len(session.Conversation))
		}
	}
}

func TestSessionStore_Reset(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()
	store.Save(&ChildSession{
		Spec: DelegationSpec{AgentID: "child-1"},
	})
	store.Save(&ChildSession{
		Spec: DelegationSpec{AgentID: "child-2"},
	})

	if got, want := store.Count(), 2; got != want {
		t.Fatalf("Count() before Reset = %d, want %d", got, want)
	}

	store.Reset()

	if got, want := store.Count(), 0; got != want {
		t.Fatalf("Count() after Reset = %d, want %d", got, want)
	}

	if _, ok := store.Get("child-1"); ok {
		t.Fatal("Get(child-1) after Reset returned true, want false")
	}
	if _, ok := store.Get("child-2"); ok {
		t.Fatal("Get(child-2) after Reset returned true, want false")
	}
}

func TestSessionStore_Count(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()
	if got, want := store.Count(), 0; got != want {
		t.Fatalf("Count() on empty store = %d, want %d", got, want)
	}

	store.Save(&ChildSession{Spec: DelegationSpec{AgentID: "a"}})
	if got, want := store.Count(), 1; got != want {
		t.Fatalf("Count() after one save = %d, want %d", got, want)
	}

	store.Save(&ChildSession{Spec: DelegationSpec{AgentID: "b"}})
	if got, want := store.Count(), 2; got != want {
		t.Fatalf("Count() after two saves = %d, want %d", got, want)
	}
}
