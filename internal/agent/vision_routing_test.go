package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
)

type visionProjectionTestResult struct{}

func (visionProjectionTestResult) ProjectToolResult() DelegationResultEnvelope {
	return DelegationResultEnvelope{
		Output:       "This is a screenshot showing a button labeled 'OK' on the left side.",
		Continuation: &DelegationContinuation{AgentID: "vision-sub-1"},
	}
}

func TestHandleImagesForVision_NoCapabilityTracking(t *testing.T) {
	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		}),
	}

	req := RunRequest{
		ResolvedModel: provider.ResolvedModel{BackendModelID: "test-model"},
		Executor:      &fakeExecutor{},
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if mutated {
		t.Fatalf("mutated = true, want false (no capability tracking)")
	}
	if len(state.Conversation[0].Images) != 1 {
		t.Fatalf("images were modified when they should not be")
	}
}

func TestHandleImagesForVision_CapableModel(t *testing.T) {
	vc := NewVisionCapabilities(false)
	vc.SetDerived("test-model", VisionCapable)

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		}),
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           &fakeExecutor{},
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if mutated {
		t.Fatalf("mutated = true, want false (capable model)")
	}
	if state.Conversation[0].Images[0].Data != "base64data" {
		t.Fatalf("image data was modified when it should not be")
	}
}

func TestHandleImagesForVision_NoImages(t *testing.T) {
	vc := NewVisionCapabilities(false)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "hello world",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "", // Already stripped
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "hello world",
				Images: []ImageBlock{
					{
						ID:       "img-1",
						FilePath: "/path/to/img.png",
						Data:     "", // Already stripped
					},
				},
			},
		}),
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           &fakeExecutor{},
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if mutated {
		t.Fatalf("mutated = true, want false (no pasted images)")
	}
}

func TestHandleImagesForVision_WithSubAgent_RoutesSuccessfully(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, input map[string]any) (any, error) {
			if toolName != "vision" {
				t.Fatalf("unexpected tool: %s", toolName)
			}
			if input["image_id"] != "img-1" {
				t.Fatalf("unexpected image_id: %v", input["image_id"])
			}
			return tool.ExecutionResult{Value: visionProjectionTestResult{}}, nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if state.Conversation[0].Images != nil {
		t.Fatalf("Images should be nil after routing, got %v", state.Conversation[0].Images)
	}

	content := state.Conversation[0].Content
	if !contains(content, "vision-sub-1") {
		t.Fatalf("content should contain continuation agent_id, got: %s", content)
	}
	if !contains(content, "screenshot showing a button") {
		t.Fatalf("content should contain image description, got: %s", content)
	}

	// Verify lineage was also updated.
	lineageMsgs := state.Lineage.FullMessages()
	if lineageMsgs[0].Images != nil {
		t.Fatalf("Lineage Images should be nil after routing")
	}
}

