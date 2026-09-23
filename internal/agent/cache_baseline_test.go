package agent

import (
	"sync"
	"testing"
)

func TestCacheBaselineStore_EmptyKeyIsNoOp(t *testing.T) {
	store := NewCacheBaselineStore()
	if shared, known := store.CompareAndPromote(CacheBaselineKey{}, []string{"a"}); shared != 0 || known {
		t.Fatalf("CompareAndPromote(empty key) = (%d, %v), want (0, false)", shared, known)
	}
	if len(store.entries) != 0 {
		t.Fatalf("store has %d entries after an empty-key call, want 0", len(store.entries))
	}
}

func TestCacheBaselineStore_NilReceiverIsNoOp(t *testing.T) {
	var store *CacheBaselineStore
	if shared, known := store.CompareAndPromote(CacheBaselineKey{CacheKey: "k"}, []string{"a"}); shared != 0 || known {
		t.Fatalf("CompareAndPromote on nil store = (%d, %v), want (0, false)", shared, known)
	}
}

func TestCacheBaselineStore_IdentityIsolation(t *testing.T) {
	store := NewCacheBaselineStore()
	base := CacheBaselineKey{
		CacheKey:               "k",
		ConfiguredProviderType: "openai_compat",
		EffectiveProviderType:  "openai_compat",
		EffectiveTransport:     "configured",
		ProviderAlias:          "my-provider",
		BackendModelID:         "model-a",
	}
	if _, known := store.CompareAndPromote(base, []string{"a"}); known {
		t.Fatal("first call reported a predecessor, want none")
	}

	for name, mutate := range map[string]func(CacheBaselineKey) CacheBaselineKey{
		"cache key":       func(k CacheBaselineKey) CacheBaselineKey { k.CacheKey = "other"; return k },
		"configured type": func(k CacheBaselineKey) CacheBaselineKey { k.ConfiguredProviderType = "anthropic"; return k },
		"effective type":  func(k CacheBaselineKey) CacheBaselineKey { k.EffectiveProviderType = "anthropic"; return k },
		"transport":       func(k CacheBaselineKey) CacheBaselineKey { k.EffectiveTransport = "anthropic"; return k },
		"provider alias":  func(k CacheBaselineKey) CacheBaselineKey { k.ProviderAlias = "other"; return k },
		"backend model":   func(k CacheBaselineKey) CacheBaselineKey { k.BackendModelID = "model-b"; return k },
	} {
		t.Run(name, func(t *testing.T) {
			if _, known := store.CompareAndPromote(mutate(base), []string{"a"}); known {
				t.Fatalf("a differing %s must not share the baseline", name)
			}
		})
	}

	if shared, known := store.CompareAndPromote(base, []string{"a"}); !known || shared != 1 {
		t.Fatalf("matching identity = (%d, %v), want (1, true)", shared, known)
	}
}

func TestCacheBaselineStore_AppendAndDivergence(t *testing.T) {
	store := NewCacheBaselineStore()
	key := CacheBaselineKey{CacheKey: "k"}

	if _, known := store.CompareAndPromote(key, []string{"a", "b"}); known {
		t.Fatal("first request reported a predecessor, want none")
	}
	if shared, known := store.CompareAndPromote(key, []string{"a", "b", "c"}); !known || shared != 2 {
		t.Fatalf("pure append = (%d, %v), want (2, true)", shared, known)
	}
	if shared, known := store.CompareAndPromote(key, []string{"a", "x", "c"}); !known || shared != 1 {
		t.Fatalf("rewrite at index 1 = (%d, %v), want (1, true)", shared, known)
	}
	if shared, known := store.CompareAndPromote(key, []string{"z"}); !known || shared != 0 {
		t.Fatalf("full divergence = (%d, %v), want (0, true)", shared, known)
	}
}

func TestCacheBaselineStore_EmptySequenceIsAKnownPredecessor(t *testing.T) {
	store := NewCacheBaselineStore()
	key := CacheBaselineKey{CacheKey: "k"}
	if _, known := store.CompareAndPromote(key, nil); known {
		t.Fatal("first request reported a predecessor, want none")
	}
	if shared, known := store.CompareAndPromote(key, nil); !known || shared != 0 {
		t.Fatalf("empty-sequence predecessor = (%d, %v), want (0, true)", shared, known)
	}
}

func TestCacheBaselineStore_ConcurrentUse(t *testing.T) {
	store := NewCacheBaselineStore()
	key := CacheBaselineKey{CacheKey: "k"}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				store.CompareAndPromote(key, []string{"a", "b"})
				store.CompareAndPromote(CacheBaselineKey{CacheKey: "other"}, []string{"c"})
			}
		}()
	}
	wg.Wait()

	if got := len(store.entries); got != 2 {
		t.Fatalf("entries = %d, want 2 distinct keys", got)
	}
	if shared, known := store.CompareAndPromote(key, []string{"a", "b"}); !known || shared != 2 {
		t.Fatalf("post-concurrency = (%d, %v), want (2, true)", shared, known)
	}
}
