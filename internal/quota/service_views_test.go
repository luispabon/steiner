package quota

import (
	"path/filepath"
	"testing"
)

func TestViewsShowOnlyRegisteredAccounts(t *testing.T) {
	seeded := []string{
		"opencode_go:0123456789abcdef",
		"openrouter:0123456789abcdef",
		"litellm:fedcba9876543210",
		"codex:acct-1",
		"provider:retired",
	}

	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	for _, key := range seeded {
		if err := store.Append(sampleSnapshot(key, testBase, 10), testBase); err != nil {
			t.Fatalf("seed Append(%s): %v", key, err)
		}
	}
	svc, _ := newTestService(t, store)
	if views := svc.Views("main"); len(views) != 0 {
		t.Fatalf("Views with no registrations = %d rows, want 0", len(views))
	}

	svc.Register(Account{Key: APIKeyAccountKey("main"), ProviderType: "openrouter", Aliases: []string{"main"}})
	svc.Observe(sampleSnapshot(APIKeyAccountKey("main"), testBase, 25))

	views := svc.Views("main")
	if len(views) != 1 {
		t.Fatalf("Views returned %d rows, want 1 (provider:main)", len(views))
	}
	main := views[0]
	if main.Key != APIKeyAccountKey("main") || !main.Active || main.ProviderType != "openrouter" {
		t.Errorf("registered view Key = %q, Active = %v, ProviderType = %q; want provider:main, active, openrouter", main.Key, main.Active, main.ProviderType)
	}
	if main.Latest == nil || len(main.Latest.Windows) != 1 || main.Latest.Windows[0].UsedPercent != 25 {
		t.Errorf("registered alias Latest = %+v, want current snapshot at 25%%", main.Latest)
	}

	// Views only filters: the unregistered snapshots remain in the store.
	stored, err := store.readAll()
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	have := make(map[string]bool)
	for _, snap := range stored {
		have[snap.AccountKey] = true
	}
	for _, key := range seeded {
		if !have[key] {
			t.Errorf("stored snapshot for %s missing after Views", key)
		}
	}
}

func TestViewsShowStoredSnapshotAfterRegistration(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "quota-history.json"))
	seeded := sampleSnapshot(APIKeyAccountKey("retired"), testBase, 40)
	if err := store.Append(seeded, testBase); err != nil {
		t.Fatalf("seed Append: %v", err)
	}
	svc, _ := newTestService(t, store)
	if views := svc.Views(""); len(views) != 0 {
		t.Fatalf("Views before registration = %d rows, want 0", len(views))
	}

	svc.Register(Account{Key: APIKeyAccountKey("retired"), Aliases: []string{"retired"}})
	views := svc.Views("")
	if len(views) != 1 || views[0].Key != seeded.AccountKey {
		t.Fatalf("Views after registration = %+v, want one row for %s", views, seeded.AccountKey)
	}
	latest := views[0].Latest
	if latest == nil || !latest.FetchedAt.Equal(seeded.FetchedAt) || len(latest.Windows) != 1 || latest.Windows[0].UsedPercent != 40 {
		t.Errorf("Latest = %+v, want the seeded snapshot at 40%%", latest)
	}
}
