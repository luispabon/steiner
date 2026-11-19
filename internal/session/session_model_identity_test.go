package session

import (
	"reflect"
	"testing"
)

func TestForkCopiesModelIdentityAndReasoningOverrides(t *testing.T) {
	t.Parallel()
	original := Session{
		Model:              "backend-id",
		ModelRef:           "large",
		ModelProvider:      "local",
		ReasoningOverrides: map[string]ReasoningOverride{"large": {Kind: "effort", Effort: "high"}},
	}
	fork, err := Fork(original)
	if err != nil {
		t.Fatalf("Fork() error = %v", err)
	}
	if fork.Model != "backend-id" || fork.ModelRef != "large" || fork.ModelProvider != "local" {
		t.Fatalf("fork model identity = (%q, %q, %q), want (backend-id, large, local)", fork.Model, fork.ModelRef, fork.ModelProvider)
	}
	if !reflect.DeepEqual(fork.ReasoningOverrides, original.ReasoningOverrides) {
		t.Fatalf("fork reasoning overrides = %v, want %v", fork.ReasoningOverrides, original.ReasoningOverrides)
	}
	fork.ReasoningOverrides["large"] = ReasoningOverride{Kind: "provider_default"}
	if original.ReasoningOverrides["large"].Kind != "effort" {
		t.Fatalf("mutating the fork changed the original override: %v", original.ReasoningOverrides)
	}
}
