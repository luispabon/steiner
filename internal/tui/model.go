package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/config"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/provider"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
	"github.com/luispabon/steiner/internal/usagestats"
)

// SessionLister is the interface for querying and loading sessions.
type SessionLister interface {
	List() ([]session.IndexEntry, error)
	Load(id string) (session.Session, error)
}

// steerQueue is the pending steering-message queue the composer enqueues
// into, the queued-message box renders from, and ctrl+g takes back from.
// *agent.SteerQueue satisfies it.
type steerQueue interface {
	Add(agent.SteerMessage)
	Drain() []agent.SteerMessage
	Snapshot() []agent.SteerMessage
	Len() int
}

type approvalState struct {
	active         bool
	tool           string
	mode           string
	preview        string
	kind           string // "path" or "mcp"
	server         string // MCP server name (empty for path)
	mcpToolName    string // MCP tool name (empty for path)
	identity       string
	selectedAction int
}

type workflowHandoffLaunch struct {
	next       string
	target     string
	submission string
	modelAlias string
	waiting    bool
	cancel     context.CancelFunc
}

type tickMsg struct{}
type sessionTickMsg struct{}
type composerBlinkMsg struct{}

type setAccentMsg struct{ preset string }

type toggleThinkingMsg struct{}

const (
	exitFlowPhaseNone = iota
	exitFlowPhaseCounting
	exitFlowPhaseCleanup
)

const (
	inputRailWidth = 1
	inputPadX      = 1
	inputPadY      = 1
	inputTailFill  = 1
)

