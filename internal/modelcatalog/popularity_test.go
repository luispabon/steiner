package modelcatalog

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestStorePopularity(t *testing.T) {
	tests := []struct {
		name string
		test func(t *testing.T, path string)
	}{
		{
			name: "records increment across stores",
			test: func(t *testing.T, path string) {
				first := NewStore(path)
				second := NewStore(path)
				if err := first.Record("openai", "gpt-4.1"); err != nil {
					t.Fatalf("first record: %v", err)
				}
				if err := second.Record("openai", "gpt-4.1"); err != nil {
					t.Fatalf("second record: %v", err)
				}
				if got := second.Snapshot()[Key{ProviderAlias: "openai", ModelID: "gpt-4.1"}]; got != 2 {
					t.Fatalf("count: got %d, want 2", got)
				}
			},
		},
		{
			name: "corrupt file is empty",
			test: func(t *testing.T, path string) {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("create parent: %v", err)
				}
				if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
					t.Fatalf("write corrupt file: %v", err)
				}
				if got := NewStore(path).Snapshot(); len(got) != 0 {
					t.Fatalf("snapshot: got %v, want empty", got)
				}
			},
		},
		{
			name: "record creates missing directories",
			test: func(t *testing.T, path string) {
				if err := NewStore(path).Record("anthropic", "claude"); err != nil {
					t.Fatalf("record: %v", err)
				}
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("stat popularity file: %v", err)
				}
				if _, err := os.Stat(filepath.Dir(path)); err != nil {
					t.Fatalf("stat parent: %v", err)
				}
			},
		},
		{
			name: "snapshot has recorded keys",
			test: func(t *testing.T, path string) {
				store := NewStore(path)
				for _, model := range []string{"small", "large"} {
					if err := store.Record("local", model); err != nil {
						t.Fatalf("record %s: %v", model, err)
					}
				}
				got := store.Snapshot()
				if len(got) != 2 || got[Key{ProviderAlias: "local", ModelID: "small"}] != 1 || got[Key{ProviderAlias: "local", ModelID: "large"}] != 1 {
					t.Fatalf("snapshot: got %v", got)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			test.test(t, filepath.Join(t.TempDir(), "nested", "model-popularity.json"))
		})
	}
}

func TestStoreConcurrentRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model-popularity.json")
	store := NewStore(path)
	const records = 32

	var wait sync.WaitGroup
	errs := make(chan error, records)
	wait.Add(records)
	for range records {
		go func() {
			defer wait.Done()
			errs <- store.Record("provider", "model")
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("record: %v", err)
		}
	}

	key := Key{ProviderAlias: "provider", ModelID: "model"}
	if got := store.Snapshot()[key]; got != records {
		t.Fatalf("count: got %d, want %d", got, records)
	}
}
