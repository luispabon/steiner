package agent

import (
	"context"
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
	wantContent := "look\n" + imageBlockPlaceholder(image)
	if sent.Content != wantContent {
		t.Fatalf("sent Content = %q, want the frozen placeholder %q", sent.Content, wantContent)
	}
	if len(sent.Images) != 1 || sent.Images[0].Data != "image-bytes" {
		t.Fatalf("sent Images = %#v, want the image delivered", sent.Images)
	}
	if len(sent.SentImageFingerprints) != 1 || sent.SentImageFingerprints[0] != imageFingerprint(image) {
		t.Fatalf("sent SentImageFingerprints = %v, want the delivered fingerprint", sent.SentImageFingerprints)
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
	if len(after.SentImageFingerprints) != 1 || after.SentImageFingerprints[0] != imageFingerprint(image) {
		t.Fatalf("SentImageFingerprints = %v, want the delivered fingerprint kept", after.SentImageFingerprints)
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
	if len(sent.SentImageFingerprints) != 0 {
		t.Fatalf("sent SentImageFingerprints = %v, want none", sent.SentImageFingerprints)
	}
	wantContent := "look\n" + imageBlockPlaceholder(image)
	if sent.Content != wantContent {
		t.Fatalf("sent Content = %q, want the frozen placeholder %q", sent.Content, wantContent)
	}
	if state.Conversation[0].Content != wantContent {
		t.Fatalf("content after cleanup = %q, want %q", state.Conversation[0].Content, wantContent)
	}
	if len(state.Conversation[0].SentImageFingerprints) != 0 {
		t.Fatalf("SentImageFingerprints = %v, want none", state.Conversation[0].SentImageFingerprints)
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
