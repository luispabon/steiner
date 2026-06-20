package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/luispabon/steiner/internal/interactive"
	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/session"
	"github.com/luispabon/steiner/internal/tui/theme"
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
	selectedAction int
}

type workflowHandoffLaunch struct {
	next      string
	target    string
	modelName string
}

type tickMsg struct{}

type paletteSetAccentMsg struct{ preset string }

type paletteToggleThinkingMsg struct{}

type paletteSwitchModelMsg struct{ name string }

type paletteClearMsg struct{}

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
	viewport viewport.Model
	input    textarea.Model
	content  contentBuffer
	status   statusState
	sidebar  sidebarState
	git      *gitState

	approval                     approvalState
	activity                     activityState
	external                     <-chan tea.Msg
	autoScroll                   bool
	contentTopPad                int
	skillNames                   []string
	skillDescriptions            map[string]string
	enabledSkills                map[string]bool
	modelNames                   []string
	modelContexts                map[string]int
	modelBaseURLs                map[string]string
	modelProviderNames           map[string]string
	controller                   interactive.Controller
	activeTheme                  theme.Theme
	styles                       theme.Styles
	inputHistory                 []string
	historyIdx                   int
	historyDraft                 string
	fileHistory                  []string
	fileHistoryIdx               int
	completionCandidates         []string
	completionIdx                int
	helpVisible                  bool
	showThinking                 bool
	compacting                   bool
	accentPreset                 string
	sidebarPosition              string
	palette                      paletteModel
	slashOverlay                 slashOverlay
	fileList                     fileListOverlay
	filePicker                   filePickerOverlay
	sessionPicker                sessionPickerOverlay
	oneshotResumePicker          oneshotResumePickerOverlay
	modelPicker                  modelPickerOverlay
	planPicker                   planPickerOverlay
	contextOverlay               contextOverlayState
	exitModal                    exitModalState
	workflowHandoff              workflowHandoffModalState
	sessionStore                 SessionLister
	showContextDiagnostics       bool
	sessionHealthCompactionCount int
	sessionHealthTurn            int
	sessionHealthState           string
	sessionHealthGuidance        string
	sessionHealthNotes           []string
	ctxInfoPromptTokens          int
	ctxInfoContextWindow         int
	ctxInfoContextUsagePercent   float64
	ctxInfoCompactionThreshold   float64
	ctxInfoEstimatorPadTokens    int
	ctxInfoStatus                string
	steerQueued                  bool // true when a steer message has been queued but not yet consumed
	interruptPending             bool
	suppressWorkflowHandoffRun   bool
	pendingWorkflowHandoffLaunch *workflowHandoffLaunch
	contentDirty                 bool
	syncDebounceSeq              int
	mousePressX                  int
	mousePressY                  int
	selection                    selectionState
	screenLines                  *[]string
	lastWheelMouseAt             time.Time
	primaryModel                 string
	imageMarkers                 []imageMarker
	oneshotRunning               bool
	oneshotPhase                 string
	oneshotSteerCh               chan string
	oneshotRunnerFactory         OneshotRunnerFactoryBuilder
}

func (m *Model) applyModelSelection(modelName, providerBaseURL string) {
	m.primaryModel = strings.TrimSpace(modelName)
	m.status.model = m.primaryModel
	m.sidebar.model = modelName
	m.sidebar.provider = strings.TrimSpace(providerBaseURL)
	if name, ok := m.modelProviderNames[modelName]; ok {
		m.sidebar.providerName = name
	}
	m.sidebar.contextBudget = m.contextBudgetForModel(modelName)
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

func compactionSidebarSummary(payload output.ContextCompactionEvent) string {
	parts := make([]string, 0, 4)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		if severity == "compacting" {
			return "compacting…"
		}
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("#%d", payload.CompactionCount))
	}
	if state := strings.TrimSpace(payload.SessionState); state != "" {
		parts = append(parts, state)
	}
	if guidance := strings.TrimSpace(payload.RestartGuidance); guidance != "" {
		parts = append(parts, guidance)
	}
	if len(parts) == 0 {
		switch {
		case strings.TrimSpace(payload.SummaryTitle) != "":
			return payload.SummaryTitle
		case payload.CompactedTurns > 0 || payload.CompactedMessages > 0:
			return fmt.Sprintf("compacted %d/%d", payload.CompactedTurns, payload.CompactedMessages)
		default:
			return "compacting"
		}
	}
	return strings.Join(parts, " ")
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

func sessionHealthSidebarSummary(payload output.ContextSessionHealthEvent) string {
	parts := make([]string, 0, 4)
	if severity := strings.TrimSpace(payload.Severity); severity != "" {
		if severity == "compacting" {
			return "compacting…"
		}
		parts = append(parts, severity)
	}
	if payload.CompactionCount > 0 {
		parts = append(parts, fmt.Sprintf("#%d", payload.CompactionCount))
	}
	if state := strings.TrimSpace(payload.SessionState); state != "" {
		parts = append(parts, state)
	}
	if guidance := strings.TrimSpace(payload.RestartGuidance); guidance != "" {
		parts = append(parts, guidance)
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
	m.ctxInfoPromptTokens = payload.PromptTokens
	m.ctxInfoContextWindow = contextWindow
	m.ctxInfoContextUsagePercent = payload.ContextUsagePercent
	m.ctxInfoCompactionThreshold = payload.CompactionThreshold
	m.ctxInfoEstimatorPadTokens = payload.EstimatorPadTokens
	m.ctxInfoStatus = payload.Status
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
