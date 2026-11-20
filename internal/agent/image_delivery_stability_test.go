package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// TestRunnerImageTextStableAcrossDelivery proves the placeholder an image
// message carries is written before the provider call and survives the later
// byte strip, so a stateful provider sees the same text before and after image
// cleanup.
func TestRunnerImageTextStableAcrossDelivery(t *testing.T) {
	image := ImageBlock{ID: "img-1", FilePath: "/tmp/shot.png", MediaType: "image/png", Data: "image-bytes", Width: 4, Height: 5, SizeBytes: 2048}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	vision := true

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
			return nil, nil
		}},
		SourceConversation: []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{image}}},
		ResolvedModel:      provider.ResolvedModel{Alias: "parent", BackendModelID: "parent", Vision: &vision},
		Limits:             Limits{MaxTurns: 2},
		Prompt:             prompt.AssemblyOptions{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(providerStub.requests) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(providerStub.requests))
	}
	sent := imageMessageInRequest(t, providerStub.requests[0].Messages)
	wantContent := "look\n" + imageBlockPlaceholder(image, false)
	if sent.Content != wantContent {
		t.Fatalf("sent Content = %q, want the frozen placeholder %q", sent.Content, wantContent)
	}
	if len(sent.Images) != 1 || sent.Images[0].Data != "image-bytes" {
		t.Fatalf("sent Images = %#v, want the image delivered", sent.Images)
	}

	if len(state.Conversation) == 0 {
		t.Fatal("conversation is empty")
	}
	after := state.Conversation[0]
	if after.Content != wantContent {
		t.Fatalf("content after cleanup = %q, want the sent text %q", after.Content, wantContent)
	}
	if len(after.Images) != 0 {
		t.Fatalf("image bytes survived the run: %#v", after.Images)
	}
}

// TestRunnerImageFingerprintOnlyWhenDelivered proves a request whose model
// cannot receive images freezes the same text but records no fingerprint, so a
// provider record never claims an image that was stripped from the wire.
func TestRunnerImageFingerprintOnlyWhenDelivered(t *testing.T) {
	image := ImageBlock{ID: "img-1", FilePath: "/tmp/shot.png", MediaType: "image/png", Data: "image-bytes", Width: 4, Height: 5, SizeBytes: 2048}
	providerStub := &fakeProvider{responses: []provider.ChatResponse{
		{Message: provider.Message{Role: provider.MessageRoleAssistant, Content: "done"}},
	}}
	vision := false

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
			return nil, nil
		}},
		SourceConversation: []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{image}}},
		ResolvedModel:      provider.ResolvedModel{Alias: "parent", BackendModelID: "parent", Vision: &vision},
		Limits:             Limits{MaxTurns: 2},
		Prompt:             prompt.AssemblyOptions{},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	sent := imageMessageInRequest(t, providerStub.requests[0].Messages)
	if len(sent.Images) != 0 {
		t.Fatalf("sent Images = %#v, want the request stripped of images", sent.Images)
	}
	wantContent := "look\n" + imageBlockPlaceholder(image, false)
	if sent.Content != wantContent {
		t.Fatalf("sent Content = %q, want the frozen placeholder %q", sent.Content, wantContent)
	}
	if state.Conversation[0].Content != wantContent {
		t.Fatalf("content after cleanup = %q, want %q", state.Conversation[0].Content, wantContent)
	}
}

// imageMessageInRequest returns the request message carrying the test prompt.
func imageMessageInRequest(t *testing.T, msgs []provider.Message) provider.Message {
	t.Helper()
	for _, msg := range msgs {
		if msg.Role != provider.MessageRoleUser {
			continue
		}
		if strings.HasPrefix(msg.Content, "look") {
			return msg
		}
	}
	t.Fatalf("user image message not found in request: %+v", msgs)
	return provider.Message{}
}

// TestRunnerFailedCallRecordsNoFingerprint proves a model call that fails never
// claims a delivery: the frozen text stays, the request record keeps the user's
// words, and no fingerprint is recorded.
func TestRunnerFailedCallRecordsNoFingerprint(t *testing.T) {
	image := ImageBlock{ID: "img-1", FilePath: "/tmp/shot.png", MediaType: "image/png", Data: "image-bytes", Width: 4, Height: 5, SizeBytes: 2048}
	providerStub := &fakeProvider{chatFn: func(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
		return provider.ChatResponse{}, errors.New("model unavailable")
	}}
	vision := true

	state, err := NewRunner().Run(context.Background(), RunRequest{
		Provider: providerStub,
		Executor: &fakeExecutor{execute: func(context.Context, string, map[string]any) (any, error) {
			return nil, nil
		}},
		SourceConversation: []Message{{Role: MessageRoleUser, Content: "look", Images: []ImageBlock{image}}},
		ResolvedModel:      provider.ResolvedModel{Alias: "parent", BackendModelID: "parent", Vision: &vision},
		Limits:             Limits{MaxTurns: 2},
		Prompt:             prompt.AssemblyOptions{},
	})
	if err == nil {
		t.Fatal("Run() error = nil, want the model failure")
	}
	after := state.Conversation[0]
	if after.RequestText != "look" {
		t.Fatalf("RequestText = %q, want the user's words", after.RequestText)
	}
	if after.Content != "look\n"+imageBlockPlaceholder(image, false) {
		t.Fatalf("Content = %q, want the frozen placeholder kept", after.Content)
	}
}

// TestStripImagesIfVisionDisabledStripsOnlyImageMessages proves the wire-level
// strip removes images only from the messages that carry them, leaving earlier
// history untouched and failing closed on messages without images.
func TestStripImagesIfVisionDisabledStripsOnlyImageMessages(t *testing.T) {
	vision := false
	earlier := provider.Message{Role: provider.MessageRoleUser, Content: "old"}
	current := provider.Message{Role: provider.MessageRoleUser, Content: "new", Images: []provider.ImageBlock{{MediaType: "image/png", Data: "AAAA"}}}
	got, stripped := stripImagesIfVisionDisabled(&vision, []provider.Message{earlier, current}, "alias", 1, nil, nil)
	if !stripped {
		t.Fatal("stripped = false, want the images stripped")
	}
	if got[0].Content != "old" || len(got[0].Images) != 0 {
		t.Fatalf("earlier message = %#v, want it untouched", got[0])
	}
	if len(got[1].Images) != 0 {
		t.Fatalf("current message = %#v, want images cleared", got[1])
	}
}
