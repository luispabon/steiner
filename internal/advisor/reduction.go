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

// reductionRequest carries the immutable inputs of one advisor reduction pass.
type reductionRequest struct {
	snapshot []provider.Message
	files    []advisorFile
	question string
}

type reductionOutcome struct {
	// messages is the final request payload, reduced or not.
	messages    []provider.Message
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
func (s *handlerState) reduceAdvisorMessages(ctx context.Context, deps HandlerDeps, req reductionRequest) reductionOutcome {
	tuning := resolveReductionTuning(deps.Config, deps.Model)
	if !tuning.enabled {
		_, messages := legacyReductionPayload(deps, req)
		return reductionOutcome{messages: messages}
	}
	transcript, inFlight := splitTrailingAssistant(req.snapshot)
	scope := reductionScopeKey(deps, s.cacheKey, tuning)
	state, replay := s.replayReductionState(scope, transcript)
	currentFiles, currentMessages := currentReductionPayload(deps, req, replay, inFlight, len(state.entries) > 0)

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
		return reductionOutcome{messages: currentMessages, diagnostics: diagnostics}
	}
	if state.exhausted && estimate.tokens < state.exhaustedTokens+minReductionSavings(tuning.trigger) && state.coversTranscript(&replay) {
		diagnostics.Strategy = "exhausted"
		return reductionOutcome{messages: currentMessages, diagnostics: diagnostics}
	}

	ratio := reductionRatio(estimate.tokens, currentMessages)
	plan := planReduction(reductionPlanInput{
		transcript:    replay.transcript,
		stages:        replay.stages,
		references:    replay.references,
		tuning:        tuning,
		ratio:         ratio,
		initialTokens: planningBaselineTokens(estimate.tokens, ratio, req.files, currentFiles),
	})
	if plan.savedTokens < minReductionSavings(tuning.trigger) || len(plan.appliedStages) == 0 {
		s.shared.storeReductionLedger(scope, state.epoch, reductionLedgerState{
			entries:         state.entries,
			exhausted:       true,
			exhaustedTokens: estimate.tokens,
			exhaustedLen:    len(transcript),
			exhaustedDigest: exhaustionDigest(&replay, len(transcript)),
		})
		diagnostics.Strategy = "exhausted"
		return reductionOutcome{messages: currentMessages, diagnostics: diagnostics}
	}

	entries := reductionLedgerEntries(replay.sourceHashes(), plan)
	if !s.shared.storeReductionLedger(scope, state.epoch, reductionLedgerState{entries: entries}) {
		diagnostics.Strategy = "concurrent"
		return reductionOutcome{messages: currentMessages, diagnostics: diagnostics}
	}

	_, finalMessages := reductionPayload(deps, plan.transcript, plan.stages, req.files, req.question, inFlight)
	finalEstimate := estimateAdvisorMessages(ctx, deps, finalMessages)
	diagnostics.Strategy = "reduced"
	diagnostics.AfterTokens = finalEstimate.tokens
	diagnostics.Stages = plan.appliedStages
	return reductionOutcome{messages: finalMessages, diagnostics: diagnostics}
}

// replayReductionState reads the scope ledger and replays it against the
// transcript, invalidating entries from the first source mismatch.
func (s *handlerState) replayReductionState(scope string, transcript []provider.Message) (reductionLedgerState, reductionReplay) {
	state := s.shared.reductionLedger(scope)
	replay := replayReductionLedger(transcript, state.entries)
	if replay.valid >= len(state.entries) {
		return state, replay
	}
	next := reductionLedgerState{entries: state.entries[:replay.valid]}
	if s.shared.storeReductionLedger(scope, state.epoch, next) {
		return reductionLedgerState{entries: next.entries, epoch: state.epoch + 1}, replay
	}
	state = s.shared.reductionLedger(scope)
	return state, replayReductionLedger(transcript, state.entries)
}

// currentReductionPayload builds the payload for the current transcript. With
// no frozen ledger it keeps the legacy dedupe and message builder untouched.
func currentReductionPayload(deps HandlerDeps, req reductionRequest, replay reductionReplay, inFlight *provider.Message, hasLedger bool) ([]advisorFile, []provider.Message) {
	if !hasLedger {
		return legacyReductionPayload(deps, req)
	}
	return reductionPayload(deps, replay.transcript, replay.stages, req.files, req.question, inFlight)
}

// reductionPayload assembles the reduced payload shared by the current and
// final request paths.
func reductionPayload(deps HandlerDeps, transcript []provider.Message, stages []reductionStage, files []advisorFile, question string, inFlight *provider.Message) ([]advisorFile, []provider.Message) {
	files = dedupeAttachments(files, transcript, stages, deps.WorkDir)
	return files, buildMessages(rebuildSnapshot(transcript, inFlight), question, files)
}

// legacyReductionPayload reproduces the pre-reduction payload exactly,
// including its weaker file-hash dedupe. It clones the loaded files before
// that dedupe so the caller's copy stays unchanged.
func legacyReductionPayload(deps HandlerDeps, req reductionRequest) ([]advisorFile, []provider.Message) {
	files := dedupeAgainstSnapshot(append([]advisorFile(nil), req.files...), req.snapshot, deps.WorkDir)
	return files, buildMessages(req.snapshot, req.question, files)
}

func reductionRatio(tokens int, messages []provider.Message) float64 {
	if contentBytes := requestContentBytes(messages); contentBytes > 0 && tokens > 0 {
		return float64(tokens) / float64(contentBytes)
	}
	return 1
}

// planningBaselineTokens measures planned savings against all requested files
// at full size, so reducing a read does not depend on whether its attachment
// happens to be deduped in the current payload.
func planningBaselineTokens(tokens int, ratio float64, fullFiles, currentFiles []advisorFile) int {
	if fileDelta := advisorFilesRenderedLen(fullFiles) - advisorFilesRenderedLen(currentFiles); fileDelta > 0 {
		return tokens + tokensForBytes(fileDelta, ratio)
	}
	return tokens
}

func reductionLedgerEntries(hashes []string, plan reductionPlan) []reductionLedgerEntry {
	entries := make([]reductionLedgerEntry, len(hashes))
	for i := range hashes {
		entries[i] = reductionLedgerEntry{
			sourceHash:     hashes[i],
			stage:          plan.stages[i],
			referenceIndex: plan.references[i],
		}
		if plan.stages[i] != reductionStageNone {
			entries[i].replacement = plan.transcript[i].Content
		}
	}
	return entries
}
