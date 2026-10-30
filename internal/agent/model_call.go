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

// requestIDSeq mints per-model-call request ids for pairing an APIRequestEvent
// with its APIResponseEvent under concurrency. Process-local; uniqueness
// across processes is unnecessary since concurrent model calls only happen
// within one process.
var requestIDSeq atomic.Uint64

func newRequestID() string {
	return fmt.Sprintf("req-%d", requestIDSeq.Add(1))
}

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
	onIssue func([]provider.Message),
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
	// Nothing after this point changes the request payload, so the raw estimate
	// describes exactly what is sent. The provider reuses it for usage
	// calibration instead of estimating the whole conversation again.
	req.RawPromptTokenEstimate = rawPromptTokens
	// The budget has accepted this request and any request-scoped vision strip
	// has already run, so this is the point where the request is actually
	// issued. onIssue (when set) hashes and promotes the outbound messages for
	// cache-prefix diagnostics.
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

// tryNonStreamFirst tries ChatCompletion before streaming. done is false when
// the caller should fall back to streaming.
func tryNonStreamFirst(
	ctx context.Context,
	prov provider.Provider,
	turn int,
	req provider.ChatRequest,
	requestID string,
	events output.EventSink,
	skipNonStream *bool,
) (response provider.ChatResponse, done bool, err error) {
	response, chatErr := prov.ChatCompletion(ctx, req)
	if chatErr == nil {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(response.Message, response.Usage, response.FinishReason, nil), turn, requestID))
		return response, true, nil
	}
	// A usage limit applies to the whole provider; falling back would only
	// spend another request.
	if _, ok := provider.AsUsageLimit(chatErr); ok {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", chatErr), turn, requestID))
		return provider.ChatResponse{}, true, chatErr
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
	if provider.IsStatefulTranscript(prov) {
		emitEvent(events, output.WithAPICallIdentity(output.NewAPIResponseEvent(nil, nil, "", chatErr), turn, requestID))
		return provider.ChatResponse{}, true, chatErr
	}
	return provider.ChatResponse{}, false, nil
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
	// stats is captured by onIssue, which executeChatRequest fires once the
	// request is actually issued. Each issued attempt (including the inline
	// image-strip retry) recomputes and promotes the transformed messages.
	var stats requestCacheStats
	onIssue := func(messages []provider.Message) {
		stats = computeRequestCacheStats(req, messages)
	}
	response, firstChunkTime, err := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream, onIssue)
	if err == nil {
		recordModelUsage(req, response.Usage)
		emitCacheDiagnostic(req, response, "", turn, stats)
		// The provider accepted this request, so it is safe to advance the
		// baseline now. Each baseline key scopes one conversation whose turns
		// run one after another, so promoting here (rather than atomically with
		// the compare in onIssue) cannot race with a sibling's or a later turn's
		// comparison.
		promoteRequestCacheStats(req, stats)
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
	retryResp, retryFirst, retryErr := executeChatRequest(ctx, req.Provider, turn, chatRequest, budget, req.Events, blocks, false, req.StreamingPreferred, skipNonStream, onIssue)
	if retryErr == nil {
		recordModelUsage(req, retryResp.Usage)
		emitCacheDiagnostic(req, retryResp, "", turn, stats)
		// See the comment on the first-attempt promote above: this retry's
		// stats overwrote the earlier (rejected) attempt's stats via the shared
		// onIssue closure, so promoting here still only advances the baseline
		// past an accepted request.
		promoteRequestCacheStats(req, stats)
	}
	return retryResp, retryFirst, retryErr
}

func tokenCount(usage *provider.UsageStats) int {
	if count := provider.UsageCompletionTokenCount(usage); count > 0 {
		return count
	}
	// When no completion token data is available, return 0 instead of estimating
	// the full request. Accumulating input/prompt tokens across turns would cause
	// the session to hit MaxTokens prematurely as the conversation grows.
	return 0
}