// Model owns the interactive TUI state and rendering pipeline.
type Model struct {
	width    int
	height   int
	viewport scrollModel
	frame    *frameStats
	input    textarea.Model
	content  contentBuffer
	status   statusState
	sidebar  sidebarState
	git      *gitState

	approval      approvalState
	activity      activityState
	external      <-chan tea.Msg
	autoScroll    bool
	contentTopPad int
	// sub-agent jump state (delegation_jump.go)
	jumpTarget          occurrenceKey // last jump target; zero value = none
	jumpAt              time.Time     // when jumpTarget was set; gates the alt+/ expand window
	jumpFlashEpoch      int           // bumped on every jump; stale flash ticks are ignored
	jumpFlashLeft       int           // remaining half-phases of the active flash
	skillNames          []string
	skillDescriptions   map[string]string
	mcpEnabled          bool
	lspEnabled          bool
	subAgentsEnabled    bool
	orchestrationLevel  config.OrchestrationLevel
	mcpServers          []MCPServerStatus
	lspServers          []LSPServerStatus
	mcpToolOrigins      map[string]MCPToolOrigin
	mcpWarned           map[string]bool // servers that already surfaced a failure warning in the current failure generation
	enabledSkills       map[string]bool
	modelNames          []string
	modelEntries        []ModelEntry
	modelEntriesUpdates <-chan []ModelEntry
	modelBackendAlias   map[string]string
	modelContexts       map[string]int
	modelBaseURLs       map[string]string
	modelProviderNames  map[string]string
	controller          interactive.Controller
	recorder            *usagestats.Recorder
	activeTheme         theme.Theme
	palette             theme.Palette
	// styles is shared by pointer across Model and every sub-component that
	// embeds a styles field; theme.Styles must be treated as immutable after
	// construction, and the accent-change path must allocate a fresh Styles
	// rather than mutating this one in place.
	styles                       *theme.Styles
	historyDraft                 string
	fileHistory                  []string
	fileHistoryIdx               int
	completionCandidates         []string
	completionIdx                int
	helpVisible                  bool
	showThinking                 bool
	compaction                   compactionState
	accentPreset                 string
	sidebarPosition              string
	slashOverlay                 slashOverlay
	fileList                     fileListOverlay
	mcpOverlay                   mcpOverlay
	lspOverlay                   lspOverlay
	filePicker                   filePickerOverlay
	sessionPicker                sessionPickerOverlay
	subAgentPicker               subAgentPickerOverlay
	oneshotResumePicker          oneshotResumePickerOverlay
	modelPicker                  modelPickerOverlay
	modelReasoningCapabilities   map[string]provider.ReasoningCapabilities
	modelReasoningEfforts        map[string]string
	currentModelAlias            string
	reasoningPicker              reasoningPickerOverlay
	reasoningLabels              map[string]string
	resolveReasoningFunc         func() (map[string]provider.ReasoningCapabilities, map[string]string)
	checkUpdateFunc              func() (string, bool)
	pollLSPStatesFunc            func() []LSPServerStatus
	worktreePlan                 *WorktreeCleanupPlan
	exitFlowPhase                int
	exitCountCancel              context.CancelFunc
	worktreeCleanupModal         confirmModalState
	resolveReasoningForAliasFunc func(alias string) (provider.ReasoningCapabilities, string)
	reasoningBatchResolved       bool
	planPicker                   planPickerOverlay
	accentPicker                 accentPickerOverlay
	profilePicker                profilePickerOverlay
	orchestrationPicker          orchestrationPickerOverlay
	orchestrationConfirm         confirmModalState
	pendingOrchestrationLevel    config.OrchestrationLevel
	contextOverlay               contextOverlayState
	usageOverlay                 usageOverlayState
	exitModal                    confirmModalState
	workflowHandoff              workflowHandoffModalState
	delegateCancelModal          delegateCancelModalState
	sessionStore                 SessionLister
	interruptPending             bool
	convState                    output.ConversationStateEvent
	convStateSeen                bool
	convLabelShown               bool
	strandedResults              int // sub-agent results delivered but never answered; drives the activity-row warning
	suppressWorkflowHandoffRun   bool
	pendingWorkflowHandoffLaunch *workflowHandoffLaunch
	contentDirty                 bool
	syncDebounceSeq              int
	reflow                       resizeReflow
	mousePressX                  int
	mousePressY                  int
	sidebarPressX, sidebarPressY int // sidebar press cell awaiting release; -1 when none
	selection                    selectionState
	lastClickTime                time.Time
	lastClickPos                 selectionPoint
	clickCount                   int
	activeRegion                 selectionRegion
	screenLines                  []string
	screenFrame                  string // last pre-highlight frame rendered during a drag; stripped into screenLines on demand
	screenFramePending           bool
	highlightCache               highlightCache
	highlightCacheStyles         *theme.Styles
	dragScrollDir                int // 0 none, -1 up, 1 down while drag-hovering a viewport edge
	dragScrollTicking            bool
	dragScrollEpoch              int
	dragLastX                    int
	dragLastY                    int
	lastWheelMouseAt             time.Time
	lastMouseMotionAt            time.Time // buttonless motion, seen only by filterMouseMotion
	pointer                      pointerPos
	rosterLayoutCache            rosterLayoutCache
	primaryModel                 string
	profileNames                 []string
	imageMarkers                 []imageMarker
	localImageCounter            int
	oneshotRunning               bool
	oneshotPhase                 string
	steers                       steerQueue
	oneshotRunnerFactory         OneshotRunnerFactoryBuilder
	notifier                     notifier
	mode                         string // current execution mode: "plan" or "build"
	ticking                      bool
	imageStore                   *agent.ImageStore
	visionCapabilities           *agent.VisionCapabilities
	sessionResetCleanup          func()
	clearConversationHooks       func()
	sessionStartedAt             *time.Time

	// Render caches for width/height-dependent styles.
	hDividerCacheWidth      int
	hDividerCacheRendered   string
	scrollbarCacheKey       scrollbarCacheKey
	scrollbarCacheRendered  string
	scrollbarCellStyles     *theme.Styles
	scrollbarThumbCell      string
	scrollbarTrackCell      string
	padLineCacheWidth       int
	padLineCacheRendered    string
	fmtBgCacheWidth         int
	bgFormat                bgFormatCache
	vpViewCache             string
	overlayCache            overlayComposeCache
	helpCompose             overlayComposeCache
	overlayMemos            overlayRenderMemos
	vpViewCacheScrollY      int
	vpViewCacheWidth        int
	vpViewCacheHasScrollbar bool

	statusViewCacheSet      bool
	statusViewCacheKey      statusState
	statusViewCacheWidth    int
	statusViewCacheRendered string

	sidebarViewCacheSet      bool
	sidebarRenders           int // renderSidebar cache misses
	sidebarViewCacheKey      sidebarCacheKey
	sidebarViewCacheFiles    []gitModifiedFile
	sidebarViewCacheRoster   []rosterEntry
	roster                   subAgentRoster
	sidebarViewCacheRendered string

	activityViewCacheSet      bool
	activityViewCacheKey      activityCacheKey
	activityViewCacheRendered string

	inputViewCacheSet      bool
	inputViewCacheKey      inputViewCacheKey
	inputViewCacheSkills   []string
	inputViewCacheRendered string

	vDividerCacheStyles   *theme.Styles
	vDividerCacheHeight   int
	vDividerCacheRendered string

	composerBlinkOn  bool
	composerBlinking bool

	lastCursorLine int
	lastCursorCol  int
}

