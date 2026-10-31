package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

//nolint:unparam // source is the assistant stream's tag; keeping it explicit lets another caller stream a different source without editing the consumer.
func consumeModelStream(ctx context.Context, sink output.EventSink, turn int, chunks <-chan provider.ChatChunk, source output.ChunkSource, firstChunkOut *time.Time) (provider.ChatResponse, error) {
	response := provider.ChatResponse{}
	message := provider.Message{Role: provider.MessageRoleAssistant}
	// streamedContent accumulates assistant deltas. It owns the content until
	// the final chunk is reconciled, so appending a delta stays amortized O(1)
	// instead of recopying the whole message per chunk.
	var streamedContent strings.Builder
	sawFinal := false
	var consumeErr error

	for chunk := range chunks {
		if consumeErr != nil {
			// The turn already failed and the request carries a backend state
			// retirement signal, so keep draining until the producer closes the
			// channel: its terminal work, such as retiring backend state, must
			// complete before the caller reads the signal. Chunks after the
			// first error are discarded rather than processed.
			continue
		}
		if err := consumeModelChunk(sink, turn, source, chunk, &response, &message, &streamedContent, &sawFinal, firstChunkOut); err != nil {
			consumeErr = err
			if !provider.HasBackendStateRetirement(ctx) {
				return provider.ChatResponse{}, consumeErr
			}
		}
	}

	if consumeErr != nil {
		return provider.ChatResponse{}, consumeErr
	}
	if !sawFinal {
		if err := ctx.Err(); err != nil {
			return provider.ChatResponse{}, err
		}
		return provider.ChatResponse{}, fmt.Errorf("stream completed without a final chunk")
	}

	// Clone so the stored reply does not keep the streaming builder's slack
	// capacity alive through the returned message.
	message.Content = strings.Clone(message.Content)
	response.Message = message
	return response, nil
}

func consumeModelChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message, streamed *strings.Builder, sawFinal *bool, firstChunkOut *time.Time) error {
	if handleRetryResetChunk(sink, turn, chunk, response, message, streamed, sawFinal) {
		if firstChunkOut != nil {
			*firstChunkOut = time.Time{}
		}
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
		handleStreamingChunk(sink, turn, source, chunk, message, streamed)
		return nil
	}

	*sawFinal = true
	// Reconciliation and content snapshots read the streamed text from the
	// message. Adopt whatever content the final chunk settles on as the
	// accumulator's baseline, so a delta arriving after a final chunk appends to
	// the same text instead of replacing it.
	handleFinalChunk(sink, turn, source, chunk, response, message)
	streamed.Reset()
	streamed.WriteString(message.Content)
	return nil
}

func handleRetryResetChunk(sink output.EventSink, turn int, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message, streamed *strings.Builder, sawFinal *bool) bool {
	if !chunk.RetryReset {
		return false
	}
	*response = provider.ChatResponse{}
	*message = provider.Message{Role: provider.MessageRoleAssistant}
	streamed.Reset()
	*sawFinal = false
	if chunk.Diagnostic != "" {
		emitEvent(sink, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
			Kind:        output.DiagnosticKindRetry,
			Turn:        turn,
			Severity:    chunk.Severity,
			Message:     chunk.Diagnostic,
			Attempt:     chunk.RetryAttempt,
			MaxAttempts: chunk.RetryMaxAttempts,
			Delay:       chunk.RetryDelay.String(),
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
		if chunk.OriginalError != nil {
			return true, chunk.OriginalError
		}
		return true, fmt.Errorf("%s", errText)
	}
	return false, nil
}

func handleStreamingChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, message *provider.Message, streamed *strings.Builder) {
	if thinking := chunk.Thinking; thinking != "" {
		emitEvent(sink, output.NewThinkingChunkEventWithSource(turn, thinking, source))
	}
	if content := chunk.Delta.Content; content != "" {
		// WriteString appends in amortized O(1) and String() is a view over the
		// builder's buffer, so keeping message.Content in sync per chunk costs no
		// copy while final-chunk reconciliation still sees the streamed text.
		streamed.WriteString(content)
		message.Content = streamed.String()
		emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
	}
	if len(chunk.Delta.ToolCalls) > 0 {
		message.ToolCalls = provider.CloneToolCalls(chunk.Delta.ToolCalls)
	}
}

// emitUnseenFinalContent surfaces final text that never streamed. The TUI
// renders streamed chunks and discards the finalized message when any chunk
// arrived, so a final that extends the streamed text must emit its suffix or
// the tail reaches history but never the screen. A final that does not extend
// the streamed text cannot be placed in order; it is logged (lengths only, no
// content) so the loss is observable instead of silent.
func emitUnseenFinalContent(sink output.EventSink, turn int, source output.ChunkSource, streamed, final string) {
	switch {
	case strings.HasPrefix(streamed, final):
		// Nothing unseen: the stream already carried all of the final text.
	case strings.HasPrefix(final, streamed):
		emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, final[len(streamed):], source))
	default:
		slog.Warn("final assistant content diverges from streamed content; not re-emitted",
			"turn", turn, "streamed_len", len(streamed), "final_len", len(final))
	}
}

// handleFinalChunk reconciles the final chunk into the response and message. It
// may replace message.Content outright, for a content snapshot or a final that
// extends the streamed text, so a caller holding a streaming builder must
// re-seed that builder from message.Content afterwards; consumeModelChunk does
// so, which keeps a delta arriving after the final chunk appending to the
// reconciled text.
func handleFinalChunk(sink output.EventSink, turn int, source output.ChunkSource, chunk provider.ChatChunk, response *provider.ChatResponse, message *provider.Message) {
	response.Usage = chunk.Usage
	response.FinishReason = chunk.FinishReason
	response.UpstreamEndpoint = chunk.UpstreamEndpoint
	response.BackendState = chunk.BackendState
	response.ClearBackendState = chunk.ClearBackendState
	content := chunk.Delta.Content
	if chunk.ContentSnapshot {
		streamed := message.Content
		message.Content = content
		emitUnseenFinalContent(sink, turn, source, streamed, content)
	} else if content != "" {
		switch {
		case message.Content == "":
			message.Content = content
			emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
		case strings.HasPrefix(content, message.Content):
			streamed := message.Content
			message.Content = content
			emitUnseenFinalContent(sink, turn, source, streamed, content)
		case strings.HasPrefix(message.Content, content):
			// Final chunk already represented by prior deltas.
		default:
			message.Content += content
			emitEvent(sink, output.NewAssistantChunkEventWithSource(turn, content, source))
		}
	}
	if len(chunk.Delta.ToolCalls) > 0 {
		message.ToolCalls = provider.CloneToolCalls(chunk.Delta.ToolCalls)
	}
	if chunk.Delta.ReasoningContent != "" {
		message.ReasoningContent = chunk.Delta.ReasoningContent
	}
	if chunk.Delta.ProviderMetadata != nil {
		message.ProviderMetadata = provider.CloneMessageMetadata(chunk.Delta.ProviderMetadata)
	}
}
