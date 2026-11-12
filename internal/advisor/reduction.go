package advisor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
)

// reductionTuning holds the effective soft limits for one advisor request.
type reductionTuning struct {
	enabled       bool
	trigger       int
	target        int
	tail          int
	outputReserve int
}

// resolveReductionTuning derives the configured trigger, capped by the advisor
// model's context window when one is known. The cap leaves room for the
// configured output reserve and the provider estimator pad.
func resolveReductionTuning(cfg Config, model provider.ResolvedModel) reductionTuning {
	configured := cfg.ContextReductionThresholdTokens
	if configured <= 0 {
		return reductionTuning{}
	}
	reserve := advisorOutputReserve(cfg.MaxTokens, model)
	trigger := configured
	if window := model.EffectiveLimits.ContextWindow; window > 0 {
		pad := max(model.EffectiveLimits.EstimatorPadTokens, 0)
		budget := window - reserve - pad
		if budget > 0 {
			cap := int(math.Floor(0.6 * float64(budget)))
			if cap > 0 && cap < trigger {
				trigger = cap
			}
		}
	}
	return reductionTuning{
		enabled:       true,
		trigger:       trigger,
		target:        int(math.Floor(0.75 * float64(trigger))),
		tail:          int(math.Ceil(0.25 * float64(trigger))),
		outputReserve: reserve,
	}
}

func advisorOutputReserve(configured *int, model provider.ResolvedModel) int {
	if configured != nil && *configured > 0 {
		return *configured
	}
	if model.EffectiveLimits.MaxOutputTokens > 0 {
		return model.EffectiveLimits.MaxOutputTokens
	}
	return provider.DefaultMaxOutputTokens
}

func minReductionSavings(trigger int) int {
	if trigger <= 0 {
		return 0
	}
	return max(1, (trigger+9)/10)
}

func reductionScopeKey(deps HandlerDeps, cacheKey string, tuning reductionTuning) string {
	return strings.Join([]string{
		"cache=" + cacheKey,
		"transport=" + deps.TransportSession,
		"parent=" + deps.ParentTransportSession,
		"provider=" + deps.Model.ProviderAlias,
		"model=" + deps.Model.BackendModelID,
		"alias=" + deps.Model.Alias,
		fmt.Sprintf("trigger=%d", tuning.trigger),
		fmt.Sprintf("reserve=%d", tuning.outputReserve),
	}, "\x00")
}

func splitTranscript(snapshot []provider.Message) ([]provider.Message, *provider.Message) {
	if n := len(snapshot); n > 0 && snapshot[n-1].Role == provider.MessageRoleAssistant {
		inFlight := snapshot[n-1]
		return snapshot[:n-1], &inFlight
	}
	return snapshot, nil
}

func rebuildSnapshot(transcript []provider.Message, inFlight *provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(transcript)+1)
	out = append(out, transcript...)
	if inFlight != nil {
		out = append(out, *inFlight)
	}
	return out
}

func requestContentBytes(messages []provider.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Content)
	}
	return total
}

type advisorTokenEstimate struct {
	tokens   int
	strategy string
}

// estimateAdvisorMessages estimates the complete advisor request with the
// advisor model. Any estimator failure falls back to a local byte heuristic
// and never blocks the provider call.
func estimateAdvisorMessages(ctx context.Context, deps HandlerDeps, messages []provider.Message) advisorTokenEstimate {
	request := provider.ChatRequest{
		Model:       deps.Model.BackendModelID,
		Messages:    messages,
		MaxTokens:   deps.Config.MaxTokens,
		Params:      deps.Model.Params,
		ExtraParams: deps.Model.ExtraParams,
	}
	estimate, err := provider.EstimateChatRequestTokenEstimate(ctx, request)
	if err != nil || estimate.Tokens <= 0 {
		return advisorTokenEstimate{tokens: fallbackRequestTokens(messages), strategy: "fallback"}
	}
	strategy := estimate.Strategy
	if strategy == "" {
		strategy = "unknown"
	}
	return advisorTokenEstimate{tokens: estimate.Tokens, strategy: strategy}
}

func fallbackRequestTokens(messages []provider.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.Role) + len(message.Content) + len(message.Name) + len(message.ToolCallID) + len(message.ReasoningContent)
		for _, call := range message.ToolCalls {
			total += len(call.ID) + len(call.Name) + fallbackArgumentsBytes(call.Arguments)
		}
		for _, image := range message.Images {
			total += len(image.Data) + len(image.MediaType) + 85
		}
	}
	return (total + 2) / 3
}

func fallbackArgumentsBytes(arguments map[string]any) int {
	data, err := json.Marshal(arguments)
	if err != nil {
		return len(fmt.Sprintf("%v", arguments))
	}
	return len(data)
}

// advisorReductionDiagnostics records the estimate and stages for one handler
// call. It is surfaced on the existing advisor_complete event.
type advisorReductionDiagnostics struct {
	Strategy     string
	Estimator    string
	BeforeTokens int
	AfterTokens  int
	Stages       []string
}

type reductionOutcome struct {
	messages    []provider.Message
	applied     bool
	diagnostics advisorReductionDiagnostics
}

