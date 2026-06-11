package agent

import (
	"testing"

	"github.com/luispabon/steiner/internal/tool/builtin"
)

func TestNormalizeToolResultWithImage(t *testing.T) {
	tests := []struct {
		name           string
		result         any
		wantImage      bool
		wantMediaType  string
		wantWidth      int
		wantHeight     int
		wantSizeBytes  int
	}{
		{
			name: "ReadResult with image pointer",
			result: &builtin.ReadResult{
				Path:   "image.png",
				Output: "[image: 2x2 png 84B]",
				Image: &builtin.ImageBlock{
					MediaType: "image/png",
					Data:      "base64encodeddata",
					Width:     2,
					Height:    2,
					SizeBytes: 84,
				},
			},
			wantImage:     true,
			wantMediaType: "image/png",
			wantWidth:     2,
			wantHeight:    2,
			wantSizeBytes: 84,
		},
		{
			name: "ReadResult with image value",
			result: builtin.ReadResult{
				Path:   "image.jpg",
				Output: "[image: 100x100 jpg 5.2MB]",
				Image: &builtin.ImageBlock{
					MediaType: "image/jpeg",
					Data:      "base64data",
					Width:     100,
					Height:    100,
					SizeBytes: 5242880,
				},
			},
			wantImage:     true,
			wantMediaType: "image/jpeg",
			wantWidth:     100,
			wantHeight:    100,
			wantSizeBytes: 5242880,
		},
		{
			name: "ReadResult without image",
			result: &builtin.ReadResult{
				Path:   "file.txt",
				Output: "file content",
			},
			wantImage: false,
		},
		{
			name:      "string result",
			result:    "some output",
			wantImage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envelope := normalizeToolResult(tt.result)

			if tt.wantImage {
				if envelope.Image == nil {
					t.Errorf("Image = nil, want non-nil")
					return
				}
				if envelope.Image.MediaType != tt.wantMediaType {
					t.Errorf("Image.MediaType = %q, want %q", envelope.Image.MediaType, tt.wantMediaType)
				}
				if envelope.Image.Width != tt.wantWidth {
					t.Errorf("Image.Width = %d, want %d", envelope.Image.Width, tt.wantWidth)
				}
				if envelope.Image.Height != tt.wantHeight {
					t.Errorf("Image.Height = %d, want %d", envelope.Image.Height, tt.wantHeight)
				}
				if envelope.Image.SizeBytes != tt.wantSizeBytes {
					t.Errorf("Image.SizeBytes = %d, want %d", envelope.Image.SizeBytes, tt.wantSizeBytes)
				}
			} else {
				if envelope.Image != nil {
					t.Errorf("Image = %v, want nil", envelope.Image)
				}
			}
		})
	}
}

func TestExtractImage(t *testing.T) {
	tests := []struct {
		name      string
		result    any
		wantImage bool
	}{
		{
			name: "ReadResult pointer with image",
			result: &builtin.ReadResult{
				Image: &builtin.ImageBlock{
					MediaType: "image/png",
					Data:      "test",
					Width:     10,
					Height:    10,
					SizeBytes: 100,
				},
			},
			wantImage: true,
		},
		{
			name: "ReadResult value with image",
			result: builtin.ReadResult{
				Image: &builtin.ImageBlock{
					MediaType: "image/gif",
					Data:      "data",
					Width:     5,
					Height:    5,
					SizeBytes: 50,
				},
			},
			wantImage: true,
		},
		{
			name:      "ReadResult pointer without image",
			result:    &builtin.ReadResult{},
			wantImage: false,
		},
		{
			name:      "non-ReadResult",
			result:    "string",
			wantImage: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img := extractImage(tt.result)
			if tt.wantImage {
				if img == nil {
					t.Errorf("extractImage() returned nil, want non-nil")
				}
			} else {
				if img != nil {
					t.Errorf("extractImage() returned %v, want nil", img)
				}
			}
		})
	}
}
