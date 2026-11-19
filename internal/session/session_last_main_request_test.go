package session

import (
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/agent"
)

func lastMainRequestRecord(at time.Time) *LastMainRequest {
	return &LastMainRequest{At: at, ProviderAlias: "local", BackendModelID: "main-model", PromptCacheKey: "cache-key"}
}

// TestForkCopiesLastMainRequestIndependently proves a fork keeps its parent's
// last main request and owns a copy of it.
func TestForkCopiesLastMainRequestIndependently(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	parent, err := NewSession("test-model", agent.ConversationLineage{})
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	parent.LastMainRequest = lastMainRequestRecord(at)
	fork, err := Fork(parent)
	if err != nil {
		t.Fatalf("Fork() = %v", err)
	}
	if fork.LastMainRequest == nil || *fork.LastMainRequest != *parent.LastMainRequest {
		t.Fatalf("fork last main request = %+v, want %+v", fork.LastMainRequest, parent.LastMainRequest)
	}
	fork.LastMainRequest.BackendModelID = "fork-model"
	if parent.LastMainRequest.BackendModelID != "main-model" {
		t.Fatalf("fork aliases the parent's last main request: %+v", parent.LastMainRequest)
	}
}

// TestStoreRoundTripsLastMainRequest proves the additive record survives a
// save and load, and stays absent for sessions that never recorded one.
func TestStoreRoundTripsLastMainRequest(t *testing.T) {
	t.Parallel()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore() = %v", err)
	}
	at := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)

	withRequest, err := NewSession("test-model", agent.ConversationLineage{})
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	withRequest.LastMainRequest = lastMainRequestRecord(at)
	withoutRequest, err := NewSession("test-model", agent.ConversationLineage{})
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	if err := store.Save(withRequest); err != nil {
		t.Fatalf("Save(with request) = %v", err)
	}
	if err := store.Save(withoutRequest); err != nil {
		t.Fatalf("Save(without request) = %v", err)
	}

	loaded, err := store.Load(withRequest.ID)
	if err != nil {
		t.Fatalf("Load(with request) = %v", err)
	}
	if loaded.LastMainRequest == nil || *loaded.LastMainRequest != *withRequest.LastMainRequest {
		t.Fatalf("loaded last main request = %+v, want %+v", loaded.LastMainRequest, withRequest.LastMainRequest)
	}
	empty, err := store.Load(withoutRequest.ID)
	if err != nil {
		t.Fatalf("Load(without request) = %v", err)
	}
	if empty.LastMainRequest != nil {
		t.Fatalf("loaded last main request for a session without one = %+v, want nil", empty.LastMainRequest)
	}
}
