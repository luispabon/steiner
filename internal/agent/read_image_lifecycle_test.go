package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestRunnerReadImageReachesCapableParentOnce(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"},
		}}}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	store := NewImageStore(t.TempDir())
	executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
		if name != "read" {
			t.Fatalf("tool = %q, want read", name)
		}
		return builtin.ReadResult{Image: &builtin.ImageBlock{
			FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data", Width: 2, Height: 3, SizeBytes: 4,
		}}, nil
	}}
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionCapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3}, VisionCapabilities: capabilities, ImageStore: store,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(providerStub.requests))
	}
	toolMsgs := toolMessages(providerStub.requests[1].Messages)
	if len(toolMsgs) != 1 || len(toolMsgs[0].Images) != 1 || toolMsgs[0].Images[0].Data != "read-image-data" {
		t.Fatalf("next request read image = %#v, want original image data", toolMsgs)
	}
	if toolMsgs[0].Images[0].ID != "img-1" || toolMsgs[0].Images[0].FilePath != "/tmp/image.png" {
		t.Fatalf("read image metadata = %#v, want registered ID and path", toolMsgs[0].Images[0])
	}
	finalTools := toolMessages(ToProviderMessages(state.Conversation))
	if len(finalTools) != 1 || len(finalTools[0].Images) != 0 {
		t.Fatalf("final conversation retained consumed image: %#v", finalTools)
	}
	if len(store.All()) != 1 {
		t.Fatalf("registered images = %d, want 1", len(store.All()))
	}
}

func TestRunnerReadImageRoutesIncapableParentOnce(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"},
		}}}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	store := NewImageStore(t.TempDir())
	visionCalls := 0
	executor := &fakeExecutor{execute: func(_ context.Context, name string, input map[string]any) (any, error) {
		switch name {
		case "read":
			return builtin.ReadResult{Image: &builtin.ImageBlock{FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data"}}, nil
		case "sub_agent":
			visionCalls++
			if input["type"] != "vision" {
				t.Fatalf("type = %v, want vision", input["type"])
			}
			visionContext, ok := input["context"].(string)
			if !ok || !contains(visionContext, "inspect image.png") {
				t.Fatalf("context = %q, want parent user request", visionContext)
			}
			if contains(visionContext, `{"path":"image.png"}`) {
				t.Fatal("context used read tool JSON instead of parent user request")
			}
			if input["image_id"] != "img-1" {
				t.Fatalf("image_id = %v, want img-1", input["image_id"])
			}
			data, _ := json.Marshal(map[string]string{"agent_id": "vision-1", "output": "a detailed description"})
			return string(data), nil
		default:
			t.Fatalf("unexpected tool %q", name)
			return nil, nil
		}
	}}
	capabilities := NewVisionCapabilities(true)
	capabilities.SetDerived("parent", VisionIncapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3}, VisionCapabilities: capabilities, ImageStore: store,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if visionCalls != 1 {
		t.Fatalf("vision calls = %d, want 1", visionCalls)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(providerStub.requests))
	}
	if requestHasImages(providerStub.requests[1].Messages) {
		t.Fatal("incapable parent request contains raw image data")
	}
	if !messageContentsContain(providerStub.requests[1].Messages, "a detailed description") {
		t.Fatal("incapable parent request lacks vision description")
	}
	if requestHasImages(ToProviderMessages(state.Conversation)) {
		t.Fatal("final conversation contains raw image data")
	}
}

func TestRunnerReadImageNoVisionSubAgentStripsSafely(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, ToolCalls: []provider.ToolCall{{
			ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"},
		}}}},
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
		if name != "read" {
			t.Fatalf("tool = %q, want read", name)
		}
		return builtin.ReadResult{Image: &builtin.ImageBlock{FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data"}}, nil
	}}
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionIncapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3}, VisionCapabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(providerStub.requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(providerStub.requests))
	}
	if requestHasImages(providerStub.requests[1].Messages) {
		t.Fatal("no-sub-agent request contains raw image data")
	}
	if !messageContentsContain(providerStub.requests[1].Messages, "could not be processed") {
		t.Fatal("no-sub-agent request lacks safe image placeholder")
	}
	if requestHasImages(ToProviderMessages(state.Conversation)) {
		t.Fatal("final conversation contains raw image data")
	}
}

func TestRunnerReadImageScrubsOnModelError(t *testing.T) {
	providerStub := &fakeProvider{}
	providerStub.chatFn = func(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
		if len(providerStub.requests) == 1 {
			return provider.ChatResponse{Message: provider.Message{
				Role:      provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"}}},
			}}, nil
		}
		return provider.ChatResponse{}, errors.New("model failed")
	}
	executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
		if name != "read" {
			t.Fatalf("tool = %q, want read", name)
		}
		return builtin.ReadResult{Image: &builtin.ImageBlock{
			FilePath: "/tmp/image.png", MediaType: "image/png", Data: "raw-read-image",
		}}, nil
	}}
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionCapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3}, VisionCapabilities: capabilities,
	})
	if err == nil || err.Error() != "model failed" {
		t.Fatalf("Run() error = %v, want model failed", err)
	}
	if requestHasImages(ToProviderMessages(state.Conversation)) {
		t.Fatal("final conversation contains raw read image data after model error")
	}
}

