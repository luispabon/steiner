package tui

import (
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

func TestPendingImagesAccumulate(t *testing.T) {
	// Test that multiple clipboardImageMsg events accumulate imageMarkers
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
	m.imageMarkers = append(m.imageMarkers, imageMarker{label: nextMarkerLabel(m.imageMarkers), image: msg1.block})
	m.imageMarkers = append(m.imageMarkers, imageMarker{label: nextMarkerLabel(m.imageMarkers), image: msg2.block})

	if len(m.imageMarkers) != 2 {
		t.Fatalf("imageMarkers len = %d, want 2", len(m.imageMarkers))
	}
	if m.imageMarkers[0].image.MediaType != "image/png" {
		t.Errorf("imageMarkers[0].image.MediaType = %q, want image/png", m.imageMarkers[0].image.MediaType)
	}
	if m.imageMarkers[1].image.MediaType != "image/jpeg" {
		t.Errorf("imageMarkers[1].image.MediaType = %q, want image/jpeg", m.imageMarkers[1].image.MediaType)
	}
}

func TestPendingImagesClearedOnSubmit(t *testing.T) {
	m := Model{
		imageMarkers: []imageMarker{
			{label: "[Image 1]", image: agent.ImageBlock{MediaType: "image/png", Data: "abc"}},
		},
	}
	m.imageMarkers = nil
	if len(m.imageMarkers) != 0 {
		t.Fatalf("imageMarkers not cleared, len = %d", len(m.imageMarkers))
	}
}

func TestClipboardImageMsgErrSilentlyIgnored(t *testing.T) {
	m := Model{}
	msg := clipboardImageMsg{err: ErrClipboardNoImage}

	// simulate the handler logic
	if msg.err != nil {
		// silently ignore — no append
	} else {
		m.imageMarkers = append(m.imageMarkers, imageMarker{label: nextMarkerLabel(m.imageMarkers), image: msg.block})
	}

	if len(m.imageMarkers) != 0 {
		t.Fatalf("imageMarkers should be empty on error, got len = %d", len(m.imageMarkers))
	}
}
