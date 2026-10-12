package agent

import (
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/usagestats"
)

// ParentUsage is the provider usage of the last successful parent model
// response. A stateful compaction provider triggers compaction from it, so an
// interactive run carries it into the next run. It is in-memory only: it is
// never written to BackendState or to saved session state.
type ParentUsage struct {
	// ModelBackendID is the backend model ID the usage was reported for.
	ModelBackendID string
	// ContextSize is the model context window the usage was measured against.
	ContextSize int
	// Usage is the provider usage of the response.
	Usage *provider.UsageStats
}

// newParentUsage returns the parent usage of a successful response made by
// req's model, or nil when the response carried no usage. Only parent runs
// (usagestats.SourceParent) record it; sub-agent and advisor runs never do.
func newParentUsage(req RunRequest, usage *provider.UsageStats) *ParentUsage {
	if usage == nil || req.UsageSource != usagestats.SourceParent {
		return nil
	}
	return &ParentUsage{
		ModelBackendID: req.ResolvedModel.BackendModelID,
		ContextSize:    req.ModelBudget.ContextSize,
		Usage:          cloneUsageStats(usage),
	}
}

// seedParentUsage returns a copy of seed when it was measured on req's model
// and context window. Any other seed is dropped, so a model or context change
// never triggers compaction from usage that no longer applies.
func seedParentUsage(seed *ParentUsage, req RunRequest) *ParentUsage {
	if seed == nil || seed.Usage == nil {
		return nil
	}
	if seed.ModelBackendID != req.ResolvedModel.BackendModelID || seed.ContextSize != req.ModelBudget.ContextSize {
		return nil
	}
	return seed.Clone()
}

// Clone returns a deep copy of the parent usage. A nil receiver returns nil.
func (u *ParentUsage) Clone() *ParentUsage {
	if u == nil {
		return nil
	}
	out := *u
	out.Usage = cloneUsageStats(u.Usage)
	return &out
}

// cloneUsageStats returns a copy of usage. Nil normalizes to nil.
func cloneUsageStats(usage *provider.UsageStats) *provider.UsageStats {
	if usage == nil {
		return nil
	}
	out := *usage
	return &out
}