func reductionParams(diagnostics advisorReductionDiagnostics) *output.AdvisorReductionInfo {
	if diagnostics.Strategy == "" && diagnostics.Estimator == "" && diagnostics.BeforeTokens == 0 && diagnostics.AfterTokens == 0 && len(diagnostics.Stages) == 0 {
		return nil
	}
	return &output.AdvisorReductionInfo{
		Strategy:     diagnostics.Strategy,
		Estimator:    diagnostics.Estimator,
		BeforeTokens: diagnostics.BeforeTokens,
		AfterTokens:  diagnostics.AfterTokens,
		Stages:       diagnostics.Stages,
	}
}

// reduceAdvisorMessages replays any frozen replacements for this conversation
// scope, decides whether the request crosses the soft trigger, and plans a new
// append-only batch when it does. It never rejects the call.
func (s *handlerState) reduceAdvisorMessages(ctx context.Context, deps HandlerDeps, snapshot []provider.Message, fullFiles, legacyFiles []advisorFile, question string, legacyMessages []provider.Message) reductionOutcome {
	tuning := resolveReductionTuning(deps.Config, deps.Model)
	if !tuning.enabled {
		return reductionOutcome{messages: legacyMessages}
	}
	transcript, inFlight := splitTranscript(snapshot)
	scope := reductionScopeKey(deps, s.cacheKey, tuning)
	state := s.shared.reductionLedger(scope)
	replay := replayReductionLedger(transcript, state.entries)

	if replay.valid < len(state.entries) {
		next := reductionLedgerState{entries: state.entries[:replay.valid]}
		if s.shared.storeReductionLedger(scope, state.epoch, next) {
			state.epoch++
			state.entries = next.clone().entries
			state.exhausted = false
			state.exhaustedTokens = 0
		} else {
			state = s.shared.reductionLedger(scope)
			replay = replayReductionLedger(transcript, state.entries)
		}
	}

	currentSnapshot := snapshot
	currentFiles := legacyFiles
	currentMessages := legacyMessages
	if len(state.entries) > 0 {
		currentSnapshot = rebuildSnapshot(replay.transcript, inFlight)
		currentFiles = dedupeAttachments(fullFiles, replay.transcript, replay.stages, deps.WorkDir)
		currentMessages = buildMessages(currentSnapshot, question, currentFiles)
	}

	estimate := estimateAdvisorMessages(ctx, deps, currentMessages)
	diagnostics := advisorReductionDiagnostics{
		Strategy:     "none",
		Estimator:    estimate.strategy,
		BeforeTokens: estimate.tokens,
		AfterTokens:  estimate.tokens,
	}
	if len(state.entries) > 0 {
		diagnostics.Strategy = "replay"
	}
	if estimate.tokens < tuning.trigger {
		return reductionOutcome{messages: currentMessages, applied: len(state.entries) > 0, diagnostics: diagnostics}
	}

	if state.exhausted && estimate.tokens < state.exhaustedTokens+minReductionSavings(tuning.trigger) {
		diagnostics.Strategy = "exhausted"
		return reductionOutcome{messages: currentMessages, applied: len(state.entries) > 0, diagnostics: diagnostics}
	}

	ratio := 1.0
	if contentBytes := requestContentBytes(currentMessages); contentBytes > 0 && estimate.tokens > 0 {
		ratio = float64(estimate.tokens) / float64(contentBytes)
	}
	// Planned savings are measured against all requested files at full size,
	// so reducing a read does not depend on whether its attachment happens to
	// be deduped in the current payload.
	planningBaseline := estimate.tokens
	if fileDelta := len(renderAdvisorFiles(fullFiles)) - len(renderAdvisorFiles(currentFiles)); fileDelta > 0 {
		planningBaseline += tokensForBytes(fileDelta, ratio)
	}
	plan := planReduction(reductionPlanInput{
		transcript:    replay.transcript,
		stages:        replay.stages,
		tuning:        tuning,
		ratio:         ratio,
		initialTokens: planningBaseline,
	})

	if plan.savedTokens < minReductionSavings(tuning.trigger) || len(plan.appliedStages) == 0 {
		next := reductionLedgerState{entries: state.entries, exhausted: true, exhaustedTokens: estimate.tokens}
		s.shared.storeReductionLedger(scope, state.epoch, next)
		diagnostics.Strategy = "exhausted"
		return reductionOutcome{messages: currentMessages, applied: len(state.entries) > 0, diagnostics: diagnostics}
	}

	entries := make([]reductionLedgerEntry, len(replay.hashes))
	for i := range replay.hashes {
		entries[i] = reductionLedgerEntry{sourceHash: replay.hashes[i], stage: plan.stages[i]}
		if plan.stages[i] != reductionStageNone {
			entries[i].replacement = plan.transcript[i].Content
		}
	}
	if !s.shared.storeReductionLedger(scope, state.epoch, reductionLedgerState{entries: entries}) {
		diagnostics.Strategy = "concurrent"
		return reductionOutcome{messages: currentMessages, applied: len(state.entries) > 0, diagnostics: diagnostics}
	}

	finalSnapshot := rebuildSnapshot(plan.transcript, inFlight)
	finalFiles := dedupeAttachments(fullFiles, plan.transcript, plan.stages, deps.WorkDir)
	finalMessages := buildMessages(finalSnapshot, question, finalFiles)
	finalEstimate := estimateAdvisorMessages(ctx, deps, finalMessages)
	diagnostics.Strategy = "reduced"
	diagnostics.AfterTokens = finalEstimate.tokens
	diagnostics.Stages = plan.appliedStages
	return reductionOutcome{messages: finalMessages, applied: true, diagnostics: diagnostics}
}
