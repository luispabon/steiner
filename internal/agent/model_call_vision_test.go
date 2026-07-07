package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

func TestStripImagesIfVisionDisabled_VisionNil(t *testing.T) {
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "hello",
			Images: []provider.ImageBlock{
				{MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	}

	var events []output.Event
	result := stripImagesIfVisionDisabled(nil, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), nil)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with nil vision: returned %d messages, want %d", len(result), len(messages))
	}
	if len(result[0].Images) == 0 {
		t.Fatal("stripImagesIfVisionDisabled with nil vision: images were stripped, want unchanged")
	}
	if len(events) != 0 {
		t.Fatalf("stripImagesIfVisionDisabled with nil vision: emitted %d events, want 0", len(events))
	}
}

func TestStripImagesIfVisionDisabled_VisionTrue(t *testing.T) {
	visionTrue := true
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "hello",
			Images: []provider.ImageBlock{
				{MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	}

	var events []output.Event
	result := stripImagesIfVisionDisabled(&visionTrue, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), nil)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with vision=true: returned %d messages, want %d", len(result), len(messages))
	}
	if len(result[0].Images) == 0 {
		t.Fatal("stripImagesIfVisionDisabled with vision=true: images were stripped, want unchanged")
	}
	if len(events) != 0 {
		t.Fatalf("stripImagesIfVisionDisabled with vision=true: emitted %d events, want 0", len(events))
	}
}

func TestStripImagesIfVisionDisabled_VisionFalseNoImages(t *testing.T) {
	visionFalse := false
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "hello",
			Images:  []provider.ImageBlock{},
		},
	}

	var events []output.Event
	result := stripImagesIfVisionDisabled(&visionFalse, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), nil)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with vision=false, no images: returned %d messages, want %d", len(result), len(messages))
	}
	if len(events) != 0 {
		t.Fatalf("stripImagesIfVisionDisabled with vision=false, no images: emitted %d events, want 0", len(events))
	}
}

func TestStripImagesIfVisionDisabled_VisionFalseWithImages(t *testing.T) {
	visionFalse := false
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "first message",
			Images:  []provider.ImageBlock{},
		},
		{
			Role:    provider.MessageRoleUser,
			Content: "second message",
			Images: []provider.ImageBlock{
				{MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	}

	var events []output.Event
	result := stripImagesIfVisionDisabled(&visionFalse, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), nil)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with vision=false: returned %d messages, want %d", len(result), len(messages))
	}
	for i, msg := range result {
		if len(msg.Images) != 0 {
			t.Fatalf("stripImagesIfVisionDisabled: message %d still has %d images, want 0", i, len(msg.Images))
		}
	}
	if len(events) != 1 {
		t.Fatalf("stripImagesIfVisionDisabled: emitted %d events, want 1", len(events))
	}

	event := events[0]
	if event.Type != output.EventTypeProviderDiagnostic {
		t.Fatalf("stripImagesIfVisionDisabled: event type = %q, want %q", event.Type, output.EventTypeProviderDiagnostic)
	}

	// Verify event payload
	payload, ok := event.Payload.(output.ProviderDiagnosticEvent)
	if !ok {
		t.Fatalf("stripImagesIfVisionDisabled: event payload type = %T, want ProviderDiagnosticEvent", event.Payload)
	}
	if payload.Severity != "warning" {
		t.Fatalf("stripImagesIfVisionDisabled: event severity = %q, want warning", payload.Severity)
	}
	if payload.Kind != "vision_disabled" {
		t.Fatalf("stripImagesIfVisionDisabled: event kind = %q, want vision_disabled", payload.Kind)
	}
	if payload.Turn != 1 {
		t.Fatalf("stripImagesIfVisionDisabled: event turn = %d, want 1", payload.Turn)
	}
}
