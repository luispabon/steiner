package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

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

// selectCompactionFallback returns the fallback target for a compaction of req
// given the last main request. It returns false without resolving when the
// fallback is disabled (zero After or nil Resolve) or the main cache is warm.
// A resolution error is returned, and a resolved target without a provider is
// an error. A target with the same provider alias and backend model ID as req
// is skipped and returns false.
func selectCompactionFallback(ctx context.Context, req RunRequest, last LastMainRequest) (CompactionTarget, bool, error) {
	fallback := req.CompactionFallback
	if fallback.Resolve == nil || fallback.After <= 0 {
		return CompactionTarget{}, false, nil
	}
	if !compactionCacheCold(compactionFallbackNow(req), last, fallback.After) {
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

// compactionFallbackNow returns the request clock's time, or the real time when
// req has no clock.
func compactionFallbackNow(req RunRequest) time.Time {
	if req.Clock != nil {
		return req.Clock.Now()
	}
	return time.Now()
}

// compactionFallbackIdentity derives the fallback identity of mainValue for the
// target model: the first 32 lowercase hex characters of the SHA-256 of
// mainValue, a fixed separator, and the target's provider alias and backend
// model ID. An empty mainValue has no identity and returns empty.
func compactionFallbackIdentity(mainValue string, target provider.ResolvedModel) string {
	if mainValue == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(mainValue + "\x00compaction-fallback\x00" + target.ProviderAlias + "/" + target.BackendModelID))
	return hex.EncodeToString(sum[:16])
}

// compactionFallbackRequest returns a copy of req that runs a compaction call on
// target. The copy takes the target's provider, model, token budget and
// compaction prompt override, and keeps req's system prefix and usage source.
// Tools, prompt tools, prompt string lists, and the target's params and extra
// params are cloned, so the copy shares no mutable state with req or target.
// The prompt cache key and transport session are derived from their own main
// values. An empty main transport session gets a fresh random key on each call.
// The copy clears the parent transport session, the cache baseline and its own
// fallback, and is marked as a fallback target.
func compactionFallbackRequest(req RunRequest, target CompactionTarget) (RunRequest, error) {
	fallback := req
	fallback.Provider = target.Provider
	fallback.ResolvedModel = target.ResolvedModel
	fallback.ResolvedModel.Params = provider.CloneToolArguments(target.ResolvedModel.Params)
	fallback.ResolvedModel.ExtraParams = provider.CloneToolArguments(target.ResolvedModel.ExtraParams)
	fallback.ModelBudget = target.ModelBudget
	fallback.Tools = provider.CloneTools(req.Tools)
	fallback.Prompt.ModelBudget = target.ModelBudget
	fallback.Prompt.Tools = provider.CloneTools(req.Prompt.Tools)
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
