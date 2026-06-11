package tui

import "testing"

func TestClipboardErrors(t *testing.T) {
	if ErrClipboardNoImage == nil {
		t.Error("ErrClipboardNoImage must not be nil")
	}
	if ErrClipboardUnsupported == nil {
		t.Error("ErrClipboardUnsupported must not be nil")
	}
	if ErrImageTooLarge == nil {
		t.Error("ErrImageTooLarge must not be nil")
	}

	// Errors must be distinct.
	if ErrClipboardNoImage == ErrClipboardUnsupported {
		t.Error("ErrClipboardNoImage and ErrClipboardUnsupported must be distinct")
	}
	if ErrClipboardNoImage == ErrImageTooLarge {
		t.Error("ErrClipboardNoImage and ErrImageTooLarge must be distinct")
	}
	if ErrClipboardUnsupported == ErrImageTooLarge {
		t.Error("ErrClipboardUnsupported and ErrImageTooLarge must be distinct")
	}
}

func TestClipboardMaxImageBytes(t *testing.T) {
	const want = 5 * 1024 * 1024
	if clipboardMaxImageBytes != want {
		t.Errorf("clipboardMaxImageBytes = %d, want %d", clipboardMaxImageBytes, want)
	}
}