func TestHandleImagesForVision_ExecutorError_FallsBackToStripping(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "examine image 1 and image 2",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "examine image 1 and image 2",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		}),
	}

	callCount := 0
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, input map[string]any) (any, error) {
			callCount++
			imageID := input["image_id"].(string)
			if imageID == "img-1" {
				return nil, fmt.Errorf("vision call failed for img-1")
			}
			// img-2 succeeds
			result := map[string]any{
				"continuation": map[string]any{"agent_id": "vision-sub-1"},
				"output":       "Image 2 shows some text",
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if callCount != 2 {
		t.Fatalf("expected 2 executor calls, got %d", callCount)
	}

	content := state.Conversation[0].Content
	if !contains(content, "could not be processed") {
		t.Fatalf("content should indicate img-1 could not be processed, got: %s", content)
	}
	if !contains(content, "Image 2 shows some text") {
		t.Fatalf("content should contain successful img-2 description, got: %s", content)
	}
}

func TestHandleImagesForVision_ImageWithoutID_SkipsRouting(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "", // No ID, so won't be routed
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "examine this image",
				Images: []ImageBlock{
					{
						ID:       "", // No ID
						FilePath: "/path/to/img.png",
						Data:     "base64data",
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if len(executor.calls) != 0 {
		t.Fatalf("executor should not have been called for image without ID")
	}

	content := state.Conversation[0].Content
	if !contains(content, "could not be processed") {
		t.Fatalf("content should indicate fallback stripping, got: %s", content)
	}
}

func TestHandleImagesForVision_NoSubAgent_StripDefensively(t *testing.T) {
	vc := NewVisionCapabilities(false) // No sub-agent
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "look at this",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "look at this",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if len(executor.calls) != 0 {
		t.Fatalf("executor should not have been called when no sub-agent is configured")
	}

	if state.Conversation[0].Images != nil {
		t.Fatalf("Images should be nil after stripping")
	}

	content := state.Conversation[0].Content
	if !contains(content, "could not be processed") {
		t.Fatalf("content should indicate defensive stripping, got: %s", content)
	}
}

func TestHandleImagesForVision_MultipleMessages(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "first message",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
				},
			},
			{
				Role:    MessageRoleAssistant,
				Content: "i understand",
			},
			{
				Role:    MessageRoleUser,
				Content: "second message with image",
				Images: []ImageBlock{
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "first message",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
				},
			},
			{
				Role:    MessageRoleAssistant,
				Content: "i understand",
			},
			{
				Role:    MessageRoleUser,
				Content: "second message with image",
				Images: []ImageBlock{
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, input map[string]any) (any, error) {
			imageID := input["image_id"].(string)
			result := map[string]any{
				"continuation": map[string]any{"agent_id": "vision-" + imageID},
				"output":       "description for " + imageID,
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if len(executor.calls) != 2 {
		t.Fatalf("expected 2 executor calls, got %d", len(executor.calls))
	}

	// Check that the assistant message was not modified.
	if state.Conversation[1].Role != MessageRoleAssistant {
		t.Fatalf("assistant message was modified")
	}
	if state.Conversation[1].Images != nil {
		t.Fatalf("assistant message should not have images")
	}
}

func contains(text, substring string) bool {
	for i := 0; i <= len(text)-len(substring); i++ {
		if text[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}

func TestRouteImageToVision_EmitsDiagnosticEvent(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	var capturedEvents []output.Event
	eventSink := output.SinkFunc(func(event output.Event) {
		capturedEvents = append(capturedEvents, event)
	})

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			if toolName != "vision" {
				t.Fatalf("unexpected tool: %s", toolName)
			}
			result := map[string]any{
				"continuation": map[string]any{"agent_id": "vision-sub-1"},
				"output":       "This is a button labeled OK",
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
		Events:             eventSink,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	// Find the vision_routed diagnostic event
	var foundEvent *output.ProviderDiagnosticEvent
	for _, event := range capturedEvents {
		if payload, ok := event.Payload.(output.ProviderDiagnosticEvent); ok {
			if payload.Kind == "vision_routed" {
				foundEvent = &payload
				break
			}
		}
	}

	if foundEvent == nil {
		t.Fatalf("no vision_routed diagnostic event found. captured events: %v", capturedEvents)
	}

	if foundEvent.Severity != "info" {
		t.Fatalf("event.Severity = %q, want info", foundEvent.Severity)
	}

	if !contains(foundEvent.Message, "img-1") {
		t.Fatalf("message should contain image ID, got: %s", foundEvent.Message)
	}

	if !contains(foundEvent.Message, "This is a button labeled OK") {
		t.Fatalf("message should contain vision output, got: %s", foundEvent.Message)
	}
}

func TestRouteImageToVision_EmitsToolCallEvents(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	var capturedEvents []output.Event
	eventSink := output.SinkFunc(func(event output.Event) {
		capturedEvents = append(capturedEvents, event)
	})

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
						Width:     800,
						Height:    600,
						SizeBytes: 10000,
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, toolName string, _ map[string]any) (any, error) {
			if toolName != "vision" {
				t.Fatalf("unexpected tool: %s", toolName)
			}
			result := map[string]any{
				"continuation": map[string]any{"agent_id": "vision-sub-1"},
				"output":       "This is a button labeled OK",
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
		Events:             eventSink,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	// Find the ToolCallStartedEvent
	var startedEvent *output.ToolCallStartedEvent
	var finishedEvent *output.ToolCallFinishedEvent
	for _, event := range capturedEvents {
		switch payload := event.Payload.(type) {
		case output.ToolCallStartedEvent:
			if payload.Tool == "vision" {
				startedEvent = &payload
			}
		case output.ToolCallFinishedEvent:
			if payload.Tool == "vision" {
				finishedEvent = &payload
			}
		}
	}

	if startedEvent == nil {
		t.Fatalf("no ToolCallStartedEvent for vision found. captured events: %v", capturedEvents)
	}
	if startedEvent.Tool != "vision" {
		t.Fatalf("startedEvent.Tool = %q, want vision", startedEvent.Tool)
	}
	if startedEvent.CallID != "vision-auto-img-1" {
		t.Fatalf("startedEvent.CallID = %q, want vision-auto-img-1", startedEvent.CallID)
	}

	if finishedEvent == nil {
		t.Fatalf("no ToolCallFinishedEvent for vision found. captured events: %v", capturedEvents)
	}
	if finishedEvent.Tool != "vision" {
		t.Fatalf("finishedEvent.Tool = %q, want vision", finishedEvent.Tool)
	}
	if finishedEvent.CallID != "vision-auto-img-1" {
		t.Fatalf("finishedEvent.CallID = %q, want vision-auto-img-1", finishedEvent.CallID)
	}
	if finishedEvent.Error != "" {
		t.Fatalf("finishedEvent.Error = %q, want empty", finishedEvent.Error)
	}
}

func TestHandleImagesForVision_MultipleImagesInOnceMessage_UncontaminatedText(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "what's in these images?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "what's in these images?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img1.png",
						MediaType: "image/png",
						Data:      "base64data1",
					},
					{
						ID:        "img-2",
						FilePath:  "/path/to/img2.png",
						MediaType: "image/png",
						Data:      "base64data2",
					},
				},
			},
		}),
	}

	var capturedTasks []string
	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, input map[string]any) (any, error) {
			task := input["task"].(string)
			capturedTasks = append(capturedTasks, task)

			imageID := input["image_id"].(string)
			result := map[string]any{
				"continuation": map[string]any{"agent_id": "vision-" + imageID},
				"output":       "description for " + imageID,
			}
			resultJSON, _ := json.Marshal(result)
			return string(resultJSON), nil
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	if len(capturedTasks) != 2 {
		t.Fatalf("expected 2 executor calls, got %d", len(capturedTasks))
	}

	// Both tasks should contain the same original user text, not contaminated by the first image's description
	expectedSubstring := "what's in these images?"
	for i, task := range capturedTasks {
		if !contains(task, expectedSubstring) {
			t.Fatalf("task[%d] should contain original user text %q, got: %s", i, expectedSubstring, task)
		}
	}

	// Verify that task[1] does NOT contain task[0]'s description (which would indicate contamination)
	if contains(capturedTasks[1], "description for img-1") {
		t.Fatalf("task[1] is contaminated with task[0]'s output. task[1]: %s", capturedTasks[1])
	}

	// Both tasks should be identical since they're based on the same original content
	if capturedTasks[0] != capturedTasks[1] {
		t.Fatalf("tasks should be identical (same original content), but task[0] != task[1]\ntask[0]: %s\ntask[1]: %s", capturedTasks[0], capturedTasks[1])
	}
}

func TestHandleImagesForVision_RoutingError_EmitsDiagnostic(t *testing.T) {
	vc := NewVisionCapabilities(true)
	vc.LatchIncapable("test-model")

	var capturedEvents []output.Event
	eventSink := output.SinkFunc(func(event output.Event) {
		capturedEvents = append(capturedEvents, event)
	})

	state := RunState{
		Conversation: []Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
					},
				},
			},
		},
		Lineage: newConversationLineage([]Message{
			{
				Role:    MessageRoleUser,
				Content: "what is in this image?",
				Images: []ImageBlock{
					{
						ID:        "img-1",
						FilePath:  "/path/to/img.png",
						MediaType: "image/png",
						Data:      "base64data",
					},
				},
			},
		}),
	}

	executor := &fakeExecutor{
		execute: func(_ context.Context, _ string, _ map[string]any) (any, error) {
			return nil, fmt.Errorf("vision call failed for img-1")
		},
	}

	req := RunRequest{
		ResolvedModel:      provider.ResolvedModel{BackendModelID: "test-model", Alias: "test-model"},
		Executor:           executor,
		VisionCapabilities: vc,
		Events:             eventSink,
	}
	p := newTurnProgressor(req, prompt.AssemblyOptions{}, nil)

	mutated := p.handleImagesForVision(context.Background(), &state)
	if !mutated {
		t.Fatalf("mutated = false, want true")
	}

	content := state.Conversation[0].Content
	if !contains(content, "could not be processed") {
		t.Fatalf("content should indicate fallback, got: %s", content)
	}

	// Find the vision_routing_failed diagnostic event
	var foundEvent *output.ProviderDiagnosticEvent
	for _, event := range capturedEvents {
		if payload, ok := event.Payload.(output.ProviderDiagnosticEvent); ok {
			if payload.Kind == "vision_routing_failed" {
				foundEvent = &payload
				break
			}
		}
	}
	if foundEvent == nil {
		t.Fatalf("no vision_routing_failed diagnostic event found. captured events: %v", capturedEvents)
	}

	if foundEvent.Severity != "warn" {
		t.Fatalf("event.Severity = %q, want warn", foundEvent.Severity)
	}

	if !contains(foundEvent.Message, "img-1") {
		t.Fatalf("message should contain image ID, got: %s", foundEvent.Message)
	}

	if !contains(foundEvent.Message, "vision call failed") {
		t.Fatalf("message should contain error, got: %s", foundEvent.Message)
	}
}

func TestVisionTaskContentForReadUsesParentUserRequest(t *testing.T) {
	messages := []Message{
		{Role: MessageRoleUser, Content: "Compare this image with the previous design."},
		{Role: MessageRoleAssistant, Content: "I will inspect it."},
		{Role: MessageRoleTool, Name: "read", Content: `{"path":".steiner/tmp/fetched/image.webp"}`, Images: []ImageBlock{{ID: "img-1", Data: "image-data"}}},
	}

	got := visionTaskContent(messages, 2)
	if got != messages[0].Content {
		t.Fatalf("visionTaskContent = %q, want parent request %q", got, messages[0].Content)
	}
	if got == messages[2].Content {
		t.Fatal("visionTaskContent used read tool JSON instead of parent request")
	}
}

func TestVisionTaskContentForUserPreservesPastedImageRequest(t *testing.T) {
	messages := []Message{{Role: MessageRoleUser, Content: "Describe this pasted image."}}
	if got := visionTaskContent(messages, 0); got != messages[0].Content {
		t.Fatalf("visionTaskContent = %q, want user content %q", got, messages[0].Content)
	}
}
