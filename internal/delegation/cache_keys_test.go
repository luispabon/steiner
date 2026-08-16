package delegation

import (
	"errors"
	"testing"
)

func TestCacheKeyStoreKeyForReusesSameAgentType(t *testing.T) {
	store := NewCacheKeyStore()
	n := 0
	mint := func() (string, error) {
		n++
		return "key-" + string(rune('a'+n-1)), nil
	}

	first, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	second, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	if first != second {
		t.Errorf("KeyFor() second call = %q, want reused %q", second, first)
	}
	if n != 1 {
		t.Errorf("mint called %d times, want 1 (only first call should mint)", n)
	}
}

func TestCacheKeyStoreKeyForDiffersByAgentType(t *testing.T) {
	store := NewCacheKeyStore()
	n := 0
	mint := func() (string, error) {
		n++
		return "key-" + string(rune('a'+n-1)), nil
	}

	codeKey, err := store.KeyFor(AgentTypeCode, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	reviewKey, err := store.KeyFor(AgentTypeReview, mint)
	if err != nil {
		t.Fatalf("KeyFor() error = %v", err)
	}
	if codeKey == reviewKey {
		t.Errorf("KeyFor() for different agent types returned the same key %q", codeKey)
	}
}

func TestCacheKeyStoreKeyForMintErrorNotCached(t *testing.T) {
	store := NewCacheKeyStore()
	wantErr := errors.New("mint failed")
	n := 0
	mint := func() (string, error) {
		n++
		if n == 1 {
			return "", wantErr
		}
		return "recovered-key", nil
	}

	_, err := store.KeyFor(AgentTypeExplore, mint)
	if !errors.Is(err, wantErr) {
		t.Fatalf("KeyFor() error = %v, want %v", err, wantErr)
	}

	key, err := store.KeyFor(AgentTypeExplore, mint)
	if err != nil {
		t.Fatalf("KeyFor() second call error = %v", err)
	}
	if key != "recovered-key" {
		t.Errorf("KeyFor() second call = %q, want %q (retry after error, not cached failure)", key, "recovered-key")
	}
	if n != 2 {
		t.Errorf("mint called %d times, want 2 (error must not be cached)", n)
	}
}