func (m *Model) resolvedPalette() theme.Palette {
	if m.palette.SidebarBG != "" && m.palette.ContentBG != "" {
		return m.palette
	}
	if m.styles != nil && m.styles.Palette.SidebarBG != "" {
		return m.styles.Palette
	}
	return theme.DefaultPalette()
}

type scrollbarCacheKey struct {
	yOffset, height, totalLines int
}

// inputViewCacheKey is the full render cache key for renderInputView:
// every model field the input render path reads, plus the render
// dimensions and blink state. skillNames is compared separately because
// a slice cannot be part of a comparable struct. Package globals read by
// the path (slashCommands, imageMarkerPattern, commandsAcceptingArbitraryArgs)
// are assigned only at package init and never written at runtime, so
// they are deliberately absent from the key. Render now depends on blink
// phase, and composerBlinkMsg (not a timestamp) drives the invalidation edge.
type inputViewCacheKey struct {
	contentWidth   int
	height         int
	value          string
	cursorLine     int
	cursorColumn   int
	placeholder    string
	oneshotRunning bool
	styles         *theme.Styles
	cursorBlinkOn  bool
}

func (m *Model) applyModelSelection(modelName, providerBaseURL string) {
	m.primaryModel = strings.TrimSpace(modelName)
	m.currentModelAlias = m.primaryModel
	m.status.model = m.primaryModel
	m.status.reasoning = m.reasoningLabels[modelName]
	m.sidebar.model = modelName
	m.sidebar.provider = strings.TrimSpace(providerBaseURL)
	if name, ok := m.modelProviderNames[modelName]; ok {
		m.sidebar.providerName = name
	}
	m.sidebar.contextBudget = m.contextBudgetForModel(modelName)
	m.sidebar.reasoning = m.reasoningLabels[modelName]
	m.sidebar.promptUsed = 0
	m.sidebar.budgetUsed = 0
	if m.sidebar.contextBudget > 0 {
		m.status.context = fmt.Sprintf("ctx 0/%d", m.sidebar.contextBudget)
	} else {
		m.status.context = ""
	}
	m.syncSidebar()
}

func (m *Model) syncSidebar() {
	m.sidebar.model = strings.TrimSpace(m.primaryModel)
	m.sidebar.provider = strings.TrimSpace(m.sidebar.provider)
	m.sidebar.providerName = strings.TrimSpace(m.sidebar.providerName)
	m.sidebar.activeSkill = m.activeSkillName()
	snap := m.git.Snapshot()
	m.sidebar.branch = snap.branch
	m.sidebar.dirty = snap.dirty
	m.sidebar.ahead = snap.ahead
	m.sidebar.modifiedFiles = snap.modifiedFiles
	m.sidebar.workingDir = strings.TrimSpace(m.sidebar.workingDir)
	if m.recorder != nil {
		sr := m.recorder.SessionReportFor(usagestats.SourceParent)
		rate, ok := sr.HitRate()
		m.sidebar.sessionCacheHitRate = rate
		m.sidebar.sessionCacheHitRateOK = ok
		lastRate, lastOK := sr.LastHitRate()
		m.sidebar.lastRequestCacheRate = lastRate
		m.sidebar.lastRequestCacheRateOK = lastOK
	}
	m.syncRoster()
	m.sidebar.sessionActive = m.sessionStartedAt != nil
	m.sidebar.sessionElapsedSec = 0
	if m.sessionStartedAt != nil {
		m.sidebar.sessionElapsedSec = int64(time.Since(*m.sessionStartedAt) / time.Second)
	}
	m.sidebar.mcpConnected = 0
	m.sidebar.mcpTotal = 0
	m.sidebar.mcpConnecting = false
	m.sidebar.mcpFailed = false
	for _, server := range m.mcpServers {
		if server.State == "disabled" {
			continue
		}
		m.sidebar.mcpTotal++
		if server.State == "connected" {
			m.sidebar.mcpConnected++
		}
		if server.State == "connecting" || server.State == "reconnecting" {
			m.sidebar.mcpConnecting = true
		}
		if server.State == "failed" {
			m.sidebar.mcpFailed = true
		}
	}
	m.sidebar.lspServers = sortLSPServerStatuses(m.lspServers)
	m.sidebar.recomputeLSPAggregate()
}

