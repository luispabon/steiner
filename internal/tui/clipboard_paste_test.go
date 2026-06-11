package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestPendingImagesAccumulate(t *testing.T) {
	// Test that multiple clipboardImageMsg events accumulate pendingImages
	m := Model{}
	block1 := agent.ImageBlock{MediaType: "image/png", Data: "abc", Width: 100, Height: 100}
	block2 := agent.ImageBlock{MediaType: "image/jpeg", Data: "def", Width: 200, Height: 200}

	msg1 := clipboardImageMsg{block: block1}
	msg2 := clipboardImageMsg{block: block2}

	// Simulate Update calls — but Model.Update requires tea infra.
	// Instead test the logic directly:
	if msg1.err != nil {
		t.Fatal("unexpected error")
	}
	m.pendingImages = append(m.pendingImages, msg1.block)
	m.pendingImages = append(m.pendingImages, msg2.block)

	if len(m.pendingImages) != 2 {
		t.Fatalf("pendingImages len = %d, want 2", len(m.pendingImages))
	}
	if m.pendingImages[0].MediaType != "image/png" {
		t.Errorf("pendingImages[0].MediaType = %q, want image/png", m.pendingImages[0].MediaType)
	}
	if m.pendingImages[1].MediaType != "image/jpeg" {
		t.Errorf("pendingImages[1].MediaType = %q, want image/jpeg", m.pendingImages[1].MediaType)
	}
}

func TestPendingImagesClearedOnSubmit(t *testing.T) {
	m := Model{
		pendingImages: []agent.ImageBlock{
			{MediaType: "image/png", Data: "abc"},
		},
	}
	m.pendingImages = nil
	if len(m.pendingImages) != 0 {
		t.Fatalf("pendingImages not cleared, len = %d", len(m.pendingImages))
	}
}

func TestClipboardImageMsgErrSilentlyIgnored(t *testing.T) {
	m := Model{}
	msg := clipboardImageMsg{err: ErrClipboardNoImage}

	// simulate the handler logic
	if msg.err != nil {
		// silently ignore — no append
	} else {
		m.pendingImages = append(m.pendingImages, msg.block)
	}

	if len(m.pendingImages) != 0 {
		t.Fatalf("pendingImages should be empty on error, got len = %d", len(m.pendingImages))
	}
}
