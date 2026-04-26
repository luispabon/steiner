package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func completeModelCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget) (provider.ChatResponse, error) {
	if budget.ContextSize > 0 {
		fit, err := budget.FitRequest(ctx, chatRequest)
		if err != nil {
			return provider.ChatResponse{}, err
		}
		if !fit.Fits {
			return provider.ChatResponse{}, fmt.Errorf("request exceeds context window: %s", fit.String())
		}
	}
	emitEvent(req.Events, output.NewAPIRequestEvent(chatRequest.Model, chatRequest.Messages, chatRequest.Tools, chatRequest.MaxTokens, blocks, budget))

	stream, err := req.Provider.StreamChatCompletion(ctx, chatRequest)
	if err == nil {
		response, streamErr := consumeModelStream(ctx, req.Events, turn, stream)
		if streamErr != nil {
			emitEvent(req.Events, output.NewAPIResponseEvent(nil, nil, "", streamErr))
			return provider.ChatResponse{}, streamErr
		}
		emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
		return response, nil
	}

	response, chatErr := req.Provider.ChatCompletion(ctx, chatRequest)
	emitEvent(req.Events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, chatErr))
	if chatErr != nil {
		return provider.ChatResponse{}, chatErr
	}
	return response, nil
}

func consumeModelStream(ctx context.Context, sink output.EventSink, turn int, chunks <-chan provider.ChatChunk) (provider.ChatResponse, error) {
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	sawFinal := false

	for chunk := range chunks {
		if errText := strings.TrimSpace(chunk.Error); errText != "" {
			return provider.ChatResponse{}, fmt.Errorf("%s", errText)
		}
		if role := chunk.Delta.Role; role != "" {
			message.Role = role
		}
		if !chunk.Done {
			if thinking := chunk.Thinking; thinking != "" {
				emitEvent(sink, output.NewThinkingChunkEvent(turn, thinking))
			}
			if content := chunk.Delta.Content; content != "" {
				message.Content += content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			}
			if len(chunk.Delta.ToolCalls) > 0 {
				message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
			}
			continue
		}
		sawFinal = true
		response.Usage = chunk.Usage
		response.FinishReason = chunk.FinishReason
		if content := chunk.Delta.Content; content != "" {
			switch {
			case message.Content == "":
				message.Content = content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			case strings.HasPrefix(content, message.Content):
				message.Content = content
			case strings.HasPrefix(message.Content, content):
				// Final chunk already represented by prior deltas.
			default:
				message.Content += content
				emitEvent(sink, output.NewAssistantChunkEvent(turn, content))
			}
		}
		if len(chunk.Delta.ToolCalls) > 0 {
			message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
		}
	}

	if !sawFinal {
		return provider.ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	response.Message = message
	return response, nil
}

func tokenCount(ctx context.Context, request provider.ChatRequest, usage *provider.UsageStats) int {
	if count := provider.UsageTokenCount(usage); count > 0 {
		return count
	}
	estimate, err := provider.EstimateChatRequestTokens(ctx, request)
	if err != nil {
		return 0
	}
	return estimate
}
