package provider

import (
	"regexp"
	"testing"
)

func TestNewPromptCacheKey(t *testing.T) {
	hex32 := regexp.MustCompile(`^[0-9a-f]{32}$`)

	seen := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		key, err := NewPromptCacheKey()
		if err != nil {
			t.Fatalf("NewPromptCacheKey() error = %v", err)
		}
		if !hex32.MatchString(key) {
			t.Fatalf("NewPromptCacheKey() = %q, want 32 lowercase hex chars", key)
		}
		if _, dup := seen[key]; dup {
			t.Fatalf("NewPromptCacheKey() returned duplicate key %q", key)
		}
		seen[key] = struct{}{}
	}
}
