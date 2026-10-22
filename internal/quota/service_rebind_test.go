package quota

import (
	"context"
	"testing"
)

func TestRegisterRebindMovesAliasToNewAccount(t *testing.T) {
	svc, _ := newTestService(t, nil)
	svc.Register(Account{Key: "k1", Aliases: []string{"a", "b"}})
	svc.Register(Account{Key: "k2", Aliases: []string{"c"}})
	svc.Register(Account{Key: "k2", Aliases: []string{"a"}})

	if key, ok := accountKeyForAlias(svc, "a"); !ok || key != "k2" {
		t.Errorf("accountKeyForAlias(a) = %q, %v; want k2, true", key, ok)
	}

	// k1 keeps b, so its DisplayAlias moves from a to b. k2 lists a and c.
	views := svc.Views("a")
	wantKeys := []string{"k2", "k1"}
	wantAliases := []string{"a", "b"}
	if len(views) != len(wantKeys) {
		t.Fatalf("Views returned %d accounts, want %d", len(views), len(wantKeys))
	}
	for i, v := range views {
		if v.Key != wantKeys[i] || v.DisplayAlias != wantAliases[i] {
			t.Errorf("views[%d] = %s (%s), want %s (%s)", i, v.Key, v.DisplayAlias, wantKeys[i], wantAliases[i])
		}
		if wantActive := v.Key == "k2"; v.Active != wantActive {
			t.Errorf("views[%d] (%s) Active = %v, want %v", i, v.Key, v.Active, wantActive)
		}
	}
}

func TestRegisterRebindDropsEmptyAccountKeepsLatest(t *testing.T) {
	svc, _ := newTestService(t, nil)
	f := &fakeFetcher{snap: sampleSnapshot("k1", testBase, 50)}
	svc.Observe(sampleSnapshot("k1", testBase, 10))
	svc.Register(Account{Key: "k1", Aliases: []string{"x"}, Fetcher: f})
	svc.Register(Account{Key: "k2", Aliases: []string{"x"}})

	// k1 has no aliases left, so its registration is gone. Its snapshot is
	// not returned, and it is never fetched.
	views := svc.Views("x")
	if len(views) != 1 {
		t.Fatalf("Views returned %d accounts, want 1", len(views))
	}
	if v := views[0]; v.Key != "k2" || v.DisplayAlias != "x" || !v.Active {
		t.Errorf("views[0] = %s (%s) Active %v, want active k2 aliased x", v.Key, v.DisplayAlias, v.Active)
	}

	svc.RefreshAll(context.Background())
	if got := f.callCount(); got != 0 {
		t.Errorf("fetch calls for dropped k1 = %d, want 0", got)
	}

	// The dropped registration keeps its snapshot, so registering the key
	// again surfaces the same state.
	svc.Register(Account{Key: "k1", Aliases: []string{"other"}})
	views = svc.Views("")
	var k1 *AccountView
	for i := range views {
		if views[i].Key == "k1" {
			k1 = &views[i]
		}
	}
	if k1 == nil || k1.Latest == nil || k1.Latest.Windows[0].UsedPercent != 10 {
		t.Errorf("re-registered k1 view = %+v, want its retained snapshot at 10%%", k1)
	}
}
