package tui

import (
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/tui/theme"
)

type contentSegmentKind int

const (
	segmentPlain contentSegmentKind = iota
	segmentAssistantProse
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
	segmentInterrupted
	segmentDelegation
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
	args           string // summarized args, ~60 chars max
	meta           string // "✅" or "❌" for finished calls
	bodyKind       string // "bash", "file", "glob", "grep", "ls", "plain"
	body           string // raw result text
	callID         string // for matching started→finished
	collapsed      bool   // default true
	hasError       bool   // set when ToolCallFinished carries an error
	rawArgs        map[string]any
	preview        output.ToolPreview
	displayPreview *output.PreviewDocument
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
}

type contentSegment struct {
	kind           contentSegmentKind
	text           string
	thinkData      *thinkingBlockData      // non-nil only for segmentThinkingBlock
	toolData       *toolCallSegment        // non-nil only for segmentToolCall
	toolGroupData  *toolCallGroupSegment   // non-nil only for segmentToolCallGroup
	approvalData   *approvalPillData       // non-nil only for segmentApprovalPill
	compactionData *compactionBannerData   // non-nil only for segmentCompactionBanner
	delegData      *delegationDisplayState // non-nil only for segmentDelegation
	// render cache
	cachedRender      string
	cachedRenderWidth int
	renderDirty       bool
}

// spinnerFrames is the braille spinner sequence used for active delegations.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

type contentBuffer struct {
	segments                      []contentSegment
	streaming                     bool
	hadChunks                     bool
	streamBuffer                  string
	renderer                      *glamour.TermRenderer
	renderWidth                   int
	styles                        theme.Styles
	glamourStyleSheet             glamour.TermRendererOption
	previewStyleCache             map[chroma.TokenType]lipgloss.Style
	collapseState                 map[int]bool // segment index → collapsed (for tool calls and thinking)
	segmentHeights                []int        // rendered line count per segment (recomputed in String())
	showThinking                  bool         // from prefs; when false skip thinking segments
	showInternalScaffoldInference bool
	inCompaction                  bool   // when true skip thinking chunks from compaction
	streamingPhase                string // "thinking" | "tool" | "answer" | ""
	streamingSource               output.ChunkSource
	tickCount                     int   // incremented by 500ms tick, used for cursor blink
	lastRenderErr                 error // captures the last render error for logging
	// delegation tracking
	activeDelegations       map[string]int // agentID → segment index (for in-flight delegations)
	pendingDelegateParents  []int          // segment indexes awaiting DelegationStartedEvent binding
	pendingDelegationStarts []int          // segment indexes awaiting parent delegate tool binding
	skillNames              []string       // skill names for command prefix matching
}

type contentEventHandler func(*contentBuffer, output.Event)

var contentEventHandlers = map[string]contentEventHandler{
	output.EventTypeThinkingChunk:      (*contentBuffer).appendThinkingChunkEvent,
	output.EventTypeAssistantChunk:     (*contentBuffer).appendAssistantChunkEvent,
	output.EventTypeApprovalRequested:  (*contentBuffer).appendApprovalRequestedEvent,
	output.EventTypeApprovalAccepted:   (*contentBuffer).appendApprovalDecisionEvent,
	output.EventTypeApprovalDenied:     (*contentBuffer).appendApprovalDecisionEvent,
	output.EventTypeDelegationStarted:  (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationComplete: (*contentBuffer).appendDelegationEvent,
	output.EventTypeDelegationFailed:   (*contentBuffer).appendDelegationEvent,
	output.EventTypeToolCallStarted:    (*contentBuffer).appendToolCallStartedEvent,
	output.EventTypeToolCallFinished:   (*contentBuffer).appendToolCallFinishedEvent,
	output.EventTypeDisplayFile:        (*contentBuffer).appendDisplayFileEvent,
	output.EventTypeStopReason:         (*contentBuffer).appendStopReasonEvent,
	output.EventTypeAssistantMessage:   (*contentBuffer).appendAssistantMessageEvent,
	output.EventTypeContextReport:      (*contentBuffer).appendContextReportEvent,
	output.EventTypeModelCallStarted:   (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeModelCallFinished:  (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeContextDiagnostics: (*contentBuffer).appendModelCallDiagnosticsEvent,
	output.EventTypeUserInput:          (*contentBuffer).appendUserInputEvent,
	output.EventTypeRunStarted:         func(*contentBuffer, output.Event) {},
	output.EventTypeRunFinished:        func(*contentBuffer, output.Event) {},
	output.EventTypeTurnStarted:        func(*contentBuffer, output.Event) {},
	output.EventTypeTurnFinished:       func(*contentBuffer, output.Event) {},
	output.EventTypeAPIRequest:         func(*contentBuffer, output.Event) {},
	output.EventTypeAPIResponse:        func(b *contentBuffer, _ output.Event) { b.finishStreaming() },
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
