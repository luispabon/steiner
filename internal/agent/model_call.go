package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

var requestIDSeq atomic.Uint64

func newRequestID() string { return fmt.Sprintf("req-%d", requestIDSeq.Add(1)) }

var errRetryTurnForVision = errors.New("retry turn for vision capability change")

type chatExecOptions struct{ prefit *prompt.RequestTokenBudget }

func recordModelUsage(req RunRequest, usage *provider.UsageStats) {
	if req.UsageRecorder == nil || usage == nil {
		return
	}
	req.UsageRecorder.Record(usagestats.Observation{
		ProviderAlias:  req.ResolvedModel.ProviderAlias,
		ProviderType:   string(req.ResolvedModel.EffectiveProviderType),
		BackendModelID: req.ResolvedModel.BackendModelID,
		PromptTokens:   usage.PromptTokens, CompletionTokens: usage.CompletionTokens,
		CacheReadTokens: usage.CacheReadInputTokens, CacheCreateTokens: usage.CacheCreationInputTokens,
		Source: req.UsageSource,
	})
}

func stripImagesIfVisionDisabled(vision *bool, messages []provider.Message, modelAlias string, turn int, events output.EventSink, capabilities *VisionCapabilities) ([]provider.Message, bool) {
	shouldStrip := false
	if capabilities != nil {
		shouldStrip = capabilities.Get(modelAlias) == VisionIncapable
	} else {
		shouldStrip = vision != nil && !*vision
	}
	if !shouldStrip {
		return messages, false
	}
	hasImages := false
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return messages, false
	}
	stripped := make([]provider.Message, len(messages))
	for i, msg := range messages {
		msg.Images = nil
		stripped[i] = msg
	}
	emitEvent(events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{
		Turn: turn, Severity: "warning", Kind: "vision_disabled",
		Message: fmt.Sprintf("model %s does not support vision; image attachments stripped from request", modelAlias),
	}))
	return stripped, true
}

func newAPIRequestEvent(model string, messages []provider.Message, tools []provider.ToolSpec, maxTokens *int, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, estimatedPromptTokens, rawPromptTokens int, isCompaction bool) output.Event {
	event := output.NewAPIRequestEvent(model, messages, tools, maxTokens, blocks, budget, estimatedPromptTokens, rawPromptTokens)
	if isCompaction {
		event = output.WithAPIRequestKind(event, output.APIRequestKindCompaction)
	}
	return event
}

//nolint:unparam // The compatibility wrapper keeps direct callers prefit-free.
func executeChatRequest(ctx context.Context, prov provider.Provider, turn int, req provider.ChatRequest, budget prompt.ModelTokenBudget, events output.EventSink, blocks []prompt.ContextBlock, isCompaction bool, streamingPreferred bool, skipNonStream *bool, onIssue func([]provider.Message)) (provider.ChatResponse, time.Time, error) {
	return executeChatRequestWithOptions(ctx, prov, turn, req, budget, events, blocks, isCompaction, streamingPreferred, skipNonStream, chatExecOptions{}, onIssue)
}

func fitChatRequest(ctx context.Context, req provider.ChatRequest, budget prompt.ModelTokenBudget, isCompaction bool, opts chatExecOptions) (prompt.RequestTokenBudget, int, int, error) {
	if opts.prefit != nil && !isCompaction {
		if err := ctx.Err(); err != nil {
			return prompt.RequestTokenBudget{}, 0, 0, err
		}
		fit := *opts.prefit
		return fit, fit.EstimatedPromptTokens, fit.RawEstimatedPromptTokens, nil
	}
	if budget.ContextSize > 0 {
		var fit prompt.RequestTokenBudget
		var err error
		if isCompaction {
			fit, err = budget.FitCompactionRequest(ctx, req)
		} else {
			fit, err = budget.FitRequest(ctx, req)
		}
		if err != nil {
			return prompt.RequestTokenBudget{}, 0, 0, err
		}
		return fit, fit.EstimatedPromptTokens, fit.RawEstimatedPromptTokens, nil
	}
	estimate, err := provider.EstimateChatRequestTokenEstimate(ctx, req)
	if err != nil {
		return prompt.RequestTokenBudget{}, 0, 0, nil
	}
	return prompt.RequestTokenBudget{}, estimate.Tokens, estimate.RawTokens, nil
}