func TestRunnerReadImageScrubsAtMaxTurns(t *testing.T) {
	providerStub := &fakeProvider{responses: []provider.ChatResponse{{Message: provider.Message{
		Role:      provider.MessageRoleAssistant,
		ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"}}},
	}}}}
	executor := &fakeExecutor{execute: func(_ context.Context, name string, _ map[string]any) (any, error) {
		if name != "read" {
			t.Fatalf("tool = %q, want read", name)
		}
		return builtin.ReadResult{Image: &builtin.ImageBlock{
			FilePath: "/tmp/image.png", MediaType: "image/png", Data: "raw-read-image",
		}}, nil
	}}
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionCapable)

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 1}, VisionCapabilities: capabilities,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if state.StopReason != StopReasonMaxTurns {
		t.Fatalf("StopReason = %q, want %q", state.StopReason, StopReasonMaxTurns)
	}
	if requestHasImages(ToProviderMessages(state.Conversation)) {
		t.Fatal("final conversation contains raw read image data at max turns")
	}
}
func TestRunnerReadImageRetainedAcrossVisionCapabilityRetry(t *testing.T) {
	providerStub := &fakeProvider{}
	hasRawImageData := func(messages []provider.Message) bool {
		for _, message := range messages {
			for _, image := range message.Images {
				if image.Data != "" {
					return true
				}
			}
		}
		return false
	}
	rawByCall := make([]bool, 0, 3)
	chatCalls := 0
	providerStub.chatFn = func(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
		chatCalls++
		rawByCall = append(rawByCall, hasRawImageData(req.Messages))
		switch chatCalls {
		case 1:
			return provider.ChatResponse{Message: provider.Message{
				Role:      provider.MessageRoleAssistant,
				ToolCalls: []provider.ToolCall{{ID: "read-1", Name: "read", Arguments: map[string]any{"path": "image.png"}}},
			}}, nil
		case 2:
			return provider.ChatResponse{}, &provider.HTTPError{StatusCode: 400, Status: "400 Bad Request", Body: "unknown variant image_url"}
		case 3:
			return provider.ChatResponse{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}}, nil
		default:
			t.Fatalf("unexpected ChatCompletion call %d", chatCalls)
			return provider.ChatResponse{}, nil
		}
	}

	visionCalls := 0
	executor := &fakeExecutor{execute: func(_ context.Context, name string, input map[string]any) (any, error) {
		switch name {
		case "read":
			return builtin.ReadResult{Image: &builtin.ImageBlock{
				FilePath: "/tmp/image.png", MediaType: "image/png", Data: "read-image-data", Width: 2, Height: 3, SizeBytes: 4,
			}}, nil
		case "sub_agent":
			visionCalls++
			if input["type"] != "vision" {
				t.Fatalf("type = %v, want vision", input["type"])
			}
			if input["image_id"] != "img-1" {
				t.Fatalf("image_id = %v, want img-1", input["image_id"])
			}
			data, _ := json.Marshal(map[string]string{"agent_id": "vision-1", "output": "a detailed description"})
			return string(data), nil
		default:
			t.Fatalf("unexpected tool %q", name)
			return nil, nil
		}
	}}
	capabilities := NewVisionCapabilities(true)
	store := NewImageStore(t.TempDir())

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub, Executor: executor,
		Prompt:        prompt.AssemblyOptions{Conversation: []provider.Message{{Role: provider.MessageRoleUser, Content: "inspect image.png"}}},
		ResolvedModel: provider.ResolvedModel{Alias: "parent", BackendModelID: "parent"},
		Limits:        Limits{MaxTurns: 3}, VisionCapabilities: capabilities, ImageStore: store, StreamingPreferred: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if chatCalls != 3 {
		t.Fatalf("ChatCompletion calls = %d, want 3", chatCalls)
	}
	if len(rawByCall) != 3 || !rawByCall[1] || rawByCall[2] {
		t.Fatalf("raw image data by request = %v, want [false true false]", rawByCall)
	}
	if visionCalls != 1 {
		t.Fatalf("vision calls = %d, want 1", visionCalls)
	}
	if hasRawImageData(ToProviderMessages(state.Conversation)) {
		t.Fatal("final conversation retained consumed image data")
	}
}

func TestHandleImagesForVisionDoesNotProcessAssistantImages(t *testing.T) {
	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("parent", VisionIncapable)
	image := ImageBlock{ID: "img-1", Data: "assistant-image"}
	state := RunState{Conversation: []Message{{Role: MessageRoleAssistant, Images: []ImageBlock{image}}}, Lineage: newConversationLineage([]Message{{Role: MessageRoleAssistant, Images: []ImageBlock{image}}})}
	p := newTurnProgressor(RunRequest{ResolvedModel: provider.ResolvedModel{Alias: "parent"}, VisionCapabilities: capabilities, Executor: &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
		t.Fatal("assistant image was routed")
		return nil, nil
	}}}, prompt.AssemblyOptions{}, nil)
	if p.handleImagesForVision(context.Background(), &state) {
		t.Fatal("assistant image changed state")
	}
	if state.Conversation[0].Images[0].Data != "assistant-image" {
		t.Fatal("assistant image was modified")
	}
}
