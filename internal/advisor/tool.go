package advisor

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/tool"
	"github.com/luispabon/steiner/internal/usagestats"
)

// ToolName is the registered provider-facing name of the advisor tool.
const ToolName = "advisor"

// BudgetExhaustedMessage returns the exact model-visible message used when the
// per-session advisor cap is exhausted.
func BudgetExhaustedMessage(used, max int) string {
	return fmt.Sprintf("advisor budget exhausted for this session (%d/%d); proceed on your own judgment", used, max)
}

// ToolDef returns the provider-facing advisor tool definition.
func ToolDef(handler func(context.Context, map[string]any) (any, error)) tool.ToolDef {
	return tool.ToolDef{
		Name:        ToolName,
		Description: "Ask a stronger-model steering advisor for concise strategic guidance. Advisory only: it does not mutate code, execute tools, or act as a sub-agent.",
		ParameterSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "What you want the advisor to judge. Free text.",
				},
				"files": map[string]any{
					"type":        "array",
					"description": "Workspace paths to include verbatim in the advisor's context. Use only when their contents are not already present in the conversation; otherwise they will be duplicated.",
					"items":       map[string]any{"type": "string"},
				},
			},
			"additionalProperties": false,
		},
		Handler: handler,
	}
}

// HandlerDeps holds the runtime dependencies for one advisor tool instance.
type HandlerDeps struct {
	Provider      provider.Provider
	Model         provider.ResolvedModel
	Events        output.EventSink
	Config        Config
	UsageRecorder *usagestats.Recorder
	// WorkDir is the working directory used to render workspace-relative
	// display paths for caller-supplied files.
	WorkDir string
	// PathPolicy resolves and constrains caller-supplied file paths using the
	// same policy the read tool enforces. Required only when a caller passes
	// files.
	PathPolicy *tool.PathPolicy
	// CacheKey is the prompt cache key used for every advisor call made
	// through this handler. When empty, NewHandler mints a fresh one so the
	// package keeps working standalone (e.g. in tests that construct
	// HandlerDeps directly).
	CacheKey string
	// SharedState carries the advisor use counter across every NewHandler
	// call it's passed to, so the budget in Config.MaxUsesPerRun is enforced
	// for the session (the handler's caller decides the shared state's
	// lifetime) instead of resetting each time a new handler is built. When
	// nil, NewHandler allocates a private one, so the package keeps working
	// standalone (e.g. in tests that construct HandlerDeps directly).
	SharedState *SharedState
}

// Config configures the per-session advisor handler.
type Config struct {
	MaxUsesPerRun int
	MaxTokens     *int
}

// SharedState tracks the advisor use counter across every handler built from
// it. Callers that want Config.MaxUsesPerRun enforced across multiple
// NewHandler calls (e.g. one per conversation turn) must pass the same
// *SharedState to HandlerDeps.SharedState each time. Safe for concurrent use.
type SharedState struct {
	mu   sync.Mutex
	uses int
}

// NewSharedState returns a fresh, empty SharedState.
func NewSharedState() *SharedState {
	return &SharedState{}
}

// NewHandler returns a fresh advisor handler backed by deps.SharedState, or a
// private one when deps.SharedState is nil.
func NewHandler(deps HandlerDeps) func(context.Context, map[string]any) (any, error) {
	cacheKey := deps.CacheKey
	if cacheKey == "" {
		cacheKey, _ = provider.NewPromptCacheKey()
	}
	shared := deps.SharedState
	if shared == nil {
		shared = NewSharedState()
	}
	state := &handlerState{shared: shared, cacheKey: cacheKey}
	return func(ctx context.Context, input map[string]any) (any, error) {
		return state.handle(ctx, deps, input)
	}
}

type handlerState struct {
	shared   *SharedState
	cacheKey string
}