func executeChatRequestWithOptions(ctx context.Context, prov provider.Provider, turn int, req provider.ChatRequest, budget prompt.ModelTokenBudget, events output.EventSink, blocks []prompt.ContextBlock, isCompaction bool, streamingPreferred bool, skipNonStream *bool, opts chatExecOptions, onIssue func([]provider.Message)) (provider.ChatResponse, time.Time, error) {
	fit, estimatedPromptTokens, rawPromptTokens, err := fitChatRequest(ctx, req, budget, isCompaction, opts)
	if err != nil {
		return provider.ChatResponse{}, time.Time{}, err
	}
	if opts.prefit != nil || budget.ContextSize > 0 {
		if !fit.Fits {
			if isCompaction {
				return provider.ChatResponse{}, time.Time{}, fmt.Errorf("compaction request exceeds context window: %s", fit.String())
			}
			return provider.ChatResponse{}, time.Time{}, fmt.Errorf("request exceeds context window: %s", fit.String())
		}
	}
	req.RawPromptTokenEstimate = rawPromptTokens
	if onIssue != nil {
		onIssue(req.Messages)
	}
	requestID := newRequestID()
	emitEvent(events, output.WithAPICallIdentity(newAPIRequestEvent(req.Model, req.Messages, req.Tools, req.MaxTokens, blocks, budget, estimatedPromptTokens, rawPromptTokens, isCompaction), turn, requestID))
	if !streamingPreferred && (skipNonStream == nil || !*skipNonStream) {
		response, done, err := tryNonStreamFirst(ctx, prov, turn, req, requestID, events, skipNonStream)
		if done {
			return response, time.Time{}, err
		}
	}
	stream, err := prov.StreamChatCompletion(ctx, req)
	if err == nil {
		var firstChunkTime time.Time
		response, streamErr := consumeModelStream(ctx, events, turn, stream, output.ChunkSourceAssistant, &firstChunkTime)
		if streamErr != nil {
			emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", streamErr), turn, requestID))
			return provider.ChatResponse{}, time.Time{}, streamErr
		}
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil), turn, requestID))
		return response, firstChunkTime, nil
	}
	if _, ok := provider.AsUsageLimit(err); ok {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", err), turn, requestID))
		return provider.ChatResponse{}, time.Time{}, err
	}
	response, chatErr := prov.ChatCompletion(ctx, req)
	emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, chatErr), turn, requestID))
	if chatErr != nil {
		return provider.ChatResponse{}, time.Time{}, chatErr
	}
	return response, time.Time{}, nil
}

func tryNonStreamFirst(ctx context.Context, prov provider.Provider, turn int, req provider.ChatRequest, requestID string, events output.EventSink, skipNonStream *bool) (provider.ChatResponse, bool, error) {
	response, chatErr := prov.ChatCompletion(ctx, req)
	if chatErr == nil {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil), turn, requestID))
		return response, true, nil
	}
	if _, ok := provider.AsUsageLimit(chatErr); ok {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", chatErr), turn, requestID))
		return provider.ChatResponse{}, true, chatErr
	}
	if IsStreamRequiredError(chatErr) {
		if skipNonStream != nil {
			*skipNonStream = true
		}
		emitEvent(events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Turn: turn, Severity: "warning", Kind: "stream_required", Message: fmt.Sprintf("model requires streaming; non-stream requests will be skipped in subsequent turns: %v", chatErr)}))
	}
	if provider.IsStatefulTranscript(prov) {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", chatErr), turn, requestID))
		return provider.ChatResponse{}, true, chatErr
	}
	return provider.ChatResponse{}, false, nil
}

