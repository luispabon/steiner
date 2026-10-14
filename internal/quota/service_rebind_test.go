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
	// listed as an unregistered account and is never fetched.
	views := svc.Views("x")
	if len(views) != 2 {
		t.Fatalf("Views returned %d accounts, want 2", len(views))
	}
	if v := views[0]; v.Key != "k1" || v.DisplayAlias != "k1" || v.Active || v.Latest == nil {
		t.Errorf("views[0] = %s (%s) Active %v Latest nil %v, want unregistered k1 with its snapshot",
			v.Key, v.DisplayAlias, v.Active, v.Latest == nil)
	}
	if v := views[1]; v.Key != "k2" || v.DisplayAlias != "x" || !v.Active {
		t.Errorf("views[1] = %s (%s) Active %v, want active k2 aliased x", v.Key, v.DisplayAlias, v.Active)
	}

	svc.RefreshAll(context.Background())
	if got := f.callCount(); got != 0 {
		t.Errorf("fetch calls for dropped k1 = %d, want 0", got)
	}
}
