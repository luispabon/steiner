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

func TestResizeImageIfNeeded_ExtremlyElongatedImage(t *testing.T) {
	// Test that extremely elongated images (1px wide, very tall) don't
	// truncate to zero dimensions.
	const testMaxDimension = 64

	// Create a 1x100 image (extremely wide aspect ratio).
	img := image.NewRGBA(image.Rect(0, 0, 1, 100))
	for x := 0; x < 1; x++ {
		for y := 0; y < 100; y++ {
			img.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		t.Fatalf("encode original: %v", err)
	}
	originalData := buf.Bytes()

	out, outW, outH, err := resizeImageIfNeeded(originalData, testMaxDimension)
	if err != nil {
		t.Fatalf("resizeImageIfNeeded: %v", err)
	}

	// Both dimensions should be at least 1.
	if outW < 1 {
		t.Errorf("output width = %d, want >= 1", outW)
	}
	if outH < 1 {
		t.Errorf("output height = %d, want >= 1", outH)
	}

	// Verify output is a valid PNG.
	resized, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Errorf("output is not a valid PNG: %v", err)
	} else {
		bounds := resized.Bounds()
		if bounds.Max.X != outW || bounds.Max.Y != outH {
			t.Errorf("decoded PNG dimensions = %dx%d, want %dx%d",
				bounds.Max.X, bounds.Max.Y, outW, outH)
		}
	}
}

func TestResizeImageIfNeeded_TranslucentPixelPreservation(t *testing.T) {
	// Test that resizing preserves translucent pixel colors without darkening.
	// This checks for premultiplied-alpha handling bugs.
	const testMaxDimension = 100

	// Create an image with a large red region with known translucent pixel.
	img := image.NewNRGBA(image.Rect(0, 0, 200, 200))
	for x := 0; x < 200; x++ {
		for y := 0; y < 200; y++ {
			img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 255})
		}
	}
	// Set a large region to translucent red (255, 0, 0, 128) so it survives downsampling.
	for x := 50; x < 150; x++ {
		for y := 50; y < 150; y++ {
			img.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 128})
		}
	}

	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, img); err != nil {
		t.Fatalf("encode original: %v", err)
	}
	originalData := buf.Bytes()

	out, _, _, err := resizeImageIfNeeded(originalData, testMaxDimension)
	if err != nil {
		t.Fatalf("resizeImageIfNeeded: %v", err)
	}

	// Decode the resized image.
	resized, _, err := image.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode resized: %v", err)
	}

	// Sample from the center of the red region in the resized image.
	bounds := resized.Bounds()
	midX := (bounds.Min.X + bounds.Max.X) / 2
	midY := (bounds.Min.Y + bounds.Max.Y) / 2

	// Get the pixel and convert to NRGBA for comparison.
	c := resized.At(midX, midY)
	nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)

	// Allow ±5 tolerance per channel for round-trip loss through PNG encoding/decoding.
	const tolerance = 5
	if nrgba.R < 250-tolerance || nrgba.R > 255 {
		t.Errorf("resized red channel = %d, want 250-255 (translucent red may be darkened?)", nrgba.R)
	}
	if nrgba.G > 0+tolerance {
		t.Errorf("resized green channel = %d, want ~0", nrgba.G)
	}
	if nrgba.B > 0+tolerance {
		t.Errorf("resized blue channel = %d, want ~0", nrgba.B)
	}
	if nrgba.A < 120 || nrgba.A > 128+tolerance {
		t.Errorf("resized alpha channel = %d, want 120-133", nrgba.A)
	}

	t.Logf("Original region: NRGBA{255, 0, 0, 128}, Resized center pixel: NRGBA{%d, %d, %d, %d}", nrgba.R, nrgba.G, nrgba.B, nrgba.A)
}

func BenchmarkResizeImageIfNeeded(b *testing.B) {
	// Benchmark resizing a 4000x3000 image.
	data := createTestPNG(4000, 3000)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, _ = ResizeImageIfNeeded(data)
	}
}