// IsStreamRequiredError reports whether err is the provider's stream-required rejection.
func IsStreamRequiredError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *provider.HTTPError
	if !errors.As(err, &httpErr) || httpErr.StatusCode != 400 {
		return false
	}
	body := strings.ToLower(httpErr.Body)
	return strings.Contains(body, "stream") && (strings.Contains(body, "must") || strings.Contains(body, "required"))
}

//nolint:unparam // The compatibility wrapper keeps direct callers prefit-free.
func completeModelCall(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, skipNonStream *bool) (provider.ChatResponse, time.Time, error) {
	return completeModelCallWithOptions(ctx, req, turn, chatRequest, blocks, budget, skipNonStream, chatExecOptions{})
}

func completeModelCallWithOptions(ctx context.Context, req RunRequest, turn int, chatRequest provider.ChatRequest, blocks []prompt.ContextBlock, budget prompt.ModelTokenBudget, skipNonStream *bool, opts chatExecOptions) (provider.ChatResponse, time.Time, error) {
	var imagesStripped bool
	chatRequest.Messages, imagesStripped = stripImagesIfVisionDisabled(req.ResolvedModel.Vision, chatRequest.Messages, req.ResolvedModel.Alias, turn, req.Events, req.VisionCapabilities)
	if imagesStripped {
		opts.prefit = nil
	}
	var stats requestCacheStats
	onIssue := func(messages []provider.Message) { stats = computeRequestCacheStats(req, messages) }
	response, firstChunkTime, err := executeChatRequestWithOptions(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream, opts, onIssue)
	if err == nil {
		recordModelUsage(req, response.Usage)
		emitCacheDiagnostic(req, response, "", turn, stats)
		promoteRequestCacheStats(req, stats)
		return response, firstChunkTime, nil
	}
	if !shouldRetryWithoutImages(err, chatRequest.Messages) {
		return response, firstChunkTime, err
	}
	if req.VisionCapabilities != nil && req.VisionCapabilities.Get(req.ResolvedModel.Alias) != VisionIncapable {
		if changed := req.VisionCapabilities.LatchIncapable(req.ResolvedModel.Alias); changed {
			if req.VisionCapabilities.TakeNotify(req.ResolvedModel.Alias) {
				disposition := "routed"
				if !req.VisionCapabilities.SubAgentConfigured() {
					disposition = "stripped"
				}
				emitEvent(req.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Turn: turn, Severity: "info", Kind: "vision_discovery", Message: fmt.Sprintf("model %s cannot view images; images will be %s", req.ResolvedModel.Alias, disposition)}))
			}
			return provider.ChatResponse{}, time.Time{}, errRetryTurnForVision
		}
	}
	stripped := provider.CloneMessages(chatRequest.Messages)
	for i := range stripped {
		stripped[i].Images = nil
	}
	emitEvent(req.Events, output.NewProviderDiagnosticEvent(output.ProviderDiagnosticEvent{Turn: turn, Severity: "warning", Kind: "vision_fallback", Message: fmt.Sprintf("model %s rejected image attachments with HTTP 400; retrying once without images", req.ResolvedModel.Alias)}))
	chatRequest.Messages = stripped
	retryResp, retryFirst, retryErr := executeChatRequestWithOptions(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream, chatExecOptions{}, onIssue)
	if retryErr == nil {
		recordModelUsage(req, retryResp.Usage)
		emitCacheDiagnostic(req, retryResp, "", turn, stats)
		promoteRequestCacheStats(req, stats)
	}
	return retryResp, retryFirst, retryErr
}

func tokenCount(usage *provider.UsageStats) int {
	if count := provider.UsageCompletionTokenCount(usage); count > 0 {
		return count
	}
	return 0
}
