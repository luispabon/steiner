package builtin

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// createTestPNG creates a simple PNG image with the specified dimensions.
func createTestPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// Fill with a simple pattern for testing.
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.SetRGBA(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), 128, 255})
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func TestResizeImageIfNeeded(t *testing.T) {
	tests := []struct {
		name         string
		width        int
		height       int
		expectResize bool
		expectWidth  int
		expectHeight int
		expectValid  bool // whether output is a valid PNG
	}{
		{
			name:         "4000x3000 resized to 2048x1536",
			width:        4000,
			height:       3000,
			expectResize: true,
			expectWidth:  2048,
			expectHeight: 1536,
			expectValid:  true,
		},
		{
			name:         "1920x1080 unchanged",
			width:        1920,
			height:       1080,
			expectResize: false,
			expectWidth:  1920,
			expectHeight: 1080,
			expectValid:  true,
		},
		{
			name:         "100x100 unchanged",
			width:        100,
			height:       100,
			expectResize: false,
			expectWidth:  100,
			expectHeight: 100,
			expectValid:  true,
		},
		{
			name:         "2048x2048 exactly at limit unchanged",
			width:        2048,
			height:       2048,
			expectResize: false,
			expectWidth:  2048,
			expectHeight: 2048,
			expectValid:  true,
		},
		{
			name:         "2049x100 resized",
			width:        2049,
			height:       100,
			expectResize: true,
			expectWidth:  2048,
			expectHeight: 99,
			expectValid:  true,
		},
		{
			name:         "100x2049 resized",
			width:        100,
			height:       2049,
			expectResize: true,
			expectWidth:  99,
			expectHeight: 2048,
			expectValid:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := createTestPNG(tt.width, tt.height)

			out, w, h, err := ResizeImageIfNeeded(originalData)
			if err != nil {
				t.Fatalf("ResizeImageIfNeeded: %v", err)
			}

			if w != tt.expectWidth {
				t.Errorf("width = %d, want %d", w, tt.expectWidth)
			}
			if h != tt.expectHeight {
				t.Errorf("height = %d, want %d", h, tt.expectHeight)
			}

			// Check if resize happened as expected.
			if tt.expectResize && bytes.Equal(out, originalData) {
				t.Errorf("expected resize but got original bytes unchanged")
			}
			if !tt.expectResize && !bytes.Equal(out, originalData) {
				t.Errorf("expected original bytes unchanged but got different data")
			}

			// Verify output is a valid PNG.
			if tt.expectValid {
				img, _, err := image.Decode(bytes.NewReader(out))
				if err != nil {
					t.Errorf("output is not a valid PNG: %v", err)
				} else {
					bounds := img.Bounds()
					if bounds.Max.X != tt.expectWidth || bounds.Max.Y != tt.expectHeight {
						t.Errorf("decoded PNG dimensions = %dx%d, want %dx%d",
							bounds.Max.X, bounds.Max.Y, tt.expectWidth, tt.expectHeight)
					}
				}
			}
		})
	}
}

func BenchmarkResizeImageIfNeeded(b *testing.B) {
	// Benchmark resizing a 4000x3000 image.
	data := createTestPNG(4000, 3000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = ResizeImageIfNeeded(data)
	}
}
