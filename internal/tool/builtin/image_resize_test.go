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
	const testMaxDimension = 64

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
			name:         "125x100 resized to 64x51",
			width:        125,
			height:       100,
			expectResize: true,
			expectWidth:  64,
			expectHeight: 51,
			expectValid:  true,
		},
		{
			name:         "60x34 unchanged",
			width:        60,
			height:       34,
			expectResize: false,
			expectWidth:  60,
			expectHeight: 34,
			expectValid:  true,
		},
		{
			name:         "32x32 unchanged",
			width:        32,
			height:       32,
			expectResize: false,
			expectWidth:  32,
			expectHeight: 32,
			expectValid:  true,
		},
		{
			name:         "64x64 exactly at limit unchanged",
			width:        64,
			height:       64,
			expectResize: false,
			expectWidth:  64,
			expectHeight: 64,
			expectValid:  true,
		},
		{
			name:         "65x10 resized",
			width:        65,
			height:       10,
			expectResize: true,
			expectWidth:  64,
			expectHeight: 9,
			expectValid:  true,
		},
		{
			name:         "10x65 resized",
			width:        10,
			height:       65,
			expectResize: true,
			expectWidth:  9,
			expectHeight: 64,
			expectValid:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalData := createTestPNG(tt.width, tt.height)

			out, w, h, err := resizeImageIfNeeded(originalData, testMaxDimension)
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
