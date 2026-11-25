package agent

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Environment variables that configure the experimental segmented compaction
// prototype. They are read once at startup (see ParseSegmentedCompactionPolicy)
// and are not part of the configuration file surface.
const (
	// SegmentedCompactionEnv enables segmented compaction when it parses as true.
	SegmentedCompactionEnv = "STEINER_EXPERIMENT_SEGMENTED_COMPACTION"
	// SegmentMaxCountEnv bounds how many immutable segments a chain may hold.
	SegmentMaxCountEnv = "STEINER_EXPERIMENT_SEGMENT_MAX_COUNT"
	// SegmentMaxChainPercentEnv bounds the segment chain as a percentage of the compaction budget.
	SegmentMaxChainPercentEnv = "STEINER_EXPERIMENT_SEGMENT_MAX_CHAIN_PERCENT"
	// SegmentMaxPostPercentEnv bounds projected post-append tokens as a percentage of the compaction budget.
	SegmentMaxPostPercentEnv = "STEINER_EXPERIMENT_SEGMENT_MAX_POST_PERCENT"
)

const (
	defaultSegmentMaxCount        = 8
	defaultSegmentMaxChainPercent = 12.5
	defaultSegmentMaxPostPercent  = 50
	maxSegmentCount               = 64
)

// SegmentedCompactionPolicy configures the experimental immutable summary segment compaction (#1042).
type SegmentedCompactionPolicy struct {
	Enabled                  bool
	MaxSegments              int     // default 8, valid 1..64
	MaxChainPercent          float64 // default 12.5, valid (0,100]; percent of the compaction budget (D15)
	MaxPostCompactionPercent float64 // default 50, valid (0,100]; percent of the compaction budget (D15)
}

// DefaultSegmentedCompactionPolicy returns the policy that applies when no
// environment overrides are set: disabled, with the documented thresholds.
func DefaultSegmentedCompactionPolicy() SegmentedCompactionPolicy {
	return SegmentedCompactionPolicy{
		MaxSegments:              defaultSegmentMaxCount,
		MaxChainPercent:          defaultSegmentMaxChainPercent,
		MaxPostCompactionPercent: defaultSegmentMaxPostPercent,
	}
}

// ParseSegmentedCompactionPolicy reads the segmented compaction settings through
// lookup. Unset or blank values keep their defaults. Thresholds are validated
// even when Enabled is false, so a bad value fails startup rather than the first
// compaction.
func ParseSegmentedCompactionPolicy(lookup func(string) (string, bool)) (SegmentedCompactionPolicy, error) {
	policy := DefaultSegmentedCompactionPolicy()

	if v, ok := envValue(lookup, SegmentedCompactionEnv); ok {
		enabled, err := strconv.ParseBool(v)
		if err != nil {
			return SegmentedCompactionPolicy{}, fmt.Errorf("parse %s: %w", SegmentedCompactionEnv, err)
		}
		policy.Enabled = enabled
	}

	if v, ok := envValue(lookup, SegmentMaxCountEnv); ok {
		count, err := strconv.Atoi(v)
		if err != nil {
			return SegmentedCompactionPolicy{}, fmt.Errorf("parse %s: %w", SegmentMaxCountEnv, err)
		}
		if count < 1 || count > maxSegmentCount {
			return SegmentedCompactionPolicy{}, fmt.Errorf("%s must be between 1 and %d, got %d", SegmentMaxCountEnv, maxSegmentCount, count)
		}
		policy.MaxSegments = count
	}

	chainPercent, err := parseSegmentPercent(lookup, SegmentMaxChainPercentEnv, policy.MaxChainPercent)
	if err != nil {
		return SegmentedCompactionPolicy{}, err
	}
	policy.MaxChainPercent = chainPercent

	postPercent, err := parseSegmentPercent(lookup, SegmentMaxPostPercentEnv, policy.MaxPostCompactionPercent)
	if err != nil {
		return SegmentedCompactionPolicy{}, err
	}
	policy.MaxPostCompactionPercent = postPercent

	return policy, nil
}

// envValue returns the trimmed value of name, reporting false when lookup is
// nil, the variable is unset, or its value is blank.
func envValue(lookup func(string) (string, bool), name string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	v, ok := lookup(name)
	if !ok {
		return "", false
	}
	v = strings.TrimSpace(v)
	return v, v != ""
}

// parseSegmentPercent parses a percentage in (0, 100], keeping fallback when the
// variable is unset or blank.
func parseSegmentPercent(lookup func(string) (string, bool), name string, fallback float64) (float64, error) {
	v, ok := envValue(lookup, name)
	if !ok {
		return fallback, nil
	}
	percent, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", name, err)
	}
	if math.IsNaN(percent) || math.IsInf(percent, 0) || percent <= 0 || percent > 100 {
		return 0, fmt.Errorf("%s must be in (0, 100], got %q", name, v)
	}
	return percent, nil
}
