package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
	"github.com/luispabon/steiner/internal/provider"
)

// compactionCacheCold reports whether the provider prompt cache is presumed
// cold at now. A non-positive after disables the check. With no recorded main
// request (zero last.At) the cache is cold. Otherwise it is cold only once more
// than after has passed since last.At.
func compactionCacheCold(now time.Time, last LastMainRequest, after time.Duration) bool {
	if after <= 0 {
		return false
	}
	if last.At.IsZero() {
		return true
	}
	return now.Sub(last.At) > after
}

// CompactionTarget is the model a fallback compaction call runs on. It replaces
// the main provider, model and token budget for that call only.
type CompactionTarget struct {
	Provider      provider.Provider
	ResolvedModel provider.ResolvedModel
	ModelBudget   prompt.ModelTokenBudget
}

// CompactionFallback configures a compaction target used when the main prompt
// cache is presumed cold. The cache is cold once more than After has passed
// since the last main request; a non-positive After disables the fallback.
// Resolve builds the target and is called only when the fallback applies. Ref
// is the configured fallback model reference; the helpers in this file do not
// interpret it.
type CompactionFallback struct {
	After   time.Duration
	Ref     string
	Resolve func(context.Context) (CompactionTarget, error)
}

// compactionFallbackEnabled reports whether fallback has a resolver and a
// positive After. Only then does a compaction read the clock or resolve a target.
func compactionFallbackEnabled(fallback CompactionFallback) bool {
	return fallback.Resolve != nil && fallback.After > 0
}

// selectCompactionFallback returns the fallback target for a compaction of req
// given the last main request, judged at now. It returns false without resolving when the
// fallback is disabled (zero After or nil Resolve) or the main cache is warm.
// A resolution error is returned, and a resolved target without a provider is
// an error. A target with the same provider alias and backend model ID as req
// is skipped and returns false.
func selectCompactionFallback(ctx context.Context, req RunRequest, last LastMainRequest, now time.Time) (CompactionTarget, bool, error) {
	fallback := req.CompactionFallback
	if !compactionFallbackEnabled(fallback) {
		return CompactionTarget{}, false, nil
	}
	if !compactionCacheCold(now, last, fallback.After) {
		return CompactionTarget{}, false, nil
	}
	target, err := fallback.Resolve(ctx)
	if err != nil {
		return CompactionTarget{}, false, fmt.Errorf("resolve compaction fallback: %w", err)
	}
	if target.Provider == nil {
		return CompactionTarget{}, false, errors.New("compaction fallback target has no provider")
	}
	if target.ResolvedModel.ProviderAlias == req.ResolvedModel.ProviderAlias && target.ResolvedModel.BackendModelID == req.ResolvedModel.BackendModelID {
		return CompactionTarget{}, false, nil
	}
	return target, true, nil
}

// compactionFallbackIdentity derives the fallback identity of mainValue for the
// target model: the first 32 lowercase hex characters of the SHA-256 of the
// length-prefixed fields mainValue, a fixed marker, the target's provider alias
// and its backend model ID. Length prefixes keep the field boundaries
// unambiguous whatever the field contents. An empty mainValue has no identity
// and returns empty.
func compactionFallbackIdentity(mainValue string, target provider.ResolvedModel) string {
	if mainValue == "" {
		return ""
	}
	fields := []string{mainValue, "compaction-fallback", target.ProviderAlias, target.BackendModelID}
	var encoded strings.Builder
	for _, field := range fields {
		encoded.WriteString(strconv.Itoa(len(field)) + ":" + field)
	}
	sum := sha256.Sum256([]byte(encoded.String()))
	return hex.EncodeToString(sum[:16])
}

// compactionFallbackRequest returns a copy of req that runs a compaction call on
// target. The copy takes the target's provider, model, token budget and
// compaction prompt override, and keeps req's system prefix and usage source.
// Tools, prompt tools, prompt string lists, prompt and source conversations,
// and the target's params and extra params are cloned, so changing them on the
// copy does not change req or target. The provider, executor, context
// manager, event sink and DrainInbox handles are shared by design.
// The prompt cache key and transport session are derived from their own main
// values. An empty main transport session gets a fresh random key on each call.
// The copy clears the parent transport session, the cache baseline and its own
// fallback, and is marked as a fallback target. It is for one compaction call
// and is not passed to Runner.Run.
func compactionFallbackRequest(req RunRequest, target CompactionTarget) (RunRequest, error) {
	fallback := req
	fallback.Provider = target.Provider
	fallback.ResolvedModel = target.ResolvedModel
	fallback.ResolvedModel.Params = provider.CloneToolArguments(target.ResolvedModel.Params)
	fallback.ResolvedModel.ExtraParams = provider.CloneToolArguments(target.ResolvedModel.ExtraParams)
	fallback.ModelBudget = target.ModelBudget
	fallback.Tools = provider.CloneTools(req.Tools)
	fallback.SourceConversation = cloneMessages(req.SourceConversation)
	fallback.Prompt.ModelBudget = target.ModelBudget
	fallback.Prompt.Tools = provider.CloneTools(req.Prompt.Tools)
	fallback.Prompt.Conversation = provider.CloneMessages(req.Prompt.Conversation)
	fallback.Prompt.SkillsRoots = cloneStrings(req.Prompt.SkillsRoots)
	fallback.Prompt.SkillNames = cloneStrings(req.Prompt.SkillNames)
	fallback.Prompt.ProjectContextExtraFiles = cloneStrings(req.Prompt.ProjectContextExtraFiles)
	fallback.Prompt.ProjectContextIgnoreFiles = cloneStrings(req.Prompt.ProjectContextIgnoreFiles)
	fallback.Prompt.SandboxWritableMounts = cloneStrings(req.Prompt.SandboxWritableMounts)
	fallback.Prompt.PromptOverrides.Compaction = target.ResolvedModel.Prompts.Compaction
	fallback.PromptCacheKey = compactionFallbackIdentity(req.PromptCacheKey, target.ResolvedModel)
	if req.TransportSession != "" {
		fallback.TransportSession = compactionFallbackIdentity(req.TransportSession, target.ResolvedModel)
	} else {
		transport, err := provider.NewPromptCacheKey()
		if err != nil {
			return RunRequest{}, fmt.Errorf("create compaction fallback transport session: %w", err)
		}
		fallback.TransportSession = transport
	}
	fallback.ParentTransportSession = ""
	fallback.CacheBaseline = nil
	fallback.CompactionFallback = CompactionFallback{}
	fallback.compactionFallbackTarget = true
	return fallback, nil
}

