package interactive

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

type testGroupScopes struct {
	mu        sync.Mutex
	next      int
	ledgers   map[string]agent.DelegationGroupLedger
	released  []string
	snapshots []string
	events    []string
}

func (g *testGroupScopes) new(seed agent.DelegationGroupLedger) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	id := string(rune('a' + g.next - 1))
	g.ledgers[id] = seed.Clone()
	return id
}

func (g *testGroupScopes) snapshot(id string) agent.DelegationGroupLedger {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.snapshots = append(g.snapshots, id)
	g.events = append(g.events, "snapshot:"+id)
	return g.ledgers[id].Clone()
}

func (g *testGroupScopes) set(id string, ledger agent.DelegationGroupLedger) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ledgers[id] = ledger.Clone()
}

func (g *testGroupScopes) release(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.released = append(g.released, id)
	g.events = append(g.events, "release:"+id)
	delete(g.ledgers, id)
}

func (g *testGroupScopes) snapshotCalls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.snapshots...)
}

func (g *testGroupScopes) eventLog() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.events...)
}

func (g *testGroupScopes) hasScope(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	_, ok := g.ledgers[id]
	return ok
}

func (g *testGroupScopes) releasedScopes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.released...)
}

func TestDriverGroupScopeCapturedForwardedAndReleased(t *testing.T) {
	groups := &testGroupScopes{ledgers: make(map[string]agent.DelegationGroupLedger)}
	s := testNewSession(t, Dependencies{
		SessionStore:        newMockSessionStore(),
		NewGroupScope:       groups.new,
		SnapshotGroupLedger: groups.snapshot,
		SealGroupBatch: func(scope, batch string) {
			if scope != "a" || batch != "batch" {
				t.Errorf("seal scope/batch = %q/%q", scope, batch)
			}
		},
		ReleaseGroupScope: groups.release,
	})
	inputs := make(chan RunInput, 1)
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		inputs <- in
		return withAnswer(in, "done"), nil
	}})
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	in := recv(t, inputs, "interactive run")
	if in.DelegationGroupScope != "a" {
		t.Fatalf("run scope = %q, want a", in.DelegationGroupScope)
	}
	in.OnToolBatchDone("batch")
	groups.set("a", agent.DelegationGroupLedger{Version: 1, Names: []string{"live"}})
	waitSettled(t, s)
	old := s.driver
	if err := s.Handle(context.Background(), RotateSession{}); err != nil {
		t.Fatal(err)
	}
	if s.driver.groupScope != "b" {
		t.Fatalf("successor scope = %q, want b", s.driver.groupScope)
	}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, []string{"live"}) {
		t.Fatalf("inherited ledger = %v", got)
	}
	if got := groups.releasedScopes(); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("released before/after close = %v", got)
	}
	if groups.hasScope(old.groupScope) {
		t.Fatal("retired scope remains after release")
	}
	if err := s.clearConversation(); err != nil {
		t.Fatal(err)
	}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; len(got) != 0 {
		t.Fatalf("clear inherited names = %v", got)
	}
	s.Close(context.Background())
}

func TestDriverGroupScopeSetConversationUsesLiveLedgerAndCallbacks(t *testing.T) {
	groups := &testGroupScopes{ledgers: make(map[string]agent.DelegationGroupLedger)}
	sealCalls := make([]string, 0)
	s := testNewSession(t, Dependencies{
		NewGroupScope:       groups.new,
		SnapshotGroupLedger: groups.snapshot,
		SealGroupBatch:      func(scope, batch string) { sealCalls = append(sealCalls, scope+":"+batch) },
		ReleaseGroupScope:   groups.release,
	})
	groups.set(s.driver.groupScope, agent.DelegationGroupLedger{Version: 1, Names: []string{"runtime"}})
	oldScope := s.driver.groupScope
	s.SetConversation([]agent.Message{{Role: agent.MessageRoleUser, Content: "new"}})
	if s.driver.groupScope == oldScope {
		t.Fatal("SetConversation reused scope")
	}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, []string{"runtime"}) {
		t.Fatalf("successor seed = %v", got)
	}
	s.SetRunner(&inputRunner{run: func(_ context.Context, in RunInput) (RunResult, error) {
		if in.DelegationGroupScope != s.driver.groupScope {
			t.Errorf("run scope = %q, driver scope = %q", in.DelegationGroupScope, s.driver.groupScope)
		}
		in.OnToolBatchDone("next")
		return withAnswer(in, "done"), nil
	}})
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	waitSettled(t, s)
	if !reflect.DeepEqual(sealCalls, []string{s.driver.groupScope + ":next"}) {
		t.Fatalf("seal calls = %v", sealCalls)
	}
	s.Close(context.Background())
}

func TestRetireBusyDriverReleasesScopeAfterFinalSaveAndClose(t *testing.T) {
	groups := &testGroupScopes{ledgers: make(map[string]agent.DelegationGroupLedger)}
	s := testNewSession(t, Dependencies{NewGroupScope: groups.new, SnapshotGroupLedger: groups.snapshot, ReleaseGroupScope: groups.release})
	started := make(chan struct{})
	finish := make(chan struct{})
	s.SetRunner(&inputRunner{run: func(ctx context.Context, in RunInput) (RunResult, error) {
		close(started)
		select {
		case <-finish:
		case <-ctx.Done():
		}
		return withAnswer(in, "settled"), nil
	}})
	if err := s.Handle(context.Background(), SubmitPrompt{Text: "go"}); err != nil {
		t.Fatal(err)
	}
	<-started
	old := s.driver
	s.retireDriver(old)
	if got := groups.releasedScopes(); len(got) != 0 {
		t.Fatalf("released while busy = %v", got)
	}
	close(finish)
	s.runs.Wait()
	if got := groups.releasedScopes(); !reflect.DeepEqual(got, []string{old.groupScope}) {
		t.Fatalf("release order = %v", got)
	}
	if groups.hasScope(old.groupScope) {
		t.Fatal("released scope was not deleted")
	}
	events := groups.eventLog()
	lastSnapshot, release := -1, -1
	for i, event := range events {
		if event == "snapshot:"+old.groupScope {
			lastSnapshot = i
		}
		if event == "release:"+old.groupScope {
			release = i
		}
	}
	if lastSnapshot < 0 || release <= lastSnapshot {
		t.Fatalf("final snapshot/release order = %v", events)
	}
	s.Close(context.Background())
}

func TestDriverWithoutScopeDoesNotSnapshotOrRelease(t *testing.T) {
	seed := agent.DelegationGroupLedger{Version: 1, Names: []string{"seed"}}
	groups := &testGroupScopes{ledgers: make(map[string]agent.DelegationGroupLedger)}
	s := testNewSession(t, Dependencies{SnapshotGroupLedger: groups.snapshot, ReleaseGroupScope: groups.release})
	s.mu.Lock()
	s.delegationGroups = &seed
	s.driver = s.newDriverLocked(nil, agent.ConversationLineage{})
	s.mu.Unlock()
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, seed.Names) {
		t.Fatalf("seed ledger = %v", got)
	}
	s.retireDriver(s.driver)
	if got := s.driver.drv.Snapshot().GroupLedger.Names; !reflect.DeepEqual(got, seed.Names) {
		t.Fatalf("seed ledger after empty-scope retirement = %v", got)
	}
	if got := groups.snapshotCalls(); len(got) != 0 {
		t.Fatalf("snapshots with empty scope = %v", got)
	}
	if got := groups.releasedScopes(); len(got) != 0 {
		t.Fatalf("releases with empty scope = %v", got)
	}
}
