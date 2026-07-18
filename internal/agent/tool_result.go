package agent

import (
	"encoding/json"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/tool/builtin"
)

// ToolResultEnvelope normalizes tool output plus metadata for agent messages.
type ToolResultEnvelope struct {
	Content   string
	Metadata  tool.ExecutionMetadata
	Retention *MessageRetention
	Image     *ImageBlock
}

func normalizeToolResult(result any) ToolResultEnvelope {
	switch v := result.(type) {
	case tool.ExecutionResult:
		env := ToolResultEnvelope{
			Content:   toolResultContent(v.Value),
			Metadata:  v.Metadata,
			Retention: messageRetentionFromToolRetention(v.Retention),
		}
		// Extract image if present in the wrapped result.
		env.Image = extractImage(v.Value)
		if env.Image != nil {
			env.Content = toolResultContentWithoutImage(v.Value)
		}
		return env
	default:
		env := ToolResultEnvelope{
			Content: toolResultContent(result),
		}
		// Extract image if present.
		env.Image = extractImage(result)
		if env.Image != nil {
			env.Content = toolResultContentWithoutImage(result)
		}
		return env
	}
}

// extractImage extracts an ImageBlock from a tool result if present.
func extractImage(result any) *ImageBlock {
	switch v := result.(type) {
	case *builtin.ReadResult:
		if v == nil {
			return nil
		}
		return imageBlockFrom(v.Image)
	case builtin.ReadResult:
		return imageBlockFrom(v.Image)
	case *builtin.FetchURLResult:
		if v == nil {
			return nil
		}
		return imageBlockFrom(v.Image)
	case builtin.FetchURLResult:
		return imageBlockFrom(v.Image)
	}
	return nil
}

func imageBlockFrom(img *builtin.ImageBlock) *ImageBlock {
	if img == nil {
		return nil
	}
	return &ImageBlock{
		MediaType: img.MediaType,
		Data:      img.Data,
		Width:     img.Width,
		Height:    img.Height,
		SizeBytes: img.SizeBytes,
	}
}

func toolResultContent(result any) string {
	switch v := result.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	default:
		data, err := json.Marshal(v)
		if err == nil {
			return string(data)
		}
		return fmt.Sprint(v)
	}
}

func toolResultContentWithoutImage(result any) string {
	switch v := result.(type) {
	case *builtin.ReadResult:
		if v == nil {
			return ""
		}
		cloned := *v
		cloned.Image = nil
		return toolResultContent(cloned)
	case builtin.ReadResult:
		cloned := v
		cloned.Image = nil
		return toolResultContent(cloned)
	case *builtin.FetchURLResult:
		if v == nil {
			return ""
		}
		cloned := *v
		cloned.Image = nil
		return toolResultContent(cloned)
	case builtin.FetchURLResult:
		cloned := v
		cloned.Image = nil
		return toolResultContent(cloned)
	case tool.ExecutionResult:
		return toolResultContentWithoutImage(v.Value)
	default:
		return toolResultContent(result)
	}
}
