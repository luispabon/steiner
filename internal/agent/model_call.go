package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// errRetryTurnForVision is a sentinel error returned by completeModelCall when
// vision capability is discovered to be incapable at runtime and latched.
// The turn-level retry is triggered in turn_progression.go's executeModelCall.
var errRetryTurnForVision = errors.New("retry turn for vision capability change")

// recordModelUsage records one observation for a usage-bearing response.
// No-op when the recorder is unset or the response carried no usage, so each
// usage-bearing response is recorded exactly once and failed/nil-usage calls
// record nothing.
func recordModelUsage(req RunRequest, usage *provider.UsageStats) {
	if req.UsageRecorder == nil || usage == nil {
		return
	}
	req.UsageRecorder.Record(usagestats.Observation{
		ProviderAlias:     req.ResolvedModel.ProviderAlias,
		ProviderType:      string(req.ResolvedModel.EffectiveProviderType),
		BackendModelID:    req.ResolvedModel.BackendModelID,
		PromptTokens:      usage.PromptTokens,
		CompletionTokens:  usage.CompletionTokens,
		CacheReadTokens:   usage.CacheReadInputTokens,
		CacheCreateTokens: usage.CacheCreationInputTokens,
		Source:            req.UsageSource,
	})
}

func stripImagesIfVisionDisabled(vision *bool, messages []provider.Message, modelAlias string, turn int, events output.EventSink, capabilities *VisionCapabilities) []provider.Message {
	shouldStrip := false
	if capabilities != nil {
		shouldStrip = capabilities.Get(modelAlias) == VisionIncapable
	} else {
		// Fallback to old behavior for backward compatibility when holder is nil
		shouldStrip = vision != nil && !*vision
	}

	if !shouldStrip {
		return messages
	}

	hasImages := false
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return messages
	}

	stripped := make([]provider.Message, len(messages))
	for i, msg := range messages {
		msg.Images = nil
		stripped[i] = msg
	}
	emitEvent(events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     turn,
		Severity: "warning",
		Kind:     "vision_disabled",
		Message:  fmt.Sprintf("model %s does not support vision; image attachments stripped from request", modelAlias),
	}))
	return stripped
}

// newAPIRequestEvent builds the API request event for a turn, marking it as a
// compaction request when isCompaction is set so context reports can label it.
func newAPIRequestEvent(model string, messages []provider.Message, tools []provider.ToolSpec, maxTokens *int, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, estimatedPromptTokens, rawPromptTokens int, isCompaction bool) output.Event {
	event := output.NewAPIRequestEvent(model, messages, tools, maxTokens, blocks, budget, estimatedPromptTokens, rawPromptTokens)
	if isCompaction {
		event = output.WithAPIRequestKind(event, output.APIRequestKindCompaction)
	}
	return event
}

func estimatePromptTokensForEvent(ctx context.Context, req provider.ChatRequest) (int, int) {
	estimate, err := provider.EstimateChatRequestTokenEstimate(ctx, req)
	if err != nil {
		return 0, 0
	}
	return estimate.Tokens, estimate.RawTokens
}

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
	skipNonStream *bool,
) (provider.ChatResponse, time.Time, error) {
	var estimatedPromptTokens, rawPromptTokens int
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
		estimatedPromptTokens = fit.EstimatedPromptTokens
		rawPromptTokens = fit.RawEstimatedPromptTokens
	} else {
		estimatedPromptTokens, rawPromptTokens = estimatePromptTokensForEvent(ctx, req)
	}
	emitEvent(events, newAPIRequestEvent(req.Model, req.Messages, req.Tools, req.MaxTokens, blocks, budget, estimatedPromptTokens, rawPromptTokens, isCompaction))

	// When streaming is not preferred, try ChatCompletion first and only fall
	// back to streaming if it is unavailable.
	if !streamingPreferred && (skipNonStream == nil || !*skipNonStream) {
		response, chatErr := prov.ChatCompletion(ctx, req)
		if chatErr == nil {
			emitEvent(events, output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil))
			return response, time.Time{}, nil
		}
		// Detect "stream required" 400 error and mark it for future turns.
		if IsStreamRequiredError(chatErr) {
			if skipNonStream != nil {
				*skipNonStream = true
			}
			emitEvent(events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
				Turn:     turn,
				Severity: "warning",
				Kind:     "stream_required",
				Message:  fmt.Sprintf("model requires streaming; non-stream requests will be skipped in subsequent turns: %v", chatErr),
			}))
		}
		// Fall through to streaming when ChatCompletion fails.
	}

	stream, err := prov.StreamChatCompletion(ctx, req)
	if err == nil {
		var firstChunkTime time.Time
		response, streamErr := consumeModelStream(ctx, events, turn, stream, output.ChunkSourceAssistant, &firstChunkTime)
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

// IsStreamRequiredError reports whether err is the provider's "this model only
// supports streaming" rejection. Some Codex models (e.g. gpt-5.4-mini) 400 on
// non-streaming Responses requests with a body like "Stream must be set to
// true". executeChatRequest uses this to latch skipNonStream so later turns go
// straight to streaming instead of wasting one failed non-stream request per
// turn (which also hid the error and hurt cache stats). Keep the match narrow
// (400 + "stream" + must/required) so unrelated 400s still surface normally.
func IsStreamRequiredError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	if httpErr.StatusCode != 400 {
		return false
	}
	// Check if the error message indicates streaming is required.
	return strings.Contains(strings.ToLower(httpErr.Body), "stream") &&
		(strings.Contains(strings.ToLower(httpErr.Body), "must") || strings.Contains(strings.ToLower(httpErr.Body), "required"))
}

func completeModelCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, skipNonStream *bool) (provider.ChatResponse, time.Time, error) {
	chatRequest.Messages = stripImagesIfVisionDisabled(req.ResolvedModel.Vision, chatRequest.Messages, req.ResolvedModel.Alias, turn, req.Events, req.VisionCapabilities)
	response, firstChunkTime, err := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream)
	if err == nil {
		recordModelUsage(req, response.Usage)
		return response, firstChunkTime, nil
	}
	if !shouldRetryWithoutImages(err, chatRequest.Messages) {
		return response, firstChunkTime, err
	}

	// If we have a capabilities holder and the model is not already known to be incapable,
	// latch it as incapable and return the sentinel error to trigger a turn-level retry.
	// This allows the model call to be re-attempted with images pre-stripped based on the latch.
	if req.VisionCapabilities != nil && req.VisionCapabilities.Get(req.ResolvedModel.Alias) != VisionIncapable {
		if changed := req.VisionCapabilities.LatchIncapable(req.ResolvedModel.Alias); changed {
			if req.VisionCapabilities.TakeNotify(req.ResolvedModel.Alias) {
				disposition := "routed"
				if !req.VisionCapabilities.SubAgentConfigured() {
					disposition = "stripped"
				}
				emitEvent(req.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
					Turn:     turn,
					Severity: "info",
					Kind:     "vision_discovery",
					Message:  fmt.Sprintf("model %s cannot view images; images will be %s", req.ResolvedModel.Alias, disposition),
				}))
			}
			return provider.ChatResponse{}, time.Time{}, errRetryTurnForVision
		}
		// Already latched to incapable; fall through to inline retry below
	}

	// Fallback inline retry for when capabilities holder is nil or already latched:
	// strip images and retry the provider call directly.
	stripped := provider.CloneMessages(chatRequest.Messages)
	for i := range stripped {
		stripped[i].Images = nil
	}
	emitEvent(req.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn:     turn,
		Severity: "warning",
		Kind:     "vision_fallback",
		Message:  fmt.Sprintf("model %s rejected image attachments with HTTP 400; retrying once without images", req.ResolvedModel.Alias),
	}))
	chatRequest.Messages = stripped
	retryResp, retryFirst, retryErr := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream)
	if retryErr == nil {
		recordModelUsage(req, retryResp.Usage)
	}
	return retryResp, retryFirst, retryErr
}

func shouldRetryWithoutImages(err error, messages []provider.Message) bool {
	if len(messages) == 0 || !requestHasImages(messages) {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == 400
}

func requestHasImages(messages []provider.Message) bool {
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			return true
		}
	}
	return false
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
		if chunk.OriginalError != nil {
			return true, chunk.OriginalError
		}
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
		message.ToolCalls = provider.CloneToolCalls(chunk.Delta.ToolCalls)
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
		message.ToolCalls = provider.CloneToolCalls(chunk.Delta.ToolCalls)
	}
	if chunk.Delta.ReasoningContent != "" {
		message.ReasoningContent = chunk.Delta.ReasoningContent
	}
	if chunk.Delta.ProviderMetadata != nil {
		message.ProviderMetadata = provider.CloneMessageMetadata(chunk.Delta.ProviderMetadata)
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