// compactionRun runs one two-stage compaction operation. A nil summaryReq runs
// the summary calls on main; otherwise they run on summaryReq.
type compactionRun func(summaryReq *RunRequest) (CompactionOutcome, error)

// compactWithFallback runs one compaction operation on the fallback target when
// selectCompactionFallback selects it for state's last main request, and on main
// otherwise. A failed target attempt is discarded and the operation reruns once
// on main from the original state. Cancellation and run limit timeouts return
// the error without a rerun.
func compactWithFallback(ctx context.Context, req RunRequest, state RunState, run compactionRun) (CompactionOutcome, error) {
	if !compactionFallbackEnabled(req.CompactionFallback) {
		return run(nil)
	}
	now := requestNow(req)
	target, selected, err := selectCompactionFallback(ctx, req, state.LastMainRequest, now)
	if err != nil {
		return compactionOnMainAfterFallback(ctx, req, run, err)
	}
	if !selected {
		return run(nil)
	}
	outcome, err := runCompactionOnTarget(req, target, run)
	if err != nil {
		return compactionOnMainAfterFallback(ctx, req, run, err)
	}
	if !outcome.Applied {
		// The target applied nothing, so no fallback summary was used.
		return outcome, nil
	}
	outcome.CompactionModel = compactionModelName(target.ResolvedModel)
	outcome.FallbackStatus = output.CompactionFallbackUsed
	outcome.FallbackReason = compactionUsedReason(now, state.LastMainRequest, req.CompactionFallback.After)
	return outcome, nil
}

// runCompactionOnTarget runs the operation with its summary calls on target.
func runCompactionOnTarget(req RunRequest, target CompactionTarget, run compactionRun) (CompactionOutcome, error) {
	fallback, err := compactionFallbackRequest(req, target)
	if err != nil {
		return CompactionOutcome{}, err
	}
	return run(&fallback)
}

// compactionOnMainAfterFallback discards a failed fallback attempt and reruns the
// operation on main. It returns fallbackErr without a rerun when the attempt was
// canceled or hit a run limit timeout.
func compactionOnMainAfterFallback(ctx context.Context, req RunRequest, run compactionRun, fallbackErr error) (CompactionOutcome, error) {
	if ctx.Err() != nil || errors.Is(fallbackErr, context.Canceled) {
		return CompactionOutcome{}, fallbackErr
	}
	outcome, err := run(nil)
	if err != nil {
		return CompactionOutcome{}, err
	}
	outcome.CompactionModel = compactionModelName(req.ResolvedModel)
	outcome.FallbackStatus = output.CompactionFallbackSkipped
	outcome.FallbackReason = compactionSkipReason(req, fallbackErr)
	return outcome, nil
}

// compactionModelName returns the canonical ProviderAlias/BackendModelID of model.
func compactionModelName(model provider.ResolvedModel) string {
	return model.ProviderAlias + "/" + model.BackendModelID
}

// compactionUsedReason explains a selected fallback decided at now. It is "no
// recent request" when no main request is recorded. Otherwise it gives the idle
// time and the threshold in whole minutes. When rounding makes them equal, it
// gives exact durations instead, so the reason still shows idle > threshold.
func compactionUsedReason(now time.Time, last LastMainRequest, after time.Duration) string {
	if last.At.IsZero() {
		return "no recent request"
	}
	idle := now.Sub(last.At)
	idleMinutes := idle.Round(time.Minute)
	afterMinutes := after.Round(time.Minute)
	if idleMinutes == afterMinutes {
		return fmt.Sprintf("idle %s > %s", idle, after)
	}
	return fmt.Sprintf("idle %dm > %dm", int(idleMinutes/time.Minute), int(afterMinutes/time.Minute))
}

// compactionSkipReason explains a selected fallback that was discarded.
func compactionSkipReason(req RunRequest, err error) string {
	if errors.Is(err, errCompactionFallbackNotFit) {
		return "context too large for " + req.CompactionFallback.Ref
	}
	return fmt.Sprintf("fallback failed: %v", err)
}
