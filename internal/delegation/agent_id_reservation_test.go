package delegation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// TestReserveDelegationAgentID verifies the reserver mints fresh IDs in call
// order for the spawning sub_agent tool and reserves nothing for follow_up,
// which resumes an existing agent under its original ID.
func TestReserveDelegationAgentID(t *testing.T) {
	resetAgentCounterForTesting()
	t.Cleanup(resetAgentCounterForTesting)

	if got := ReserveDelegationAgentID(SubAgentToolName); got != "child-1" {
		t.Fatalf("first reserve = %q, want child-1", got)
	}
	if got := ReserveDelegationAgentID(SubAgentToolName); got != "child-2" {
		t.Fatalf("second reserve = %q, want child-2", got)
	}
	if got := ReserveDelegationAgentID(FollowUpToolName); got != "" {
		t.Fatalf("follow_up reserve = %q, want empty", got)
	}
}

// TestSpecializedHandlerUsesReservedAgentID verifies a specialized child binds
// to the child ID reserved on its context, and falls back to generating one
// when the context carries no reservation.
func TestSpecializedHandlerUsesReservedAgentID(t *testing.T) {
	t.Run("reserved", func(t *testing.T) {
		var captured string
		deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			captured = req.AgentID
			return successRunState(), nil
		}})
		ctx := agent.WithDelegationAgentID(context.Background(), "child-42")
		if _, err := newSpecializedHandler(AgentTypeExplore, deps)(ctx, validStructuredTask("explore")); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if captured != "child-42" {
			t.Fatalf("AgentID = %q, want child-42", captured)
		}
	})
	t.Run("fallback", func(t *testing.T) {
		var captured string
		deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
			captured = req.AgentID
			return successRunState(), nil
		}})
		if _, err := newSpecializedHandler(AgentTypeExplore, deps)(context.Background(), validStructuredTask("explore")); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if captured == "" {
			t.Fatal("AgentID = empty, want a generated child ID")
		}
	})
}

// TestVisionHandlerUsesReservedAgentID is the vision analogue: the vision child
// binds to the child ID reserved on its context rather than minting a new one.
func TestVisionHandlerUsesReservedAgentID(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, []byte("fake-png-content"), 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 10, 20, 15)

	var captured string
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		captured = req.AgentID
		return successRunState(), nil
	}})
	deps.ImageStore = store
	deps.CacheKeyStore = NewCacheKeyStore()

	input := map[string]any{
		"objective":        "describe image",
		"context":          "background",
		"deliverable":      "description",
		"constraints":      []any{},
		"success_criteria": []any{},
		"checks":           []any{},
		"image_id":         ref.ID,
	}
	ctx := agent.WithDelegationAgentID(context.Background(), "child-9")
	if _, err := newVisionHandler(deps)(ctx, input); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if captured != "child-9" {
		t.Fatalf("AgentID = %q, want child-9", captured)
	}
}
