package tui

import (
	"strings"
	"time"

	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type contentSegmentKind int

const (
	segmentPlain contentSegmentKind = iota
	segmentAssistantMarkdown
	segmentApproval
	segmentTool
	segmentThinking
	segmentUser
	segmentUserMarkdown
	segmentThinkingBlock
	segmentToolCall
	segmentToolCallGroup
	segmentApprovalPill
	segmentCompactionBanner
	segmentSeparator
	segmentInterrupted
	segmentDelegation
	segmentDelegationGroup
	segmentPendingSteer
	segmentStatus
	segmentImagesAttached
)

type thinkingBlockData struct {
	collapsed bool   // default true
	body      string // full content
	streaming bool   // true while chunks are still arriving
	source    output.ChunkSource
}

type toolCallSegment struct {
	tool           string // "bash", "read", "mutate", "glob", "grep", etc.
	args           string // summarized args; truncated at render time to fit terminal width
	meta           string // "✓" or "✗" for finished calls
	bodyKind       string // "bash", "file", "glob", "grep", "ls", "json", "plain"
	body           string // raw result text
	callID         string // for matching started→finished
	collapsed      bool   // default true
	hasError       bool   // set when ToolCallFinished carries an error
	active         bool
	startTime      int64
	elapsed        string
	spinnerFrame   int
	rawArgs        map[string]any
	preview        output.ToolPreview
	displayPreview *output.PreviewDocument
	// approval state — embedded in the tool row instead of a sibling pill
	approvalPending        bool
	approvalResolved       bool
	approvalAccepted       bool
	approvalMode           string
	approvalPreview        string
	approvalKind           string // "path" or "mcp"
	approvalServer         string // MCP server name (empty for path)
	approvalMCPTool        string // MCP tool name (empty for path)
	approvalAgentID        string
	approvalIdentity       string
	approvalQueueDepth     int
	approvalSelectedAction int // 0=allow once, 1=always allow, 2=deny
}

type toolCallGroupSegment struct {
	tool    string
	mixed   bool
	entries []*toolCallSegment
}

// delegationGroupSegment holds consecutive specialist delegations rendered in one
// bordered box. Never holds an advisor delegation. Entries are append-only and
// never reordered, so an entry pointer stays valid for the segment's lifetime.
type delegationGroupSegment struct {
	entries []*delegationDisplayState
}

type approvalPillData struct {
	tool        string
	mode        string
	preview     string
	kind        string // "path" or "mcp"
	server      string // MCP server name (empty for path)
	mcpToolName string // MCP tool name (empty for path)
	agentID     string
	identity    string
	queueDepth  int
	resolved    bool
	accepted    bool
}

type compactionBannerData struct {
	label    string
	subtitle string
	finished bool
	summary  string
	msgCount int // number of messages compacted (for finished summary)

	// timing — set on first "compacting" event; computed on finish
	startTime int64  // unix nano; 0 when replaying history (no wall-clock available)
	elapsed   string // formatted elapsed time, set when finished

	// spinner state (updated by tick)
	spinnerFrame int

	// session context
	compactionCount int // running count of compactions in this session

	// compaction statistics (populated on finish)
	compactedTurns    int
	compactedMessages int
	retainedTurns     int
	retainedMessages  int
	mode              string  // compaction mode reported by the engine
	beforeTokens      int     // prompt tokens before compaction
	beforePct         float64 // context usage % before compaction
	afterTokens       int     // prompt tokens after compaction
	afterPct          float64 // context usage % after compaction
	summaryTitle      string  // human-readable summary title
	cacheReadTokens   int
	inputTokens       int
	cacheCreateTokens int
}

type separatorData struct {
	label   string
	closing bool
	phase   bool
}

type delegationTranscriptEntryKind int

const (
	delegationTranscriptEntryAssistant delegationTranscriptEntryKind = iota
	delegationTranscriptEntryThinking
	delegationTranscriptEntryTool
)

type delegationTranscriptEntry struct {
	kind     delegationTranscriptEntryKind
	body     string
	source   output.ChunkSource
	tool     string
	args     string
	callID   string
	status   string
	hasError bool
}

// delegationLocator identifies one delegation: the segment that renders it and
// the state it renders. The segment may hold a single delegation or be a group,
// so a segment index alone no longer identifies a delegation.
type delegationLocator struct {
	seg int
	dd  *delegationDisplayState
}

// toolCallLocator identifies one regular tool call and its owning segment. The
// segment may hold a single call or a group.
type toolCallLocator struct {
	seg int
	td  *toolCallSegment
}

// delegationDisplayState tracks in-flight or finished delegation state for rendering.
type delegationDisplayState struct {
	isAdvisor bool
	agentID   string
	agentType string
	toolLabel string // specialized tool name used for rendering (e.g. "explore")
	// (see effectiveTypeLabel in content_render_delegation.go for how these two combine)
	taskPreview             string // truncated to ~80 chars
	promptText              string
	promptCollapsed         bool
	parentCallID            string
	parentArgs              string
	startTime               int64 // unix nano, set on DelegationStarted
	cacheWaiting            bool
	cacheWaitDeadline       int64  // unix nano, valid only when cacheWaiting
	elapsed                 string // formatted elapsed, set on Complete/Failed
	spinnerFrame            int    // index into spinnerFrames
	status                  string // "active" | "complete" | "failed"
	finalizedByCancellation bool   // true when cancellation finalized this display
	// result fields (Complete)
	resultStatus      string
	turnCount         int
	tokenCount        int
	toolCallCount     int
	cacheHitRate      float64
	cacheHitOK        bool
	cacheReadTokens   int
	inputTokens       int
	cacheCreateTokens int
	modelName         string
	reasoning         string
	promptTokens      int
	contextWindow     int
	contextFillPct    float64 // last known context window occupancy %, 0 if unknown
	outputTPS         float64 // latest per-turn output tokens/sec, 0 if unknown
	// failure field
	errMsg string
	// output text and visibility
	output    string
	collapsed bool
	// child transcript state
	currentOperation string
	entries          []delegationTranscriptEntry
	childToolEntries map[string]int
	// follow_up state: set when this is a follow-up call to an existing child
	isFollowUp      bool
	followUpAgentID string // the child agent being followed up on
	// follow-up baseline: cumulative stats from the original child at the time
	// the follow_up tool was called. Used to compute per-follow-up deltas from
	// cumulative DelegationCompleteEvent payload values.
	baselineTurnCount     int
	baselineToolCallCount int
	baselineTokenCount    int
	advisorUse            int
	advisorMaxUses        int
	advisorBudget         int
	advisorUses           int
	advisorDenied         int
	advisorQuestion       string
	advisorFiles          []string
	briefObjective        string
	briefContext          string
	briefDeliverable      string
	briefConstraints      []string
	briefSuccessCriteria  []string
	briefChecks           []string
	extCurrent            int
	extMax                int
}

type imagesAttachedRowData struct {
	id     string
	width  int
	height int
	size   string
	path   string
}

type imagesAttachedData struct {
	rows []imagesAttachedRowData
}

type contentSegment struct {
	kind               contentSegmentKind
	text               string
	timestamp          time.Time
	thinkData          *thinkingBlockData      // non-nil only for segmentThinkingBlock
	toolData           *toolCallSegment        // non-nil only for segmentToolCall
	toolGroupData      *toolCallGroupSegment   // non-nil only for segmentToolCallGroup
	approvalData       *approvalPillData       // non-nil only for segmentApprovalPill
	compactionData     *compactionBannerData   // non-nil only for segmentCompactionBanner
	separatorData      *separatorData          // non-nil only for segmentSeparator
	delegData          *delegationDisplayState // non-nil only for segmentDelegation
	delegGroupData     *delegationGroupSegment // non-nil only for segmentDelegationGroup
	imagesAttachedData *imagesAttachedData     // non-nil only for segmentImagesAttached
	// render cache
	cachedRender      string
	cachedRenderWidth int
	renderDirty       bool
	// renderGen increments each time processSegment re-renders this segment.
	// Anchors record it at capture time so a same-width remap can skip the
	// matchRow scan while the render is unchanged.
	renderGen int
}

// spinnerFrames is the braille spinner sequence used for active delegations.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type contentBuffer struct {
	segments          []contentSegment
	streaming         bool
	hadChunks         bool
	streamBuffer      string
	renderer          *glamour.TermRenderer
	renderWidth       int
	styles            *theme.Styles
	modelBadge        func(backend string) (alias, effort string)
	modelAliasBadge   func(alias string) (name, effort string)
	glamourStyleSheet glamour.TermRendererOption
	previewStyleCache map[chroma.TokenType]lipgloss.Style
	collapseState     map[int]bool    // segment index → collapsed (for tool calls and thinking)
	segmentHeights    []int           // rendered line count per segment (recomputed in String())
	showThinking      bool            // from prefs; when false skip thinking segments
	lastShowThinking  bool            // last showThinking value observed by checkBufferDirty
	compaction        compactionState // when true skip thinking chunks from compaction
	streamingPhase    string          // "thinking" | "tool" | "answer" | ""
	streamingSource   output.ChunkSource
	tickCount         int   // incremented by 500ms tick, used for cursor blink
	lastRenderErr     error // captures the last render error for logging
	// delegation tracking
	activeDelegations       map[string]delegationLocator // agentID → delegation locator (for in-flight delegations)
	activeToolCalls         map[string]toolCallLocator   // callID → regular tool-call locator
	pendingDelegateParents  []delegationLocator          // delegations awaiting DelegationStartedEvent binding
	pendingDelegationStarts []delegationLocator          // delegations awaiting parent delegate tool binding
	activeAdvisorSegment    int                          // 1-based segment index; 0 means none active
	skillNames              []string                     // skill names for command prefix matching
	mcpToolOrigins          map[string]MCPToolOrigin     // registry tool name -> MCP server/tool it came from
	maxDelegationBodyLines  int                          // max lines for delegation body (transcript + prompt); 0 = uncapped
	workingDir              string                       // current working directory for resolving relative paths
	homeDir                 string                       // home directory for resolving ~ paths

	// Render cache.
	stringCacheWidth    int
	stringCacheRendered string

	// gen is bumped whenever an existing segment is mutated in place (never on
	// append). It invalidates the settled-prefix cache below so a retroactive
	// mutation of an already-settled segment cannot serve stale output.
	gen int

	// Settled-prefix cache: the joined render of segments [0, prefixCacheLen),
	// reused across dirty frames so streaming only re-walks the changing tail
	// (preview, spinners, live segments). Keyed on gen (retroactive mutation),
	// width (rendered segments depend on it), and showThinking (visibility).
	prefixCacheSet          bool
	prefixCacheRendered     string
	prefixCacheLastKind     contentSegmentKind
	prefixCacheLen          int
	prefixCacheWidth        int
	prefixCacheShowThinking bool
	prefixCacheGen          int
}

