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
		return env
	default:
		env := ToolResultEnvelope{
			Content: toolResultContent(result),
		}
		// Extract image if present.
		env.Image = extractImage(result)
		return env
	}
}

// extractImage extracts an ImageBlock from a tool result if present.
func extractImage(result any) *ImageBlock {
	switch v := result.(type) {
	case *builtin.ReadResult:
		if v != nil && v.Image != nil {
			return &ImageBlock{
				MediaType: v.Image.MediaType,
				Data:      v.Image.Data,
				Width:     v.Image.Width,
				Height:    v.Image.Height,
				SizeBytes: v.Image.SizeBytes,
			}
		}
	case builtin.ReadResult:
		if v.Image != nil {
			return &ImageBlock{
				MediaType: v.Image.MediaType,
				Data:      v.Image.Data,
				Width:     v.Image.Width,
				Height:    v.Image.Height,
				SizeBytes: v.Image.SizeBytes,
			}
		}
	}
	return nil
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
