package delegation

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
)

func TestVisionHandler_DispatchGateLeaderWrapsEvents(t *testing.T) {
	dir := t.TempDir()
	imgPath := filepath.Join(dir, "test.png")
	if err := os.WriteFile(imgPath, []byte("fake-png-content"), 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	store := agent.NewImageStore(dir)
	ref := store.Register(imgPath, "image/png", 10, 20, 15)

	var capturedReq agent.RunRequest
	var runCount int
	events := &recordingEventSink{}
	deps := minimalDeps(&mockRunner{runFunc: func(_ context.Context, req agent.RunRequest) (agent.RunState, error) {
		runCount++
		if runCount == 1 {
			capturedReq = req
			if _, ok := req.Events.(*dispatchReleaseSink); !ok {
				t.Errorf("req.Events=%T, want *dispatchReleaseSink", req.Events)
			}
			req.Events.Emit(output.NewThinkingChunkEventWithSource(1, "thinking", output.ChunkSourceAssistant))
		}
		return agent.RunState{}, nil
	}})
	deps.Events = events
	deps.ImageStore = store
	deps.CacheKeyStore = NewCacheKeyStore()

	handler := newVisionHandler(deps)
	input := map[string]any{
		"objective":        "describe image",
		"context":          "background",
		"deliverable":      "description",
		"constraints":      []any{},
		"success_criteria": []any{},
		"checks":           []any{},
		"image_id":         ref.ID,
	}
	if _, err := handler(context.Background(), input); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if capturedReq.Events == nil {
		t.Fatal("runner did not capture req.Events")
	}
	if got := waitingEvents(events.Events()); len(got) != 0 {
		t.Fatalf("leader emitted %d waiting events, want none", len(got))
	}
	started := startedEvents(events.Events())
	if len(started) != 1 {
		t.Fatalf("started events = %d, want 1", len(started))
	}
	payload, ok := started[0].Payload.(output.DelegationStartedEvent)
	if !ok {
		t.Fatalf("started payload = %T, want DelegationStartedEvent", started[0].Payload)
	}
	if payload.AgentType != string(AgentTypeVision) || started[0].Scope.AgentID == "" || started[0].Scope.AgentType != string(AgentTypeVision) {
		t.Errorf("started type/scope = %q/%+v, want %q and agent scope", payload.AgentType, started[0].Scope, AgentTypeVision)
	}
}
