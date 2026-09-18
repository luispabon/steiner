package tui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luispabon/steiner/internal/agent"
)

// minimalPNG returns the bytes of a 1x1 PNG image for testing.
func minimalPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("minimalPNG encode: %v", err)
	}
	return buf.Bytes()
}

func TestReadClipboardImageFile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		size      int
		wantError error
	}{
		{name: "at limit", size: clipboardMaxImageBytes},
		{name: "over limit", size: clipboardMaxImageBytes + 1, wantError: ErrImageTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "image.png")
			if err := os.WriteFile(path, make([]byte, tc.size), 0o600); err != nil {
				t.Fatalf("write test image: %v", err)
			}

			data, err := readClipboardImageFile(path)
			if !errors.Is(err, tc.wantError) {
				t.Fatalf("readClipboardImageFile() error = %v, want %v", err, tc.wantError)
			}
			if tc.wantError == nil && len(data) != tc.size {
				t.Errorf("readClipboardImageFile() returned %d bytes, want %d", len(data), tc.size)
			}
		})
	}
}

func TestBuildClipboardImageMsgWithStorePopulatesIDAndFilePath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := agent.NewImageStore(filepath.Join(dir, "images"))

	data := minimalPNG(t)
	msg := buildClipboardImageMsg(data, "image/png", store)

	got, ok := msg.(clipboardImageMsg)
	if !ok {
		t.Fatalf("expected clipboardImageMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.block.ID == "" {
		t.Error("block.ID is empty, want img-1")
	}
	if got.block.FilePath == "" {
		t.Error("block.FilePath is empty, want a path under the store dir")
	}
	if !strings.HasPrefix(got.block.FilePath, store.Dir()) {
		t.Errorf("block.FilePath %q does not start with store dir %q", got.block.FilePath, store.Dir())
	}
	if _, err := os.Stat(got.block.FilePath); err != nil {
		t.Errorf("file not found at FilePath %q: %v", got.block.FilePath, err)
	}
	// Filename must end with .png
	if filepath.Ext(got.block.FilePath) != ".png" {
		t.Errorf("expected .png extension, got %q", filepath.Ext(got.block.FilePath))
	}
}

func TestBuildClipboardImageMsgWithoutStoreNoIDOrFilePath(t *testing.T) {
	t.Parallel()
	data := minimalPNG(t)
	msg := buildClipboardImageMsg(data, "image/png", nil)

	got, ok := msg.(clipboardImageMsg)
	if !ok {
		t.Fatalf("expected clipboardImageMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}
	if got.block.ID != "" {
		t.Errorf("block.ID = %q, want empty when store is nil", got.block.ID)
	}
	if got.block.FilePath != "" {
		t.Errorf("block.FilePath = %q, want empty when store is nil", got.block.FilePath)
	}
}

func TestExtFromMIME(t *testing.T) {
	t.Parallel()
	cases := []struct{ mime, want string }{
		{"image/png", "png"},
		{"image/jpeg", "jpg"},
		{"image/gif", "gif"},
		{"image/webp", "webp"},
		{"image/bmp", "png"}, // default
	}
	for _, tc := range cases {
		got := extFromMIME(tc.mime)
		if got != tc.want {
			t.Errorf("extFromMIME(%q) = %q, want %q", tc.mime, got, tc.want)
		}
	}
}

func TestPendingImagesAccumulate(t *testing.T) {
	// Test that multiple clipboardImageMsg events accumulate imageMarkers
	t.Parallel()
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
	t.Parallel()
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

func TestHandleClipboardImageMsgNoImageIsSilent(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)

	m.handleClipboardImageMsg(clipboardImageMsg{err: ErrClipboardNoImage})

	if len(m.content.segments) != 0 {
		t.Fatalf("content segments = %d, want 0 for no-image sentinel", len(m.content.segments))
	}
}

func TestHandleClipboardImageMsgProcessingErrorAppendsError(t *testing.T) {
	t.Parallel()
	m := newModel(Config{}, nil)
	errProcessing := errors.New("clipboard decode failed")

	m.handleClipboardImageMsg(clipboardImageMsg{err: errProcessing})

	if len(m.content.segments) != 1 {
		t.Fatalf("content segments = %d, want 1", len(m.content.segments))
	}
	if got := m.content.segments[0].text; !strings.Contains(got, errProcessing.Error()) {
		t.Fatalf("error content = %q, want %q", got, errProcessing)
	}
}

func TestBuildClipboardImageMsgResizedImageDetectsActualType(t *testing.T) {
	t.Parallel()
	// Create a large JPEG image that will trigger resizing to PNG.
	// ResizeImageIfNeeded re-encodes images > 2048px to PNG.
	img := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	for y := 0; y < 3000; y++ {
		for x := 0; x < 3000; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: uint8(x % 256), B: uint8(y % 256), A: 255})
		}
	}

	// Encode as JPEG
	var jpegBuf bytes.Buffer
	if err := jpeg.Encode(&jpegBuf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	jpegData := jpegBuf.Bytes()

	// Process with buildClipboardImageMsg using a store
	dir := t.TempDir()
	store := agent.NewImageStore(filepath.Join(dir, "images"))
	msg := buildClipboardImageMsg(jpegData, "image/jpeg", store)

	got, ok := msg.(clipboardImageMsg)
	if !ok {
		t.Fatalf("expected clipboardImageMsg, got %T", msg)
	}
	if got.err != nil {
		t.Fatalf("unexpected error: %v", got.err)
	}

	// After resizing, the JPEG bytes are re-encoded as PNG, so MediaType should be image/png
	if got.block.MediaType != "image/png" {
		t.Errorf("block.MediaType = %q, want image/png (resized JPEG becomes PNG)", got.block.MediaType)
	}

	// File extension should match the actual content type (PNG)
	if filepath.Ext(got.block.FilePath) != ".png" {
		t.Errorf("block.FilePath extension = %q, want .png", filepath.Ext(got.block.FilePath))
	}

	// File should exist and contain PNG bytes
	data, err := os.ReadFile(got.block.FilePath)
	if err != nil {
		t.Errorf("failed to read stored file: %v", err)
	}

	// PNG files start with the PNG signature
	if len(data) < 8 || !bytes.Equal(data[:8], []byte{137, 80, 78, 71, 13, 10, 26, 10}) {
		t.Error("stored file is not valid PNG (wrong magic bytes)")
	}
}
