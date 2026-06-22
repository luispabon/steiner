package tui

import (
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

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
	segmentPendingSteer
	segmentStatus
)

type thinkingBlockData struct {
	preview   string // first 80 chars
	collapsed bool   // default true
	body      string // full content
	streaming bool   // true while chunks are still arriving
	source    output.ChunkSource
}

type toolCallSegment struct {
	tool           string // "bash", "read", "mutate", "glob", "grep", etc.
	args           string // summarized args; truncated at render time to fit terminal width
	meta           string // "✅" or "❌" for finished calls
	bodyKind       string // "bash", "file", "glob", "grep", "ls", "plain"
	body           string // raw result text
	callID         string // for matching started→finished
	collapsed      bool   // default true
	hasError       bool   // set when ToolCallFinished carries an error
	rawArgs        map[string]any
	preview        output.ToolPreview
	displayPreview *output.PreviewDocument
	// approval state — embedded in the tool row instead of a sibling pill
	approvalPending        bool
	approvalResolved       bool
	approvalAccepted       bool
	approvalMode           string
	approvalPreview        string
	approvalSelectedAction int // 0=allow once, 1=always allow, 2=deny
}

type toolCallGroupSegment struct {
	tool    string
	entries []*toolCallSegment
}

type approvalPillData struct {
	tool     string
	mode     string
	preview  string
	resolved bool
	accepted bool
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

	// UI state
	collapsed bool // default true; controls whether detail rows are shown
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

// delegationDisplayState tracks in-flight or finished delegation state for rendering.
type delegationDisplayState struct {
	isAdvisor       bool
	agentID         string
	toolLabel       string // specialized tool name (e.g. "explore"), empty means "delegate"
	taskPreview     string // truncated to ~80 chars
	promptText      string
	promptCollapsed bool
	parentCallID    string
	parentArgs      string
	startTime       int64  // unix nano, set on DelegationStarted
	elapsed         string // formatted elapsed, set on Complete/Failed
	spinnerFrame    int    // index into spinnerFrames
	status          string // "active" | "complete" | "failed"
	// result fields (Complete)
	resultStatus   string
	turnCount      int
	tokenCount     int
	toolCallCount  int
	modelName      string
	promptTokens   int
	contextWindow  int
	contextFillPct float64 // last known context window occupancy %, 0 if unknown
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
	extCurrent            int
	extMax                int
}

type contentSegment struct {
	kind           contentSegmentKind
	text           string
	timestamp      time.Time
	thinkData      *thinkingBlockData      // non-nil only for segmentThinkingBlock
	toolData       *toolCallSegment        // non-nil only for segmentToolCall
	toolGroupData  *toolCallGroupSegment   // non-nil only for segmentToolCallGroup
	approvalData   *approvalPillData       // non-nil only for segmentApprovalPill
	compactionData *compactionBannerData   // non-nil only for segmentCompactionBanner
	separatorData  *separatorData          // non-nil only for segmentSeparator
	delegData      *delegationDisplayState // non-nil only for segmentDelegation
	// render cache
	cachedRender      string
	cachedRenderWidth int
	renderDirty       bool
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
	styles            theme.Styles
	glamourStyleSheet glamour.TermRendererOption
	previewStyleCache map[chroma.TokenType]lipgloss.Style
	collapseState     map[int]bool    // segment index → collapsed (for tool calls and thinking)
	segmentHeights    []int           // rendered line count per segment (recomputed in String())
	showThinking      bool            // from prefs; when false skip thinking segments
	compaction        compactionState // when true skip thinking chunks from compaction
	streamingPhase    string          // "thinking" | "tool" | "answer" | ""
	streamingSource   output.ChunkSource
	tickCount         int   // incremented by 500ms tick, used for cursor blink
	lastRenderErr     error // captures the last render error for logging
	// delegation tracking
	activeDelegations       map[string]int // agentID → segment index (for in-flight delegations)
	pendingDelegateParents  []int          // segment indexes awaiting DelegationStartedEvent binding
	pendingDelegationStarts []int          // segment indexes awaiting parent delegate tool binding
	activeAdvisorSegment    int            // 1-based segment index; 0 means none active
	skillNames              []string       // skill names for command prefix matching
	maxDelegationBodyLines  int            // max lines for delegation body (transcript + prompt); 0 = uncapped
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
	output.EventTypeAPIRequest:             func(*contentBuffer, output.Event) {},
	output.EventTypeAPIResponse:            func(b *contentBuffer, _ output.Event) { b.finishStreaming() },
	// SteerReceived is handled by model_events.go (PromoteLastPendingSteer); no
	// content line is emitted here.
	output.EventTypeSteerReceived: func(*contentBuffer, output.Event) {},
}

func (b *contentBuffer) AppendEvent(event output.Event) {
	if event.Scope.AgentID != "" && b.appendScopedDelegationEvent(event) {
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
