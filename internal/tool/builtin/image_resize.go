package builtin

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"  // register GIF decoder for image.Decode
	_ "image/jpeg" // register JPEG decoder for image.Decode
	"image/png"
	_ "image/png" // register PNG decoder for image.Decode
)

const maxImageDimension = 2048

// ResizeImageIfNeeded caps the longest side at 2048px preserving aspect ratio.
// Returns resized PNG bytes plus new width/height.
// If image already fits within 2048px on both sides, returns original bytes unchanged
// (no re-encode) along with actual dimensions.
func ResizeImageIfNeeded(data []byte) (out []byte, width, height int, err error) {
	// Decode the image to determine its dimensions and original format.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("decode image: %w", err)
	}

	bounds := img.Bounds()
	width = bounds.Max.X
	height = bounds.Max.Y

	// If both dimensions are within the limit, return original bytes unchanged.
	if width <= maxImageDimension && height <= maxImageDimension {
		return data, width, height, nil
	}

	// Calculate scale factor based on the longest dimension.
	maxDim := width
	if height > maxDim {
		maxDim = height
	}

	scale := float64(maxImageDimension) / float64(maxDim)
	newWidth := int(float64(width) * scale)
	newHeight := int(float64(height) * scale)

	// Create a new RGBA image with the target dimensions.
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Simple nearest-neighbor scaling using stdlib image/draw.CatmullRom or basic pixel mapping.
	// Use draw.Copy as a baseline and interpolate via nearest-neighbor lookup.
	for py := 0; py < newHeight; py++ {
		for px := 0; px < newWidth; px++ {
			// Map new pixel back to original image coordinates.
			srcX := (px * width) / newWidth
			srcY := (py * height) / newHeight

			// Clamp to valid range (should not be needed if math is right).
			if srcX >= width {
				srcX = width - 1
			}
			if srcY >= height {
				srcY = height - 1
			}

			// Get the color from the original image at this nearest point.
			r, g, b, a := img.At(srcX, srcY).RGBA()
			// RGBA() returns 16-bit values; convert back to 8-bit.
			resized.Set(px, py, color.RGBA{
				R: uint8(r >> 8),
				G: uint8(g >> 8),
				B: uint8(b >> 8),
				A: uint8(a >> 8),
			})
		}
	}

	// Encode the resized image as PNG.
	buf := bytes.NewBuffer(nil)
	if err := png.Encode(buf, resized); err != nil {
		return nil, 0, 0, fmt.Errorf("encode resized image: %w", err)
	}

	return buf.Bytes(), newWidth, newHeight, nil
}