func (s *handlerState) handle(ctx context.Context, deps HandlerDeps, input map[string]any) (any, error) {
	in, err := decodeAdvisorInput(input)
	if err != nil {
		return nil, fmt.Errorf("advisor: %w", err)
	}
	snapshot, ok := agent.ConversationSnapshotFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("advisor: live conversation snapshot missing from context")
	}

	files, err := loadAdvisorFiles(deps.WorkDir, deps.PathPolicy, in.Files)
	if err != nil {
		return nil, err
	}
	files = dedupeAgainstSnapshot(files, snapshot, deps.WorkDir)

	s.shared.mu.Lock()
	nextUse := s.shared.uses + 1
	maxUses := deps.Config.MaxUsesPerRun
	if nextUse > maxUses {
		used := s.shared.uses
		s.shared.mu.Unlock()
		message := BudgetExhaustedMessage(used, maxUses)
		emitEvent(deps.Events, output.NewAdvisorBudgetExhaustedEvent(deps.Model.BackendModelID, used, maxUses, message, in.Question, advisorDisplayPaths(files)))
		return message, nil
	}
	s.shared.uses = nextUse
	s.shared.mu.Unlock()

	// Keep the provider-visible tool list stable for the whole run so prompt/KV
	// cache prefixes stay reusable. The per-session cap lives in shared handler
	// state on purpose, even though Anthropic guidance often suggests removing
	// spent tools.
	emitEvent(deps.Events, output.NewAdvisorStartedEvent(deps.Model.BackendModelID, nextUse, maxUses, in.Question, advisorDisplayPaths(files)))
	response, err := advise(ctx, deps.Provider, deps.Model, snapshot, in.Question, files, deps.Config.MaxTokens, deps.Events, s.cacheKey)
	if err != nil {
		emitEvent(deps.Events, output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{
			Model:     deps.Model.BackendModelID,
			UseNumber: nextUse,
			MaxUses:   maxUses,
			Err:       err,
		}))
		return nil, err
	}

	recordAdvisorUsage(deps.UsageRecorder, deps.Model, response.Usage)

	note := strings.TrimSpace(response.Message.Content)
	truncated := response.FinishReason == "length"

	if truncated {
		note += "\n\n[advisor response truncated — raise advisor.max_tokens in config]"
	}
	if note == "" {
		note = "advisor returned no content"
	}

	var cacheReadTokens, cacheCreateTokens, inputTokens int
	if response.Usage != nil {
		cacheReadTokens = response.Usage.CacheReadInputTokens
		cacheCreateTokens = response.Usage.CacheCreationInputTokens
		// HitRate's second argument is the NON-cached portion of the prompt
		// (see usagestats.Report.InputTokens); Usage.PromptTokens is the total.
		inputTokens = response.Usage.NonCachedPromptTokens()
	}

	emitEvent(deps.Events, output.NewAdvisorCompleteEvent(output.AdvisorCompleteParams{
		Model:             deps.Model.BackendModelID,
		UseNumber:         nextUse,
		MaxUses:           maxUses,
		Note:              note,
		Truncated:         truncated,
		CacheReadTokens:   cacheReadTokens,
		CacheCreateTokens: cacheCreateTokens,
		InputTokens:       inputTokens,
		TokenCount:        provider.UsageCompletionTokenCount(response.Usage),
	}))
	return note, nil
}

// recordAdvisorUsage records one observation for a usage-bearing advisor
// response. No-op when the recorder is unset or the response carried no
// usage, mirroring internal/agent's recordModelUsage.
func recordAdvisorUsage(recorder *usagestats.Recorder, model provider.ResolvedModel, usage *provider.UsageStats) {
	if recorder == nil || usage == nil {
		return
	}
	recorder.Record(usagestats.Observation{
		ProviderAlias:     model.ProviderAlias,
		ProviderType:      string(model.EffectiveProviderType),
		BackendModelID:    model.BackendModelID,
		PromptTokens:      usage.PromptTokens,
		CompletionTokens:  usage.CompletionTokens,
		CacheReadTokens:   usage.CacheReadInputTokens,
		CacheCreateTokens: usage.CacheCreationInputTokens,
		Source:            usagestats.SourceAdvisor,
	})
}

func emitEvent(sink output.EventSink, event output.Event) {
	if sink != nil {
		sink.Emit(event)
	}
}

func advise(ctx context.Context, prov provider.Provider, rm provider.ResolvedModel, conversation []provider.Message, question string, files []advisorFile, maxTokens *int, events output.EventSink, cacheKey string) (provider.ChatResponse, error) {
	if prov == nil {
		return provider.ChatResponse{}, fmt.Errorf("advisor: provider is required")
	}
	if strings.TrimSpace(rm.BackendModelID) == "" {
		return provider.ChatResponse{}, fmt.Errorf("advisor: model is required")
	}

	req := provider.ChatRequest{
		Model:               rm.BackendModelID,
		Messages:            buildMessages(conversation, question, files),
		MaxTokens:           maxTokens,
		Params:              rm.Params,
		ExtraParams:         rm.ExtraParams,
		PromptCacheKey:      cacheKey,
		AdvisorCacheProfile: true,
	}
	if rm.ReasoningEffectiveEffort != "" {
		req.Reasoning = &provider.ReasoningRequest{Effort: rm.ReasoningEffectiveEffort}
	}

	if req.Reasoning != nil {
		// Streaming is required for reasoning models. Go directly to streaming
		// so the provider produces thinking chunks.
		stream, err := prov.StreamChatCompletion(ctx, req)
		if err != nil {
			return provider.ChatResponse{}, fmt.Errorf("advisor: %w", err)
		}
		resp, drainErr := streamWithEvents(stream, events)
		if drainErr != nil {
			return provider.ChatResponse{}, fmt.Errorf("advisor: %w", drainErr)
		}
		return resp, nil
	}

	response, err := prov.ChatCompletion(ctx, req)
	if err != nil {
		// Fall back to streaming if the provider rejects non-stream requests.
		if agent.IsStreamRequiredError(err) {
			stream, streamErr := prov.StreamChatCompletion(ctx, req)
			if streamErr != nil {
				return provider.ChatResponse{}, fmt.Errorf("advisor: %w", streamErr)
			}
			resp, drainErr := streamWithEvents(stream, events)
			if drainErr != nil {
				return provider.ChatResponse{}, fmt.Errorf("advisor: %w", drainErr)
			}
			return resp, nil
		}
		return provider.ChatResponse{}, fmt.Errorf("advisor: %w", err)
	}
	return response, nil
}
