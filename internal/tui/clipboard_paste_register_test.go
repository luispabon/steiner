package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// TestBuildClipboardImageMsgRegistersOnWriteFailure verifies the image is always
// registered, even when the store folder cannot be created, so recall fails
// cleanly instead of the ID going missing.
func TestBuildClipboardImageMsgRegistersOnWriteFailure(t *testing.T) {
	t.Parallel()
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	// Root is a regular file, so MkdirAll(store.Dir()) always fails.
	store := agent.NewImageStore(filepath.Join(blocker, "images"))

	msg := buildClipboardImageMsg(minimalPNG(t), "image/png", store)
	got, ok := msg.(clipboardImageMsg)
	if !ok {
		t.Fatalf("expected clipboardImageMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.block.ID != "img-1" {
		t.Errorf("block.ID = %q, want img-1 even when the write fails", got.block.ID)
	}
	if got.block.FilePath != "" {
		t.Errorf("block.FilePath = %q, want empty on write failure", got.block.FilePath)
	}
	if _, ok := store.Get(got.block.ID); !ok {
		t.Errorf("Get(%q) = not found, want registered", got.block.ID)
	}
}
