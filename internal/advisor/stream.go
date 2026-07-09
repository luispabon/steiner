package advisor

import (
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/provider"
)

// drainStream reads all chunks from a streaming response and assembles them
// into a single ChatResponse. It mirrors agent.consumeModelChunk and
// handleFinalChunk (internal/agent/model_call.go) but without TUI events.
func drainStream(chunks <-chan provider.ChatChunk) (provider.ChatResponse, error) {
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	sawFinal := false

	for chunk := range chunks {
		if chunk.RetryReset {
			response = provider.ChatResponse{}
			message = provider.Message{Role: provider.MessageRoleAssistant}
			sawFinal = false
			continue
		}
		if errText := strings.TrimSpace(chunk.Error); errText != "" {
			if chunk.OriginalError != nil {
				return provider.ChatResponse{}, chunk.OriginalError
			}
			return provider.ChatResponse{}, fmt.Errorf("%s", errText)
		}
		if role := chunk.Delta.Role; role != "" {
			message.Role = role
		}
		if !chunk.Done {
			message.Content += chunk.Delta.Content
			continue
		}
		// Final chunk
		sawFinal = true
		response.Usage = chunk.Usage
		response.FinishReason = chunk.FinishReason
		if content := chunk.Delta.Content; content != "" {
			switch {
			case message.Content == "":
				message.Content = content
			case strings.HasPrefix(content, message.Content):
				message.Content = content
			case strings.HasPrefix(message.Content, content):
				// Final chunk already represented by prior deltas.
			default:
				message.Content += content
			}
		}
	}

	if !sawFinal {
		return provider.ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	response.Message = message
	return response, nil
}