func (m *Model) activeSkillName() string {
	if m == nil {
		return ""
	}
	for _, name := range m.skillNames {
		if m.enabledSkills[name] {
			return name
		}
	}
	return ""
}

// resolveReasoningForAliasIfPending synchronously resolves reasoning
// capabilities for a single model alias when nothing is yet known about that
// alias — i.e. it is absent from m.modelReasoningCapabilities. This covers
// both the startup window (before the async modelReasoningResolvedMsg
// arrives) and models the startup batch never covers at all, such as a
// discovered-but-unaliased model that isn't listed under cfg.Models.Definitions.
// Once an alias has an entry in m.modelReasoningCapabilities — whether from
// the batch or from a prior on-demand resolve — this is a no-op for that
// alias.
func (m *Model) resolveReasoningForAliasIfPending(alias string) *Model {
	if _, known := m.modelReasoningCapabilities[alias]; known || m.resolveReasoningForAliasFunc == nil {
		return m
	}
	caps, effort := m.resolveReasoningForAliasFunc(alias)
	if m.modelReasoningCapabilities == nil {
		m.modelReasoningCapabilities = make(map[string]provider.ReasoningCapabilities)
	}
	m.modelReasoningCapabilities[alias] = caps
	if m.modelReasoningEfforts == nil {
		m.modelReasoningEfforts = make(map[string]string)
	}
	m.modelReasoningEfforts[alias] = effort
	if label := reasoningSidebarLabel(effort, caps); label != "" {
		if m.reasoningLabels == nil {
			m.reasoningLabels = make(map[string]string)
		}
		m.reasoningLabels[alias] = label
	}
	return m
}

func waitForModelEntries(ch <-chan []ModelEntry) tea.Cmd {
	return func() tea.Msg {
		entries, ok := <-ch
		return modelEntriesUpdatedMsg{entries: entries, ok: ok}
	}
}

func waitForExternalMsg(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		if ch == nil {
			return bridgeClosedMsg{}
		}
		msg, ok := <-ch
		if !ok {
			return bridgeClosedMsg{}
		}
		return msg
	}
}

func (m *Model) syncInputChrome() {
	m.input.Prompt = ""
	switch {
	case m.oneshotRunning:
		m.input.Placeholder = "steering — esc to interrupt (or /exit, /thinking, /accent)"
	case m.approval.active:
		m.input.Placeholder = "approval pending above — use arrows, tab, enter, or esc"
	case m.activity.busy() && !m.ordinaryConversationWaiting():
		pending := 0
		if m.steers != nil {
			pending = m.steers.Len()
		}
		if pending > 0 {
			m.input.Placeholder = queuedSteerPlaceholder(pending)
		} else {
			m.input.Placeholder = "working… esc to interrupt, or type to steer"
		}
	default:
		m.input.Placeholder = "ask steiner — / for commands, @ for files"
	}
	m.status.approvalActive = m.approval.active
	m.status.streaming = m.activity.busy() && !m.ordinaryConversationWaiting() && !m.approval.active
}

// needsTicking reports whether the ticker needs to keep firing.
func (m *Model) needsTicking() bool {
	return m.activity.spinning ||
		m.content.streaming ||
		m.compaction.Active() ||
		m.content.HasActiveDelegations() ||
		m.content.HasActiveToolCalls() ||
		m.content.HasActiveCompactions() ||
		m.sidebar.mcpConnecting ||
		m.sidebar.lspStarting ||
		(m.usageOverlay.IsOpen() && m.usageOverlay.refreshing()) ||
		m.roster.hasRunning() ||
		m.contentDirty
}

// ensureTicking restarts the ticker if it is not already running.
// Returns a tickCmd if the ticker was restarted, nil otherwise.
func (m *Model) ensureTicking() tea.Cmd {
	if m.ticking {
		return nil
	}
	m.ticking = true
	return tickCmd()
}

// ensureComposerBlinking restarts the composer blink ticker if the composer
// has focus and the ticker is not already running.
// Returns a composerBlinkCmd if the ticker was restarted, nil otherwise.
func (m *Model) ensureComposerBlinking() tea.Cmd {
	if m.composerBlinking || !m.input.Focused() {
		return nil
	}
	m.composerBlinking = true
	m.composerBlinkOn = true
	return composerBlinkCmd()
}
