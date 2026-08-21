package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/luispabon/steiner/internal/agent"

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

type compactionState struct {
	active  bool
	summary string
}

func (c compactionState) Active() bool           { return c.active }
func (c compactionState) SuppressThinking() bool { return c.active }
func (c compactionState) SidebarLabel() string {
	if c.active {
		return "compacting…"
	}
	return c.summary
}

func newCompactionState(payload output.ContextCompactionEvent) compactionState {
	return compactionState{
		active:  payload.Severity == "compacting",
		summary: "",
	}
}

// setCompaction fans the compaction state out to every reader (model,
// content buffer, sidebar) from one place so the copies cannot desync.
func (m *Model) setCompaction(cs compactionState) {
	m.compaction = cs
	m.content.compaction = cs
	m.sidebar.compaction = cs
}

type workflowHandoffLaunch struct {
	next       string
	target     string
	submission string
}

type tickMsg struct{}

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
	input    textarea.Model
	content  contentBuffer
	status   statusState
	sidebar  sidebarState
	git      *gitState

	approval           approvalState
	activity           activityState
	external           <-chan tea.Msg
	autoScroll         bool
	contentTopPad      int
	skillNames         []string
	skillDescriptions  map[string]string
	mcpEnabled         bool
	mcpServers         []MCPServerStatus
	mcpToolOrigins     map[string]MCPToolOrigin
	mcpWarned          map[string]bool // servers that already surfaced a failure warning in the current failure generation
	enabledSkills      map[string]bool
	modelNames         []string
	modelBackendAlias  map[string]string
	modelContexts      map[string]int
	modelBaseURLs      map[string]string
	modelProviderNames map[string]string
	controller         interactive.Controller
	recorder           *usagestats.Recorder
	activeTheme        theme.Theme
	// styles is shared by pointer across Model and every sub-component that
	// embeds a styles field; theme.Styles must be treated as immutable after
	// construction, and the accent-change path must allocate a fresh Styles
	// rather than mutating this one in place.
	styles                       *theme.Styles
	inputHistory                 []string
	historyIdx                   int
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
	filePicker                   filePickerOverlay
	sessionPicker                sessionPickerOverlay
	oneshotResumePicker          oneshotResumePickerOverlay
	modelPicker                  modelPickerOverlay
	modelReasoningCapabilities   map[string]provider.ReasoningCapabilities
	modelReasoningEfforts        map[string]string
	currentModelAlias            string
	reasoningPicker              reasoningPickerOverlay
	reasoningLabels              map[string]string
	resolveReasoningFunc         func() (map[string]provider.ReasoningCapabilities, map[string]string)
	worktreePlan                 *WorktreeCleanupPlan
	exitFlowPhase                int
	worktreeCleanupModal         worktreeCleanupModalState
	resolveReasoningForAliasFunc func(alias string) (provider.ReasoningCapabilities, string)
	reasoningBatchResolved       bool
	planPicker                   planPickerOverlay
	accentPicker                 accentPickerOverlay
	contextOverlay               contextOverlayState
	exitModal                    exitModalState
	workflowHandoff              workflowHandoffModalState
	sessionStore                 SessionLister
	steerQueued                  bool // true when a steer message has been queued but not yet consumed
	interruptPending             bool
	suppressWorkflowHandoffRun   bool
	pendingWorkflowHandoffLaunch *workflowHandoffLaunch
	contentDirty                 bool
	syncDebounceSeq              int
	mousePressX                  int
	mousePressY                  int
	selection                    selectionState
	lastClickTime                time.Time
	lastClickPos                 selectionPoint
	clickCount                   int
	activeRegion                 selectionRegion
	screenLines                  []string
	dragScrollDir                int // 0 none, -1 up, 1 down while drag-hovering a viewport edge
	dragScrollTicking            bool
	dragScrollEpoch              int
	dragLastX                    int
	dragLastY                    int
	lastWheelMouseAt             time.Time
	primaryModel                 string
	imageMarkers                 []imageMarker
	oneshotRunning               bool
	oneshotPhase                 string
	oneshotSteerCh               chan agent.SteerMessage
	oneshotRunnerFactory         OneshotRunnerFactoryBuilder
	notifier                     notifier
	mode                         string // current execution mode: "plan" or "build"
	ticking                      bool
	imageStore                   *agent.ImageStore
	visionCapabilities           *agent.VisionCapabilities
	sessionResetCleanup          func()

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
	fmtBgCacheInput         string
	fmtBgCacheWidth         int
	fmtBgCacheOutput        string
	vpViewCache             string
	vpViewCacheScrollY      int
	vpViewCacheWidth        int
	vpViewCacheHasScrollbar bool

	statusViewCacheSet      bool
	statusViewCacheKey      statusState
	statusViewCacheWidth    int
	statusViewCacheRendered string

	sidebarViewCacheSet      bool
	sidebarViewCacheKey      sidebarCacheKey
	sidebarViewCacheFiles    []gitModifiedFile
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
}

type scrollbarCacheKey struct {
	yOffset, height, totalLines int
}

