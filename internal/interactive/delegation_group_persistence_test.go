package interactive

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/session"
)

func TestInteractiveGroupLedgerSeedSaveAndLoad(t *testing.T) {
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store})
	initial := s.driver.drv.Snapshot().GroupLedger
	if initial.Version != 1 || len(initial.Names) != 0 {
		t.Fatalf("initial group ledger = %#v, want explicit empty v1", initial)
	}

	seed := agent.DelegationGroupLedger{Version: 1, Names: []string{" beta ", "alpha"}}
	s.mu.Lock()
	s.delegationGroups = cloneDelegationGroupLedger(&seed)
	s.driver = s.newDriverLocked(nil, agent.ConversationLineage{})
	s.mu.Unlock()
	seed.Names[0] = "mutated"
	got := s.driver.drv.Snapshot().GroupLedger
	if !reflect.DeepEqual(got.Names, []string{"alpha", "beta"}) {
		t.Fatalf("driver group ledger = %#v, want cloned normalized names", got)
	}

	if err := s.saveSession(); err != nil {
		t.Fatalf("saveSession() = %v", err)
	}
	saved := store.savedSessions[s.SessionID()]
	if saved.DelegationGroups == nil || !reflect.DeepEqual(saved.DelegationGroups.Names, got.Names) {
		t.Fatalf("saved groups = %#v, want %#v", saved.DelegationGroups, got)
	}
	saved.DelegationGroups.Names[0] = "changed"
	if s.delegationGroups.Names[0] != "alpha" {
		t.Fatal("saved session aliases live group ledger")
	}
}

func TestLoadSessionGroupLedgerExplicitEmptyAndUnsupportedIsNonDestructive(t *testing.T) {
	store := newMockSessionStore()
	store.loadedSessions["empty"] = session.Session{ID: "empty", Model: "test", DelegationGroups: &agent.DelegationGroupLedger{Version: 1}}
	store.loadedSessions["unsupported"] = session.Session{ID: "unsupported", Model: "test", DelegationGroups: &agent.DelegationGroupLedger{Version: 2}}
	s := testNewSession(t, Dependencies{SessionStore: store, Config: config.Config{Modes: config.ModesConfig{Default: config.ExecutionModePlan}}})
	if err := s.loadSession(context.Background(), "empty"); err != nil {
		t.Fatalf("load explicit empty: %v", err)
	}
	if got := s.driver.drv.Snapshot().GroupLedger; got.Version != 1 || len(got.Names) != 0 {
		t.Fatalf("loaded explicit empty ledger = %#v", got)
	}

	beforeID := s.SessionID()
	beforeConversation := s.Conversation()
	beforeLedger := s.driver.drv.Snapshot().GroupLedger
	beforeSaved := store.savedSessions["unsupported"]
	if err := s.loadSession(context.Background(), "unsupported"); err == nil {
		t.Fatal("load unsupported ledger succeeded")
	}
	if s.SessionID() != beforeID || !reflect.DeepEqual(s.Conversation(), beforeConversation) || !reflect.DeepEqual(s.driver.drv.Snapshot().GroupLedger, beforeLedger) {
		t.Fatal("unsupported load changed live session identity, history, or group ledger")
	}
	if !reflect.DeepEqual(store.savedSessions["unsupported"], beforeSaved) {
		t.Fatal("unsupported load changed saved session")
	}
}

func TestSnapshotUnchangedChecksGroupLedgerForEmptyConversation(t *testing.T) {
	before := &agent.DriverSnapshot{GroupLedger: agent.DelegationGroupLedger{Version: 1}}
	after := agent.DriverSnapshot{GroupLedger: agent.DelegationGroupLedger{Version: 1, Names: []string{"group"}}}
	if snapshotUnchanged(before, after) {
		t.Fatal("ledger-only change on empty conversation treated as unchanged")
	}
}

func TestForkSessionCopiesGroupLedgerIndependently(t *testing.T) {
	store := newMockSessionStore()
	s := testNewSession(t, Dependencies{SessionStore: store})
	s.mu.Lock()
	s.delegationGroups = &agent.DelegationGroupLedger{Version: 1, Names: []string{"reserved"}}
	s.mu.Unlock()
	if err := s.handleForkSession(context.Background()); err != nil {
		t.Fatalf("handleForkSession() = %v", err)
	}
	var fork *session.Session
	for id, saved := range store.savedSessions {
		if id != s.SessionID() && saved.DelegationGroups != nil {
			copy := saved
			fork = &copy
		}
	}
	if fork == nil || !reflect.DeepEqual(fork.DelegationGroups.Names, []string{"reserved"}) {
		t.Fatalf("fork groups = %#v, want reserved group", fork)
	}
	fork.DelegationGroups.Names[0] = "fork-only"
	if s.delegationGroups.Names[0] != "reserved" {
		t.Fatal("fork group ledger aliases live session")
	}
}
