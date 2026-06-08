package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

func executeChatRequest(
	ctx context.Context,
	prov provider.Provider,
	turn int,
	req provider.ChatRequest,
	budget prompt.ModelTokenBudget,
	events output.EventSink,
	blocks []prompt.ContextBlock,
	isCompaction bool,
	streamingPreferred bool,
	source output.ChunkSource,
) (provider.ChatResponse, time.Time, error) {
	if budget.ContextSize > 0 {
		var fit prompt.RequestTokenBudget
		var err error
		if isCompaction {
			fit, err = budget.FitCompactionRequest(ctx, req)
		} else {
			fit, err = budget.FitRequest(ctx, req)
		}
		if err != nil {
			return provider.ChatResponse{}, time.Time{}, err
		}
		if !fit.Fits {
			if isCompaction {
				return provider.ChatResponse{}, time.Time{}, fmt.Errorf("compaction request exceeds context window: %s", fit.String())
			}
			return provider.ChatResponse{}, time.Time{}, fmt.Errorf("request exceeds context window: %s", fit.String())
		}
	}
	emitEvent(events, output.NewAPIRequestEvent(req.Model, req.Messages, req.Tools, req.MaxTokens, blocks, budget))

	// When streaming is not preferred, try ChatCompletion first and only fall
	// back to streaming if it is unavailable.
	if !streamingPreferred {
		response, chatErr := prov.ChatCompletion(ctx, req)
		if chatErr == nil {
			emitEvent(events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
			return response, time.Time{}, nil
		}
		// Fall through to streaming when ChatCompletion fails.
	}

	stream, err := prov.StreamChatCompletion(ctx, req)
	if err == nil {
		var firstChunkTime time.Time
		response, streamErr := consumeModelStream(ctx, events, turn, stream, source, &firstChunkTime)
		if streamErr != nil {
			emitEvent(events, output.NewAPIResponseEvent(nil, nil, "", streamErr))
			return provider.ChatResponse{}, time.Time{}, streamErr
		}
		emitEvent(events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
		return response, firstChunkTime, nil
	}

	response, chatErr := prov.ChatCompletion(ctx, req)
	emitEvent(events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, chatErr))
	if chatErr != nil {
		return provider.ChatResponse{}, time.Time{}, chatErr
	}
	return response, time.Time{}, nil
}

func completeModelCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget) (provider.ChatResponse, time.Time, error) {
	return executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, output.ChunkSourceAssistant)
}

func consumeModelStream(_ context.Context, sink output.EventSink, turn int, chunks <-chan provider.ChatChunk, source output.ChunkSource, firstChunkOut *time.Time) (provider.ChatResponse, error) {
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	sawFinal := false

	for chunk := range chunks {
		if err := consumeModelChunk(sink, turn, source, chunk, &response, &message, &sawFinal, firstChunkOut); err != nil {
			return provider.ChatResponse{}, err
		}
	}

	if !sawFinal {
		return provider.ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	response.Message = message
	return response, nil
}

func consumeModelChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message, sawFinal *bool, firstChunkOut *time.Time) error {
	if handleRetryResetChunk(sink, turn, chunk, response, message, sawFinal) {
		return nil
	}
	if handleDiagnosticChunk(sink, turn, chunk) {
		return nil
	}
	if handled, err := handleErrorChunk(chunk); handled || err != nil {
		return err
	}
	if role := chunk.Delta.Role; role != "" {
		message.Role = role
	}
	if !chunk.Done {
		// Capture first-chunk timestamp on first content or thinking chunk
		if firstChunkOut != nil && firstChunkOut.IsZero() {
			if chunk.Thinking != "" || chunk.Delta.Content != "" {
				*firstChunkOut = time.Now()
			}
		}
		handleStreamingChunk(sink, turn, source, chunk, message)
		return nil
	}

	*sawFinal = true
	handleFinalChunk(sink, turn, source, chunk, response, message)
	return nil
}

func handleRetryResetChunk(sink output.EventSink, turn int, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message, sawFinal *bool) bool {
	if !chunk.RetryReset {
		return false
	}
	*response = provider.ChatResponse{}
	*message = provider.Message{Role: provider.MessageRoleAssistant}
	*sawFinal = false
	if chunk.Diagnostic != "" {
		emitEvent(sink, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
			Turn:     turn,
			Severity: chunk.Severity,
			Message:  chunk.Diagnostic,
		}))
	}
	return true
}

func handleDiagnosticChunk(sink output.EventSink, turn int, chunk provider.ChatChunk) bool {
	if chunk.Diagnostic == "" {
		return false
	}
	emitEvent(sink, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     turn,
		Severity: chunk.Severity,
		Message:  chunk.Diagnostic,
	}))
	return true
}

func handleErrorChunk(chunk provider.ChatChunk) (bool, error) {
	if errText := strings.TrimSpace(chunk.Error); errText != "" {
		return true, fmt.Errorf("%s", errText)
	}
	return false, nil
}

func handleStreamingChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, message *provider.Message) {
	if thinking := chunk.Thinking; thinking != "" {
		emitEvent(sink, output.NewThinkingChunkEventWithSource(turn, thinking, source))
	}
	if content := chunk.Delta.Content; content != "" {
		message.Content += content
		emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
	}
	if len(chunk.Delta.ToolCalls) > 0 {
		message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
	}
}

func handleFinalChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message) {
	response.Usage = chunk.Usage
	response.FinishReason = chunk.FinishReason
	if content := chunk.Delta.Content; content != "" {
		switch {
		case message.Content == "":
			message.Content = content
			emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
		case strings.HasPrefix(content, message.Content):
			message.Content = content
		case strings.HasPrefix(message.Content, content):
			// Final chunk already represented by prior deltas.
		default:
			message.Content += content
			emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
		}
	}
	if len(chunk.Delta.ToolCalls) > 0 {
		message.ToolCalls = cloneProviderToolCalls(chunk.Delta.ToolCalls)
	}
	if chunk.Delta.ReasoningContent != "" {
		message.ReasoningContent = chunk.Delta.ReasoningContent
	}
}

func tokenCount(_ context.Context, _ provider.ChatRequest, usage *provider.UsageStats) int {
	if count := provider.UsageCompletionTokenCount(usage); count > 0 {
		return count
	}
	// When no completion token data is available, return 0 instead of estimating
	// the full request. Accumulating input/prompt tokens across turns would cause
	// the session to hit MaxTokens prematurely as the conversation grows.
	return 0
}
