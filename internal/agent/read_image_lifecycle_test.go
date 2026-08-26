package agent

import (
	"context"
	"encoding/json"
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
		case "vision":
			visionCalls++
			task, ok := input["task"].(string)
			if !ok || !contains(task, "inspect image.png") {
				t.Fatalf("vision task = %q, want parent user request", task)
			}
			if contains(task, `{"path":"image.png"}`) {
				t.Fatal("vision task used read tool JSON instead of parent user request")
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
