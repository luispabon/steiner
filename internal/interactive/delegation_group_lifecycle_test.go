package interactive

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

type testGroupScopes struct {
	mu       sync.Mutex
	next     int
	ledgers  map[string]agent.DelegationGroupLedger
	released []string
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
	if got := groups.snapshot(old.groupScope).Names; !reflect.DeepEqual(got, []string{"live"}) {
		t.Fatalf("retired snapshot = %v", got)
	}
	if err := s.clearConversation(); err != nil {
		t.Fatal(err)
	}
	if got := s.driver.drv.Snapshot().GroupLedger.Names; len(got) != 0 {
		t.Fatalf("clear inherited names = %v", got)
	}
	s.Close(context.Background())
}
