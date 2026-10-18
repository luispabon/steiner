package quota

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestViewsHideUnregisteredLegacyAPIKeyRows(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	for _, key := range []string{"openrouter:0123456789abcdef", "codex:acct-1", "provider:retired"} {
		if err := store.Append(sampleSnapshot(key, testBase, 10), testBase); err != nil {
			t.Fatalf("seed Append(%s): %v", key, err)
		}
	}
	svc, _ := newTestService(t, store)
	svc.Register(Account{Key: APIKeyAccountKey("main"), ProviderType: "openrouter", Aliases: []string{"main"}})
	svc.Observe(sampleSnapshot(APIKeyAccountKey("main"), testBase, 25))

	views := svc.Views("main")
	var keys []string
	for _, v := range views {
		keys = append(keys, v.Key)
	}
	wantKeys := []string{"codex:acct-1", "provider:main", "provider:retired"}
	if !slices.Equal(keys, wantKeys) {
		t.Fatalf("view keys = %q, want %q", keys, wantKeys)
	}

	main := views[1]
	if !main.Active || main.ProviderType != "openrouter" {
		t.Errorf("registered alias view Active = %v, ProviderType = %q; want active openrouter", main.Active, main.ProviderType)
	}
	if main.Latest == nil || len(main.Latest.Windows) != 1 || main.Latest.Windows[0].UsedPercent != 25 {
		t.Errorf("registered alias Latest = %+v, want current snapshot at 25%%", main.Latest)
	}
}
