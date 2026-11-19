package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

// rejectingModelProvider answers every request as a provider that does not know
// the model, which is the shape a model rejection takes on the wire.
type rejectingModelProvider struct{}

func (rejectingModelProvider) ChatCompletion(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, rejectedModelHTTPError()
}

func (rejectingModelProvider) SupportsUsageStats() bool { return false }

func (rejectingModelProvider) StreamChatCompletion(context.Context, provider.ChatRequest) (<-chan provider.ChatChunk, error) {
	return nil, rejectedModelHTTPError()
}

func rejectedModelHTTPError() error {
	return &provider.HTTPError{StatusCode: 400, Body: `{"error":{"message":"invalid model test-model"}}`}
}

// TestModelRejectionGuidanceReachesSessionNotice drives a real agent run
// through the CLI runner with a provider that rejects the model. The agent
// runner emits the stop-reason event itself, so the guidance must arrive as a
// separate conversation notice rather than being lost behind the reported
// error.
func TestModelRejectionGuidanceReachesSessionNotice(t *testing.T) {
	t.Parallel()
	cfg := testRuntimeConfig("test-model")
	cfg.Models.Effective.CompactionFallback = "cheap-model"
	var (
		mu     sync.Mutex
		events []output.Event
	)
	sink := output.SinkFunc(func(event output.Event) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	rt := cliRuntime{
		cfg:      cfg,
		provider: rejectingModelProvider{},
		registry: tool.NewRegistry(),
		workDir:  t.TempDir(),
		homeDir:  t.TempDir(),
		events:   sink,
	}
	sess, err := interactive.NewSession(interactive.Dependencies{
		BaseEvents: sink,
		Config:     cfg,
		Runner:     sessionRunner{runner: cliRunner{runtime: rt, runMode: "interactive"}},
	})
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	t.Cleanup(func() { sess.Close(context.Background()) })

	ctx := context.Background()
	if err := sess.Handle(ctx, interactive.SubmitPrompt{Text: "hello"}); err != nil {
		t.Fatalf("Handle(SubmitPrompt) error = %v", err)
	}
	if !sess.WaitRuns(ctx) {
		t.Fatal("WaitRuns() did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	var notices []string
	var stops []output.StopReasonEvent
	for _, event := range events {
		switch payload := event.Payload.(type) {
		case output.ConfigWarningEvent:
			notices = append(notices, payload.Message)
		case output.StopReasonEvent:
			stops = append(stops, payload)
		}
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "Change it with /model") || !strings.Contains(notices[0], `"cheap-model"`) {
		t.Fatalf("conversation notices = %v, want model rejection guidance naming the fallback", notices)
	}
	if len(stops) != 1 || stops[0].Reason != string(agent.StopReasonError) || !strings.Contains(stops[0].Error, "invalid model test-model") {
		t.Fatalf("stop events = %#v, want one error stop carrying the provider message", stops)
	}
}
