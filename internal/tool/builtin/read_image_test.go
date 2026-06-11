package builtin

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

func TestReadImageFile(t *testing.T) {
	// Create a temporary test PNG image.
	tmpFile, err := os.CreateTemp("", "test_image_*.png")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	// Create a simple 2x2 PNG image.
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(1, 0, color.RGBA{0, 255, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	img.Set(1, 1, color.RGBA{255, 255, 255, 255})

	if err := png.Encode(tmpFile, img); err != nil {
		t.Fatalf("Failed to encode PNG: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	tests := []struct {
		name          string
		filePath      string
		displayPath   string
		wantErr       bool
		errContains   string
		wantImage     bool
		wantWidth     int
		wantHeight    int
		wantMediaType string
	}{
		{
			name:          "read valid PNG image",
			filePath:      tmpFile.Name(),
			displayPath:   "images/test.png",
			wantErr:       false,
			wantImage:     true,
			wantWidth:     2,
			wantHeight:    2,
			wantMediaType: "image/png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := readImageFile(tt.filePath, tt.displayPath)
			if tt.wantErr {
				if err == nil {
					t.Errorf("readImageFile() error = nil, wantErr %v", tt.wantErr)
				}
				if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("readImageFile() error = %q, expected to contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Errorf("readImageFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if result == nil {
				t.Errorf("readImageFile() returned nil result")
				return
			}

			if !tt.wantImage && result.Image == nil {
				t.Errorf("readImageFile() Image = nil, want non-nil")
				return
			}

			if tt.wantImage {
				if result.Image == nil {
					t.Errorf("readImageFile() Image = nil, want non-nil")
					return
				}
				if result.Image.MediaType != tt.wantMediaType {
					t.Errorf("readImageFile() MediaType = %q, want %q", result.Image.MediaType, tt.wantMediaType)
				}
				if result.Image.Width != tt.wantWidth {
					t.Errorf("readImageFile() Width = %d, want %d", result.Image.Width, tt.wantWidth)
				}
				if result.Image.Height != tt.wantHeight {
					t.Errorf("readImageFile() Height = %d, want %d", result.Image.Height, tt.wantHeight)
				}
				if result.Image.Data == "" {
					t.Errorf("readImageFile() Data is empty, want base64-encoded content")
				}
				if result.Image.SizeBytes == 0 {
					t.Errorf("readImageFile() SizeBytes = 0, want > 0")
				}
			}

			if result.Path != tt.displayPath {
				t.Errorf("readImageFile() Path = %q, want %q", result.Path, tt.displayPath)
			}

			if result.Output == "" {
				t.Errorf("readImageFile() Output is empty")
			}
		})
	}
}

func TestReadImageFileTooLarge(t *testing.T) {
	// Create a temporary file larger than 5MB.
	tmpFile, err := os.CreateTemp("", "large_image_*.bin")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() {
		_ = os.Remove(tmpFile.Name())
	}()

	// Write 6MB of data.
	sizeBytes := 6 * 1024 * 1024
	if _, err := tmpFile.Write(make([]byte, sizeBytes)); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	result, err := readImageFile(tmpFile.Name(), "large_image.bin")
	if err == nil {
		t.Errorf("readImageFile() error = nil, want error for file > 5MB")
		return
	}
	if result != nil {
		t.Errorf("readImageFile() returned result with error: %v", err)
	}
	if !containsString(err.Error(), "too large") {
		t.Errorf("readImageFile() error = %q, expected to contain 'too large'", err.Error())
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		name string
		size int
		want string
	}{
		{"bytes", 512, "512B"},
		{"kilobytes", 2048, "2KB"},
		{"megabytes", 1048576, "1.0MB"},
		{"megabytes decimal", 1258291, "1.2MB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatFileSize(tt.size)
			if got != tt.want {
				t.Errorf("formatFileSize(%d) = %q, want %q", tt.size, got, tt.want)
			}
		})
	}
}

func TestIsImageExtension(t *testing.T) {
	tests := []struct {
		ext  string
		want bool
	}{
		{".png", true},
		{".jpg", true},
		{".jpeg", true},
		{".gif", true},
		{".webp", true},
		{".PNG", true},
		{".go", false},
		{".txt", false},
		{".json", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			got := IsImageExtension(tt.ext)
			if got != tt.want {
				t.Errorf("IsImageExtension(%q) = %v, want %v", tt.ext, got, tt.want)
			}
		})
	}
}

// containsString is a helper to check if a string contains a substring.
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