// inputViewCacheKey is the full render cache key for renderInputView:
// every model field the input render path reads, plus the render
// dimensions. skillNames is compared separately because a slice cannot
// be part of a comparable struct. Package globals read by the path
// (slashCommands, imageMarkerPattern, commandsAcceptingArbitraryArgs)
// are assigned only at package init and never written at runtime, so
// they are deliberately absent from the key.
type inputViewCacheKey struct {
	contentWidth   int
	height         int
	value          string
	cursorLine     int
	cursorColumn   int
	placeholder    string
	oneshotRunning bool
	styles         *theme.Styles
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
	if snap := m.git.Snapshot(); snap.ready {
		m.sidebar.branch = snap.branch
		m.sidebar.dirty = snap.dirty
		m.sidebar.ahead = snap.ahead
		m.sidebar.modifiedFiles = append([]gitModifiedFile(nil), snap.modifiedFiles...)
	}
	m.sidebar.workingDir = strings.TrimSpace(m.sidebar.workingDir)
	if m.recorder != nil {
		sr := m.recorder.SessionReportFor(usagestats.SourceParent)
		rate, ok := sr.HitRate()
		m.sidebar.sessionCacheHitRate = rate
		m.sidebar.sessionCacheHitRateOK = ok
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

func appendStatusContext(base, fragment string) string {
	base = strings.TrimSpace(base)
	fragment = strings.TrimSpace(fragment)
	if fragment == "" {
		return base
	}
	if base == "" {
		return fragment
	}
	return base + " | " + fragment
}

func compactionStatusFragment(payload output.ContextCompactionEvent) string {
	parts := make([]string, 0, 3)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compaction #%d", payload.CompactionCount))
	}
	if mode := strings.TrimSpace(payload.Mode); mode != "" {
		parts = append(parts, "mode "+mode)
	}
	if payload.Mode != "" || payload.SummaryTokenBudget > 0 || payload.ThresholdAchieved {
		parts = append(parts, fmt.Sprintf("threshold achieved %t", payload.ThresholdAchieved))
	}
	if payload.Severity == "critical" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart now")
	} else if payload.Severity == "warning" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart soon")
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func sessionHealthStatusFragment(payload output.ContextSessionHealthEvent) string {
	parts := make([]string, 0, 3)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("compaction #%d", payload.CompactionCount))
	}
	if payload.Severity == "critical" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart now")
	} else if payload.Severity == "warning" && strings.TrimSpace(payload.RestartGuidance) != "" {
		parts = append(parts, "restart soon")
	}
	return strings.Join(parts, " ")
}

func cloneModelProviderNames(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelBaseURLs(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelReasoningCapabilities(src map[string]provider.ReasoningCapabilities) map[string]provider.ReasoningCapabilities {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]provider.ReasoningCapabilities, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneModelContexts(src map[string]int) map[string]int {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func resolveModelBadge(backend string, aliases, reasoningLabels map[string]string) (string, string) {
	alias := backend
	if a, ok := aliases[backend]; ok {
		alias = a
	}
	return alias, reasoningLabels[alias]
}

// reasoningSidebarLabel derives the sidebar/picker label for a model's
// reasoning state: the effective effort when set, "default" when
// the model is reasoning-capable but uses no explicit effort, or "" when the
// model has no reasoning capability at all.
func reasoningSidebarLabel(effort string, caps provider.ReasoningCapabilities) string {
	if effort != "" {
		return effort
	}
	if len(caps.SupportedEfforts) > 0 {
		return "default"
	}
	return ""
}

// newReasoningLabels seeds the per-alias sidebar/picker reasoning label from
// each model's config-declared effective effort and reasoning capabilities,
// so a configured effort is visible from startup rather than only after a
// picker selection.
func newReasoningLabels(efforts map[string]string, caps map[string]provider.ReasoningCapabilities) map[string]string {
	labels := make(map[string]string, len(caps))
	for name, c := range caps {
		if label := reasoningSidebarLabel(efforts[name], c); label != "" {
			labels[name] = label
		}
	}
	return labels
}

// resolveReasoningForAliasIfPending synchronously resolves reasoning
// capabilities for a single model alias when the batch resolution fired from
// Init has not completed yet. This covers the startup window where the user
// opens the /model picker and selects a model before the async
// modelReasoningResolvedMsg arrives, so the reasoning picker step is not
// silently skipped for a model that does support configurable effort. Once
// the batch resolution completes, this is a no-op.
func (m *Model) resolveReasoningForAliasIfPending(alias string) *Model {
	if m.reasoningBatchResolved || m.resolveReasoningForAliasFunc == nil {
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

func (m *Model) contextBudgetForModel(name string) int {
	if m == nil || len(m.modelContexts) == 0 {
		return 0
	}
	return m.modelContexts[strings.TrimSpace(name)]
}

func (m *Model) applyContextBudget(payload output.ContextBudgetEvent) {
	contextWindow := payload.ContextWindow
	if contextWindow <= 0 {
		contextWindow = payload.ContextTokens
	}
	if m == nil || contextWindow <= 0 {
		return
	}
	promptUsed := payload.PromptTokens
	if promptUsed < 0 {
		promptUsed = 0
	}
	budgetUsed := payload.TotalTokens
	if budgetUsed < 0 {
		budgetUsed = 0
	}
	if budgetUsed == 0 {
		budgetUsed = promptUsed
	}
	m.sidebar.promptUsed = promptUsed
	m.sidebar.budgetUsed = budgetUsed
	m.sidebar.contextBudget = contextWindow
	if payload.Turn > 0 {
		m.sidebar.currentTurn = payload.Turn
	}
	m.status.context = fmt.Sprintf("ctx %d/%d", promptUsed, contextWindow)
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
	case m.steerQueued && m.activity.busy():
		m.input.Placeholder = "message queued — esc to interrupt"
	case m.activity.busy():
		m.input.Placeholder = "working… esc to interrupt, or type to steer"
	default:
		m.input.Placeholder = "ask steiner — / for commands, @ for files"
	}
	m.status.approvalActive = m.approval.active
	m.status.streaming = m.activity.busy() && !m.approval.active
}

// needsTicking reports whether the ticker needs to keep firing.
func (m *Model) needsTicking() bool {
	return m.activity.spinning ||
		m.content.streaming ||
		m.compaction.Active() ||
		m.content.HasActiveDelegations() ||
		m.content.HasActiveCompactions() ||
		m.sidebar.mcpConnecting ||
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
