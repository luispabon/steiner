package agent

import (
	"encoding/json"
	"fmt"

	"github.com/luispabon/steiner/internal/tool"
)

// ToolResultEnvelope normalizes tool output plus metadata for agent messages.
type ToolResultEnvelope struct {
	Content   string
	Metadata  tool.ExecutionMetadata
	Retention *MessageRetention
}

func normalizeToolResult(result any) ToolResultEnvelope {
	switch v := result.(type) {
	case tool.ExecutionResult:
		return ToolResultEnvelope{
			Content:   toolResultContent(v.Value),
			Metadata:  v.Metadata,
			Retention: messageRetentionFromToolRetention(v.Retention),
		}
	default:
		return ToolResultEnvelope{
			Content: toolResultContent(result),
		}
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
