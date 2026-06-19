package builtin

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"  // register GIF decoder for image.Decode
	_ "image/jpeg" // register JPEG decoder for image.Decode
	_ "image/png"  // register PNG decoder for image.Decode
	"os"
	"path/filepath"
	"strings"

	"github.com/deepnoodle-ai/dive/toolkit"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tool"
)

// NewReadTool creates a ToolDef for the read tool backed by Dive's ReadFileTool.
// nolint:gocyclo // handler closure complexity is unavoidable with multi-branch file type logic and pagination
func NewReadTool(env Env) tool.ToolDef {
	readTool := toolkit.NewReadFileTool()
	return tool.ToolDef{
		Name:            "read",
		Description:     "Read a file or part of a file. Prefer offset and limit for large files. Use grep or glob first when locating code. Supports image files for visual inspection and returns image data plus dimensions/size metadata. Returns line-numbered content and pagination metadata for text files.",
		ParameterSchema: ReadSchema(),
		Handler: func(ctx context.Context, input map[string]any) (any, error) {
			in, err := decodeInput[ReadInput](input)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			normalizeRead(&in)

			absPath, err := env.PathPolicy.ResolveReadPath(in.Path)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			// Check if this is an image file and handle it specially.
			if IsImageExtension(filepath.Ext(absPath)) {
				return readImageFile(absPath, relDisplayPath(env.WorkDir, absPath))
			}

			diveResult, err := readTool.Call(ctx, &toolkit.ReadFileInput{
				FilePath: absPath,
				Offset:   in.Offset,
				Limit:    in.Limit,
			})
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			contentText := ""
			if len(diveResult.Content) > 0 {
				contentText = diveResult.Content[0].Text
			}

			if diveResult.IsError {
				return &ReadResult{
					Path:   relDisplayPath(env.WorkDir, absPath),
					Output: contentText,
				}, nil
			}

			data, err := os.ReadFile(absPath)
			if err != nil {
				return nil, fmt.Errorf("read: %w", err)
			}

			totalLines := 0
			if len(data) > 0 {
				totalLines = bytes.Count(data, []byte{'\n'})
				if data[len(data)-1] != '\n' {
					totalLines++
				}
			}

			outputLines := strings.Split(contentText, "\n")
			if len(outputLines) > 0 && outputLines[len(outputLines)-1] == "" {
				outputLines = outputLines[:len(outputLines)-1]
			}
			numLines := len(outputLines)

			startLine := in.Offset
			endLine := startLine + numLines - 1
			if numLines == 0 {
				endLine = 0
			}

			result := ReadResult{
				Path:       relDisplayPath(env.WorkDir, absPath),
				FileHash:   fileContentHash(data),
				StartLine:  startLine,
				EndLine:    endLine,
				TotalLines: totalLines,
				Output:     contentText,
			}

			if endLine > 0 && endLine < totalLines {
				result.NextOffset = endLine + 1
			}

			return result, nil
		},
	}
}

// readImageFile reads an image file, base64-encodes it, detects dimensions,
// and returns a ReadResult with an embedded ImageBlock.
func readImageFile(absPath, displayPath string) (*ReadResult, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}

	// Check size before base64 encoding (max 5MB).
	if len(data) > 5*1024*1024 {
		return nil, fmt.Errorf("read image: file too large (max 5MB, got %s)", output.FormatFileSize(len(data)))
	}

	// Detect dimensions using stdlib image package.
	width, height := 0, 0
	img, _, err := image.Decode(bytes.NewReader(data))
	if err == nil {
		bounds := img.Bounds()
		width = bounds.Max.X
		height = bounds.Max.Y
	}
	// If decoding fails, we still proceed with width=0, height=0.

	// Detect media type from extension.
	ext := strings.ToLower(filepath.Ext(absPath))
	mediaType := ""
	switch ext {
	case ".png":
		mediaType = "image/png"
	case ".jpg", ".jpeg":
		mediaType = "image/jpeg"
	case ".gif":
		mediaType = "image/gif"
	case ".webp":
		mediaType = "image/webp"
	default:
		// Fallback (should not happen if IsImageExtension is correct).
		mediaType = "image/unknown"
	}

	// Base64 encode.
	encoded := base64.StdEncoding.EncodeToString(data)

	// Build summary string.
	var summary string
	if width > 0 {
		summary = fmt.Sprintf("[image: %dx%d %s %s]", width, height, ext[1:], output.FormatFileSize(len(data)))
	} else {
		summary = fmt.Sprintf("[image: %s %s]", ext[1:], output.FormatFileSize(len(data)))
	}

	return &ReadResult{
		Path:   displayPath,
		Output: summary,
		Image: &ImageBlock{
			MediaType: mediaType,
			Data:      encoded,
			Width:     width,
			Height:    height,
			SizeBytes: len(data),
		},
	}, nil
}
