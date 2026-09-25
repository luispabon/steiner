package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// stateHasImageData reports whether any message in the state's conversation or
// lineage still carries a non-empty image payload.
func stateHasImageData(state RunState) bool {
	if messagesHaveImageData(state.Conversation) {
		return true
	}
	for _, generation := range state.Lineage.Generations {
		if messagesHaveImageData(generation.SummaryPrefix) || messagesHaveImageData(generation.Messages) {
			return true
		}
	}
	return false
}

func messagesHaveImageData(messages []Message) bool {
	for _, message := range messages {
		for _, image := range message.Images {
			if image.Data != "" {
				return true
			}
		}
	}
	return false
}

func incapableWithSubAgent(alias string) *VisionCapabilities {
	capabilities := NewVisionCapabilities(true)
	capabilities.SetDerived(alias, VisionIncapable)
	return capabilities
}

func TestFinalizeImagesAtRunExitForRequest(t *testing.T) {
	image := func(id string) ImageBlock {
		return ImageBlock{ID: id, FilePath: "/tmp/" + id + ".png", MediaType: "image/png", Data: "bytes-" + id}
	}
	prefix := []Message{{Role: MessageRoleUser, Content: "prior summary"}}

	tests := []struct {
		name   string
		req    RunRequest
		state  RunState
		verify func(t *testing.T, got RunState)
	}{
		{
			name: "empty lineage strips raw conversation",
			state: RunState{Conversation: []Message{
				{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{image("img-1")}},
			}},
			verify: func(t *testing.T, got RunState) {
				t.Helper()
				if stateHasImageData(got) {
					t.Fatalf("image data survived: %#v", got)
				}
				if len(got.Lineage.Generations) != 0 {
					t.Fatalf("empty lineage was populated: %#v", got.Lineage)
				}
				if !strings.Contains(got.Conversation[0].Content, "[image img-1:") {
					t.Fatalf("placeholder missing: %q", got.Conversation[0].Content)
				}
			},
		},
		{
			name: "lineage strips current generation and rebuilds conversation",
			state: RunState{
				Conversation: []Message{{Role: MessageRoleUser, Content: "stale"}},
				Lineage: ConversationLineage{
					Generations: []ConversationGeneration{{
						ID:            2,
						SummaryPrefix: prefix,
						Messages:      []Message{{Role: MessageRoleTool, Name: "read", Content: "read result", Images: []ImageBlock{image("img-2")}}},
					}},
					NextGenerationID: 3,
				},
			},
			req: RunRequest{
				ResolvedModel:      provider.ResolvedModel{Alias: "parent"},
				VisionCapabilities: incapableWithSubAgent("parent"),
			},
			verify: func(t *testing.T, got RunState) {
				t.Helper()
				if stateHasImageData(got) {
					t.Fatalf("image data survived: %#v", got)
				}
				if !reflect.DeepEqual(got.Lineage.Generations[0].SummaryPrefix, prefix) {
					t.Fatalf("summary prefix mutated: %#v", got.Lineage.Generations[0].SummaryPrefix)
				}
				if !reflect.DeepEqual(got.Conversation, got.Lineage.FullMessages()) {
					t.Fatalf("conversation not rebuilt from lineage: %#v", got.Conversation)
				}
				if !strings.Contains(got.Conversation[1].Content, "use follow_up") {
					t.Fatalf("capability-specific placeholder missing: %q", got.Conversation[1].Content)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finalizeImagesAtRunExitForRequest(tt.req, tt.state)
			tt.verify(t, got)
		})
	}
}

func TestRunnerRunExitStripsImageAfterTransientRetry(t *testing.T) {
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	providerStub := &fakeProvider{}
	rawByCall := make([]bool, 0, 3)
	calls := 0
	providerStub.chatFn = func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
		calls++
		rawByCall = append(rawByCall, requestHasImages(req.Messages))
		switch calls {
		case 1:
			return provider.ChatResponse{Message: provider.Message{
				Role:      provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"}}},
			}}, nil
		case 2:
			return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 503, Status: "503 Service Unavailable"}
		case 3:
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}}, nil
		default:
			t.Fatalf("unexpected ChatCompletion call %d", calls)
			return provider.ChatResponse{}, nil
		}
	}
	executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
		if name != "read" {
			t.Fatalf("tool = %q, want read", name)
		}
		return builtin.ReadResult{Image: &builtin.ImageBlock{
			FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data",
		}}, nil
	}}
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionCapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 5}, VisionCapabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if calls != 3 {
		t.Fatalf("ChatCompletion calls = %d, want 3", calls)
	}
	if len(rawByCall) != 3 || rawByCall[0] || !rawByCall[1] || !rawByCall[2] {
		t.Fatalf("raw image data by request = %v, want [false true true]", rawByCall)
	}
	if stateHasImageData(state) {
		t.Fatalf("final state retained image data: %#v", state)
	}
}

func TestRunnerRunExitStripsImagesWhenCancelledBeforeTurn(t *testing.T) {
	conversation := []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{
		{ID: "img-1", FilePath: "/tmp/a.png", MediaType: "image/png", Data: "user-image-data"},
	}}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state, err := NewRunner().Run(ctx, RunRequest{
		Provider: &fakeProvider{}, Executor: &fakeExecutor{},
		SourceConversation: conversation,
		ResolvedModel:      provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:             Limits{MaxTurns: 3},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if state.StopReason != StopReasonCancelled {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonCancelled)
	}
	if stateHasImageData(state) {
		t.Fatalf("cancelled state retained image data: %#v", state)
	}
	if !strings.Contains(state.Conversation[0].Content, "[image img-1:") {
		t.Fatalf("cancelled conversation missing placeholder: %q", state.Conversation[0].Content)
	}
}
