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
	store.Update("child-1", updatedConversation, 2, 20, 3)

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
}

func TestSessionStoreGetUnknownID(t *testing.T) {
	t.Parallel()

	store := NewSessionStore()

	if _, ok := store.Get("missing"); ok {
		t.Fatal("Get() returned true for unknown id")
	}

	store.Update("missing", nil, 1, 1, 1)

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
				store.Update(id, []agent.Message{{Role: agent.MessageRoleAssistant, Content: fmt.Sprintf("turn-%d", j)}}, 1, 2, 3)
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
		if len(session.Conversation) != 1 {
			t.Fatalf("Conversation length for %q = %d, want 1", id, len(session.Conversation))
		}
	}
}
