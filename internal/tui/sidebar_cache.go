package tui

import (
	"slices"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// sidebarStateComparable mirrors every field of sidebarState except
// modifiedFiles, which is a slice and makes sidebarState itself
// non-comparable. It exists solely so sidebar render caching can key on "the
// entire input" the way statusState's cache does (see renderStatus):
// comparing this projection plus a separate modifiedFiles comparison covers
// every field View reads, so the cache cannot go stale without the
// comparison catching it. TestSidebarStateComparableFieldParity pins this
// struct's field set against sidebarState's so an added field cannot be
// silently missed.
type sidebarStateComparable struct {
	expanded              bool
	model                 string
	reasoning             string
	version               string
	quant                 string
	provider              string
	providerName          string
	homeDir               string
	promptUsed            int
	budgetUsed            int
	contextBudget         int
	currentTurn           int
	maxTurns              int
	compaction            compactionState
	branch                string
	dirty                 bool
	ahead                 int
	workingDir            string
	activeSkill           string
	styles                *theme.Styles
	tickCount             int
	perfDurationMs        int64
	perfTTFTMs            int64
	perfOutputTPS         float64
	sessionCacheHitRate   float64
	sessionCacheHitRateOK bool
	sessionActive         bool
	sessionElapsedSec     int64
	oneshotPhase          string
	sandboxStatus         string
	execMode              string
	mcpConnected          int
	mcpTotal              int
	mcpConnecting         bool
	mcpFailed             bool
}

// comparable projects s onto its comparable fields (everything but
// modifiedFiles).
func (s sidebarState) comparable() sidebarStateComparable {
	return sidebarStateComparable{
		expanded:              s.expanded,
		model:                 s.model,
		reasoning:             s.reasoning,
		version:               s.version,
		quant:                 s.quant,
		provider:              s.provider,
		providerName:          s.providerName,
		homeDir:               s.homeDir,
		promptUsed:            s.promptUsed,
		budgetUsed:            s.budgetUsed,
		contextBudget:         s.contextBudget,
		currentTurn:           s.currentTurn,
		maxTurns:              s.maxTurns,
		compaction:            s.compaction,
		branch:                s.branch,
		dirty:                 s.dirty,
		ahead:                 s.ahead,
		workingDir:            s.workingDir,
		activeSkill:           s.activeSkill,
		styles:                s.styles,
		tickCount:             s.tickCount,
		perfDurationMs:        s.perfDurationMs,
		perfTTFTMs:            s.perfTTFTMs,
		perfOutputTPS:         s.perfOutputTPS,
		sessionCacheHitRate:   s.sessionCacheHitRate,
		sessionCacheHitRateOK: s.sessionCacheHitRateOK,
		sessionActive:         s.sessionActive,
		sessionElapsedSec:     s.sessionElapsedSec,
		oneshotPhase:          s.oneshotPhase,
		sandboxStatus:         s.sandboxStatus,
		execMode:              s.execMode,
		mcpConnected:          s.mcpConnected,
		mcpTotal:              s.mcpTotal,
		mcpConnecting:         s.mcpConnecting,
		mcpFailed:             s.mcpFailed,
	}
}

// sidebarCacheKey is the full render cache key for sidebarState.View: the
// comparable projection of every scalar field, plus the render dimensions
// (View's own parameters) and modifiedFiles (compared separately since a
// slice can't be part of a comparable struct).
type sidebarCacheKey struct {
	width, height int
	state         sidebarStateComparable
}

// renderSidebar memoizes sidebarState.View, keyed on every field View reads.
// Unlike the choke-point pattern used elsewhere in this package (bump a
// counter at known invalidation sites), this key is the input itself: any
// write to any sidebar field, from anywhere, changes the key and forces a
// fresh render. That matters here because sidebar fields are written from
// well over a dozen sites across model.go, model_events.go, model_init.go
// and model_update.go, most of which do not go through syncSidebar — a
// choke-point counter would have to be threaded through all of them to stay
// complete, and any site missed would serve a stale frame indefinitely. The
// comparable-projection key sidesteps that enumeration risk entirely.
func (m *Model) renderSidebar(width, height int) string {
	key := sidebarCacheKey{width: width, height: height, state: m.sidebar.comparable()}
	if m.sidebarViewCacheSet && m.sidebarViewCacheKey == key &&
		slices.Equal(m.sidebarViewCacheFiles, m.sidebar.modifiedFiles) {
		return m.sidebarViewCacheRendered
	}
	rendered := m.sidebar.View(width, height)
	m.sidebarViewCacheSet = true
	m.sidebarViewCacheKey = key
	m.sidebarViewCacheFiles = append([]gitModifiedFile(nil), m.sidebar.modifiedFiles...)
	m.sidebarViewCacheRendered = rendered
	return rendered
}