func (b *contentBuffer) resolveDelegationModel(model string) (name, effort string) {
	model = strings.TrimSpace(model)
	if b.modelBadge != nil {
		return b.modelBadge(model)
	}
	return model, ""
}

// resolveAliasBadge resolves a badge from a delegation's already-known model
// alias, bypassing the ambiguous backend ID to alias reverse map.
func (b *contentBuffer) resolveAliasBadge(alias string) (string, string) {
	if b.modelAliasBadge != nil {
		return b.modelAliasBadge(alias)
	}
	return alias, ""
}

type contentEventHandler func(*contentBuffer, output.Event)

var contentEventHandlers = map[string]contentEventHandler{
	output.EventTypeThinkingChunk:          (*contentBuffer).appendThinkingChunkEvent,
	output.EventTypeAssistantChunk:         (*contentBuffer).appendAssistantChunkEvent,
	output.EventTypeApprovalRequested:      (*contentBuffer).appendApprovalRequestedEvent,
	output.EventTypeApprovalAccepted:       (*contentBuffer).appendApprovalDecisionEvent,
	output.EventTypeApprovalDenied:         (*contentBuffer).appendApprovalDecisionEvent,
	output.EventTypeDelegationStarted:      (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationComplete:     (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationCacheWaiting: (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationFailed:       (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationExtension:    (*contentBuffer).appendDelegationEvent,
	output.EventTypeAdvisorStarted:         (*contentBuffer).appendAdvisorEvent,
	output.EventTypeAdvisorComplete:        (*contentBuffer).appendAdvisorEvent,
	output.EventTypeAdvisorBudgetExhausted: (*contentBuffer).appendAdvisorEvent,
	output.EventTypeProviderDiagnostic:     (*contentBuffer).appendProviderDiagnosticEvent,
	output.EventTypeToolCallStarted:        (*contentBuffer).appendToolCallStartedEvent,
	output.EventTypeToolCallFinished:       (*contentBuffer).appendToolCallFinishedEvent,
	output.EventTypeDisplayFile:            (*contentBuffer).appendDisplayFileEvent,
	output.EventTypeStopReason:             (*contentBuffer).appendStopReasonEvent,
	output.EventTypeAssistantMessage:       (*contentBuffer).appendAssistantMessageEvent,
	output.EventTypeContextReport:          (*contentBuffer).appendContextReportEvent,
	output.EventTypeModelCallStarted:       (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeModelCallFinished:      (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeContextDiagnostics:     (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeUserInput:              (*contentBuffer).appendUserInputEvent,
	output.EventTypePhaseTransition:        (*contentBuffer).appendPhaseTransitionEvent,
	output.EventTypeRunStarted:             func(*contentBuffer, output.Event) {},
	output.EventTypeRunFinished:            func(*contentBuffer, output.Event) {},
	// ModeChanged transcript lines are appended explicitly by model_events.go
	// so the "mode → x" wording matches other status-line conventions.
	output.EventTypeModeChanged: func(*contentBuffer, output.Event) {},
	// ConfigWarning transcript lines are appended explicitly by model_events.go
	// (warning styling) so the event never mutates sandbox state.
	output.EventTypeConfigWarning: func(*contentBuffer, output.Event) {},
	output.EventTypeAPIRequest:    func(*contentBuffer, output.Event) {},
	output.EventTypeAPIResponse:   func(b *contentBuffer, _ output.Event) { b.finishStreaming() },
	// SteerReceived is handled by model_events.go (PromoteLastPendingSteer); no
	// content line is emitted here.
	output.EventTypeSteerReceived: func(*contentBuffer, output.Event) {},
	// MCPStatus is display-only state handled by model_events.go; no transcript
	// line is emitted here.
	output.EventTypeMCPStatus: func(*contentBuffer, output.Event) {},
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	if event.Scope.AgentID != "" && b.appendScopedDelegationEvent(event) {
		return
	}
	// Redirect thinking chunks to active advisor segment if present and sourced from advisor.
	if event.Type == output.EventTypeThinkingChunk &&
		b.activeAdvisorSegment > 0 &&
		thinkingChunkSource(event) == output.ChunkSourceAdvisor {
		b.appendAdvisorEvent(event)
		return
	}
	if handler, ok := contentEventHandlers[event.Type]; ok {
		handler(b, event)
		return
	}
	b.finishStreaming()
	line := strings.TrimSpace(output.FormatEvent(event))
	if shouldSuppressLine(line) {
		return
	}
	b.appendLine(line)
}

func thinkingChunkSource(event output.Event) output.ChunkSource {
	if payload, ok := event.Payload.(output.ThinkingChunkEvent); ok {
		return payload.Source
	}
	return ""
}

// AppendCompactionResult appends a centered separator line with the given label
// followed by the summary text rendered as a markdown block, then a closing separator.
func (b *contentBuffer) AppendCompactionResult(label string, summaryText string) {
	b.appendLabeledBlock(label, summaryText)
}

// appendLabeledBlock appends opening separator, markdown body, and closing separator.
func (b *contentBuffer) appendLabeledBlock(label string, body string) {
	b.segments = append(b.segments, contentSegment{
		kind:          segmentSeparator,
		separatorData: &separatorData{label: label},
		renderDirty:   true,
	})
	b.appendMarkdownBlock(body)
	b.segments = append(b.segments, contentSegment{
		kind:          segmentSeparator,
		separatorData: &separatorData{label: label, closing: true},
		renderDirty:   true,
	})
}

func (b *contentBuffer) appendPhaseTransitionEvent(event output.Event) {
	b.finishStreaming()
	b.appendStyled(strings.TrimSpace(output.FormatEvent(event)), segmentStatus)
}

// AppendPhaseDivider appends a phase transition divider to mark the beginning of a new oneshot phase.
func (b *contentBuffer) AppendPhaseDivider(phaseName string) {
	b.segments = append(b.segments, contentSegment{
		kind:          segmentSeparator,
		separatorData: &separatorData{label: "Phase: " + phaseName, phase: true},
		renderDirty:   true,
	})
}
