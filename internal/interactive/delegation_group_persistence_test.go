package interactive

import (
	"context"
	"reflect"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tool"
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

func TestRetiredSnapshotPersistsBothLedgersForCreateAndExisting(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "existing"}[existing], func(t *testing.T) {
			store := newMockSessionStore()
			s := testNewSession(t, Dependencies{SessionStore: store})
			id := "retired"
			if existing {
				store.loadedSessions[id] = session.Session{ID: id, Model: "test", Title: "kept"}
			}
			groupLedger := agent.DelegationGroupLedger{Version: 1, Names: []string{"reserved"}}
			outstanding := []agent.SubAgentLedgerEntry{{AgentID: "child", AgentType: "code"}}
			snap := agent.DriverSnapshot{
				Lineage: agent.ConversationLineage{Generations: []agent.ConversationGeneration{{ID: 1, Messages: []agent.Message{{Role: agent.MessageRoleUser, Content: "retired"}}}}, NextGenerationID: 2},
				Ledger:  outstanding, GroupLedger: groupLedger,
			}
			meta := runSessionMeta{id: id, modelID: "test", cacheKey: "cache", group: "group"}
			if err := s.saveSnapshotAs(meta, snap); err != nil {
				t.Fatalf("saveSnapshotAs() = %v", err)
			}
			saved, err := store.Load(id)
			if err != nil {
				t.Fatalf("Load saved snapshot: %v", err)
			}
			if saved.DelegationGroups == nil || !reflect.DeepEqual(*saved.DelegationGroups, groupLedger) {
				t.Fatalf("saved group ledger = %#v, want %#v", saved.DelegationGroups, groupLedger)
			}
			if !reflect.DeepEqual(saved.SubAgentLedger, outstanding) {
				t.Fatalf("saved outstanding ledger = %#v, want %#v", saved.SubAgentLedger, outstanding)
			}
			if !reflect.DeepEqual(saved.Lineage, snap.Lineage) {
				t.Fatalf("saved lineage = %#v, want snapshot lineage", saved.Lineage)
			}
		})
	}
}

func TestLoadLegacyGroupLedgerMigratesAndSavesModernLedger(t *testing.T) {
	call := func(id, name, group string) agent.Message {
		return agent.Message{Role: agent.MessageRoleAssistant, ToolCalls: []agent.ToolCall{{ID: id, Name: name, Arguments: map[string]any{"group": group}}}}
	}
	result := func(id, name, status string) agent.Message {
		return agent.Message{Role: agent.MessageRoleTool, ToolCallID: id, Name: name, DelegationAdmission: &tool.DelegationAdmission{Status: status}}
	}
	lineage := agent.ConversationLineage{Generations: []agent.ConversationGeneration{{ID: 1, Messages: []agent.Message{
		call("accepted", "sub_agent", "accepted-group"), result("accepted", "sub_agent", tool.DelegationAdmissionAccepted),
		call("unknown", "follow_up", "unknown-group"),
		call("rejected", "sub_agent", "rejected-group"), result("rejected", "sub_agent", tool.DelegationAdmissionRejected),
	}}}, NextGenerationID: 2}
	store := newMockSessionStore()
	store.loadedSessions["legacy"] = session.Session{ID: "legacy", Model: "test", Lineage: lineage}
	s := testNewSession(t, Dependencies{SessionStore: store})
	if err := s.loadSession(context.Background(), "legacy"); err != nil {
		t.Fatalf("load legacy: %v", err)
	}
	want := []string{"accepted-group", "unknown-group"}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded migrated names = %#v, want %#v", got, want)
	}
	if err := s.saveSession(); err != nil {
		t.Fatalf("save migrated session: %v", err)
	}
	saved, err := store.Load("legacy")
	if err != nil {
		t.Fatalf("load saved migration: %v", err)
	}
	if saved.DelegationGroups == nil || saved.DelegationGroups.Version != 1 || !reflect.DeepEqual(saved.DelegationGroups.Names, want) {
		t.Fatalf("saved modern ledger = %#v, want v1 names %#v", saved.DelegationGroups, want)
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
