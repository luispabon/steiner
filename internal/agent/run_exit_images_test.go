package agent

import (
	"context"
	"errors"
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

// TestRunnerRunExitStripsImagesOnValidationFailure covers the earliest Run exit
// path: request validation rejects the run before any turn executes, yet Run
// still strips image payloads and leaves a placeholder behind.
func TestRunnerRunExitStripsImagesOnValidationFailure(t *testing.T) {
	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: &fakeProvider{},
		Prompt: prompt.AssemblyOptions{Conversation: []provider.Message{{
			Role:    provider.MessageRoleUser,
			Content: "look",
			Images: []provider.ImageBlock{
				{ID: "img-1", FilePath: "/tmp/img-1.png", MediaType: "image/png", Data: "user-image-data"},
			},
		}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3},
	})
	if err == nil || err.Error() != "tool executor is required" {
		t.Fatalf("Run() error = %v, want %q", err, "tool executor is required")
	}
	if state.StopReason != StopReasonError {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonError)
	}
	if stateHasImageData(state) {
		t.Fatalf("state retained image data: %#v", state)
	}
	if len(state.Conversation) == 0 || !strings.Contains(state.Conversation[0].Content, "[image img-1:") {
		t.Fatalf("state conversation missing placeholder: %#v", state.Conversation)
	}
}

// TestRunnerRunExitsStripDeferredReadImages drives every named Runner.Run exit
// end-to-end through a read-image tool turn and asserts the returned state never
// carries image payloads and always leaves an image placeholder behind.
func TestRunnerRunExitsStripDeferredReadImages(t *testing.T) {
	origSleep := runnerRetrySleepFn
	runnerRetrySleepFn = func(_ context.Context, _ time.Duration) error { return nil }
	defer func() { runnerRetrySleepFn = origSleep }()

	readTurn := func() provider.ChatResponse {
		return provider.ChatResponse{Message: provider.Message{
			Role:      provider.MessageRoleAssistant,
			ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"}}},
		}}
	}
	doneTurn := provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}}

	newRequest := func(t *testing.T, p provider.Provider, limits Limits) RunRequest {
		t.Helper()
		capabilities := NewVisionCapabilities(false)
		capabilities.SetDerived("parent", VisionCapable)
		executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
			if name != "read" {
				t.Fatalf("tool = %q, want read", name)
			}
			return builtin.ReadResult{Image: &builtin.ImageBlock{
				FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data", Width: 2, Height: 3, SizeBytes: 4,
			}}, nil
		}}
		return RunRequest{
			Provider: p, Executor: executor,
			Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
			ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
			Limits:        limits, VisionCapabilities: capabilities, ImageStore: NewImageStore(t.TempDir()),
		}
	}

	tests := []struct {
		name     string
		run      func(t *testing.T) (RunState, error)
		wantStop StopReason
		wantErr  string
	}{
		{
			name: "non-retryable provider error",
			run: func(t *testing.T) (RunState, error) {
				p := &fakeProvider{}
				calls := 0
				p.chatFn = func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
					calls++
					if calls == 1 {
						return readTurn(), nil
					}
					return provider.ChatResponse{}, errors.New("model failed")
				}
				return NewRunner().Run(context.Background(), newRequest(t, p, Limits{MaxTurns: 5}))
			},
			wantStop: StopReasonError,
			wantErr:  "model failed",
		},
		{
			name: "cancellation during model call",
			run: func(t *testing.T) (RunState, error) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				p := &fakeProvider{}
				calls := 0
				p.chatFn = func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
					calls++
					if calls == 1 {
						return readTurn(), nil
					}
					cancel()
					return provider.ChatResponse{}, ctx.Err()
				}
				return NewRunner().Run(ctx, newRequest(t, p, Limits{MaxTurns: 5}))
			},
			wantStop: StopReasonCancelled,
		},
		{
			name: "max turns immediately after read-image tool turn",
			run: func(t *testing.T) (RunState, error) {
				p := &fakeProvider{responses: []provider.ChatResponse{readTurn(), doneTurn}}
				return NewRunner().Run(context.Background(), newRequest(t, p, Limits{MaxTurns: 1}))
			},
			wantStop: StopReasonMaxTurns,
		},
		{
			name: "max tokens after read-image tool turn",
			run: func(t *testing.T) (RunState, error) {
				readWithUsage := readTurn()
				readWithUsage.Usage = &provider.UsageStats{CompletionTokens: 10}
				p := &fakeProvider{responses: []provider.ChatResponse{readWithUsage, doneTurn}}
				return NewRunner().Run(context.Background(), newRequest(t, p, Limits{MaxTurns: 5, MaxTokens: 1}))
			},
			wantStop: StopReasonMaxTokens,
		},
		{
			name: "normal completion",
			run: func(t *testing.T) (RunState, error) {
				p := &fakeProvider{responses: []provider.ChatResponse{readTurn(), doneTurn}}
				return NewRunner().Run(context.Background(), newRequest(t, p, Limits{MaxTurns: 5}))
			},
			wantStop: StopReasonComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := tt.run(t)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Run() error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			if state.StopReason != tt.wantStop {
				t.Fatalf("StopReason = %q, want %q", state.StopReason, tt.wantStop)
			}
			if stateHasImageData(state) {
				t.Fatalf("state retained image data: %#v", state)
			}
			if !messagesContainImagePlaceholder(state.Conversation) {
				t.Fatalf("state conversation missing image placeholder: %#v", state.Conversation)
			}
		})
	}
}

