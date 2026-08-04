package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// The four tests below (VisionNil, VisionTrue, VisionFalseNoImages, VisionFalseWithImages)
// pass capabilities=nil and so only exercise the deprecated *bool fallback path.

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

func TestStripImagesIfVisionDisabled_CapabilitiesSupported(t *testing.T) {
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "hello",
			Images: []provider.ImageBlock{
				{MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	}

	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("test-model", VisionCapable)

	var events []output.Event
	result := stripImagesIfVisionDisabled(nil, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), capabilities)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with capabilities=VisionCapable: returned %d messages, want %d", len(result), len(messages))
	}
	if len(result[0].Images) == 0 {
		t.Fatal("stripImagesIfVisionDisabled with capabilities=VisionCapable: images were stripped, want unchanged")
	}
	if len(events) != 0 {
		t.Fatalf("stripImagesIfVisionDisabled with capabilities=VisionCapable: emitted %d events, want 0", len(events))
	}
}

func TestStripImagesIfVisionDisabled_CapabilitiesUnsupported(t *testing.T) {
	messages := []provider.Message{
		{
			Role:    provider.MessageRoleUser,
			Content: "hello",
			Images: []provider.ImageBlock{
				{MediaType: "image/png", Data: "iVBORw0KGgo="},
			},
		},
	}

	capabilities := NewVisionCapabilities(false)
	capabilities.SetDerived("test-model", VisionIncapable)

	var events []output.Event
	result := stripImagesIfVisionDisabled(nil, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
		events = append(events, e)
	}), capabilities)

	if len(result) != len(messages) {
		t.Fatalf("stripImagesIfVisionDisabled with capabilities=VisionIncapable: returned %d messages, want %d", len(result), len(messages))
	}
	if len(result[0].Images) != 0 {
		t.Fatalf("stripImagesIfVisionDisabled with capabilities=VisionIncapable: images not stripped, want stripped")
	}
	if len(events) != 1 {
		t.Fatalf("stripImagesIfVisionDisabled with capabilities=VisionIncapable: emitted %d events, want 1", len(events))
	}
}

// TestStripImagesIfVisionDisabled_CapabilitiesOverridesDeprecatedBool pins the precedence rule
// in stripImagesIfVisionDisabled: when capabilities is non-nil, it is used exclusively and the
// deprecated vision *bool is ignored entirely, even when the two disagree.
func TestStripImagesIfVisionDisabled_CapabilitiesOverridesDeprecatedBool(t *testing.T) {
	t.Run("capabilities capable overrides deprecated false", func(t *testing.T) {
		messages := []provider.Message{
			{
				Role:    provider.MessageRoleUser,
				Content: "hello",
				Images: []provider.ImageBlock{
					{MediaType: "image/png", Data: "iVBORw0KGgo="},
				},
			},
		}

		visionFalse := false
		capabilities := NewVisionCapabilities(false)
		capabilities.SetDerived("test-model", VisionCapable)

		var events []output.Event
		result := stripImagesIfVisionDisabled(&visionFalse, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
			events = append(events, e)
		}), capabilities)

		if len(result[0].Images) == 0 {
			t.Fatal("capabilities=VisionCapable with deprecated vision=false: images were stripped, want unchanged (capabilities must win)")
		}
		if len(events) != 0 {
			t.Fatalf("capabilities=VisionCapable with deprecated vision=false: emitted %d events, want 0", len(events))
		}
	})

	t.Run("capabilities incapable overrides deprecated true", func(t *testing.T) {
		messages := []provider.Message{
			{
				Role:    provider.MessageRoleUser,
				Content: "hello",
				Images: []provider.ImageBlock{
					{MediaType: "image/png", Data: "iVBORw0KGgo="},
				},
			},
		}

		visionTrue := true
		capabilities := NewVisionCapabilities(false)
		capabilities.SetDerived("test-model", VisionIncapable)

		var events []output.Event
		result := stripImagesIfVisionDisabled(&visionTrue, messages, "test-model", 1, output.SinkFunc(func(e output.Event) {
			events = append(events, e)
		}), capabilities)

		if len(result[0].Images) != 0 {
			t.Fatal("capabilities=VisionIncapable with deprecated vision=true: images were not stripped, want stripped (capabilities must win)")
		}
		if len(events) != 1 {
			t.Fatalf("capabilities=VisionIncapable with deprecated vision=true: emitted %d events, want 1", len(events))
		}
	})
}