// TestRunnerRunExitStripsPastedImages covers the two remaining exit paths for a
// pasted (user-attached) image rather than a deferred read image: a
// non-retryable provider error and cancellation during the model call. Both
// must leave the returned state without image bytes and with a placeholder.
func TestRunnerRunExitStripsPastedImages(t *testing.T) {
	newRequest := func(p provider.Provider) RunRequest {
		return RunRequest{
			Provider: p, Executor: &fakeExecutor{},
			SourceConversation: []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{
				{ID: "img-1", FilePath: "/tmp/img-1.png", MediaType: "image/png", Data: "user-image-data"},
			}}},
			ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
			Limits:        Limits{MaxTurns: 5},
		}
	}

	tests := []struct {
		name     string
		run      func(t *testing.T) (RunState, error)
		wantStop StopReason
		wantErr  string
	}{
		{
			name: "non-retryable provider error",
			run: func(t *testing.T) (RunState, error) {
				p := &fakeProvider{}
				sentRaw := false
				p.chatFn = func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
					sentRaw = requestHasImages(req.Messages)
					return provider.ChatResponse{}, errors.New("model failed")
				}
				state, err := NewRunner().Run(context.Background(), newRequest(p))
				if !sentRaw {
					t.Error("pasted image data was not sent to the provider")
				}
				return state, err
			},
			wantStop: StopReasonError,
			wantErr:  "model failed",
		},
		{
			name: "cancellation during model call",
			run: func(_ *testing.T) (RunState, error) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				p := &fakeProvider{}
				p.chatFn = func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
					cancel()
					return provider.ChatResponse{}, ctx.Err()
				}
				return NewRunner().Run(ctx, newRequest(p))
			},
			wantStop: StopReasonCancelled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, err := tt.run(t)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Run() error = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Run() error = %v, want nil", err)
			}
			if state.StopReason != tt.wantStop {
				t.Fatalf("StopReason = %q, want %q", state.StopReason, tt.wantStop)
			}
			if stateHasImageData(state) {
				t.Fatalf("state retained image data: %#v", state)
			}
			if !messagesContainImagePlaceholder(state.Conversation) {
				t.Fatalf("state conversation missing image placeholder: %#v", state.Conversation)
			}
		})
	}
}

func messagesContainImagePlaceholder(messages []Message) bool {
	for _, message := range messages {
		if strings.Contains(message.Content, "[image img-1:") {
			return true
		}
	}
	return false
}
