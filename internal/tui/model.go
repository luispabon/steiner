package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/luispabon/steiner/internal/output"
)

type approvalState struct {
	active  bool
	tool    string
	mode    string
	preview string
}

type Model struct {
	width         int
	height        int
	viewport      viewport.Model
	input         textinput.Model
	content       contentBuffer
	status        statusState
	sidebar       sidebarState
	git           *gitState
	keys          keyMap
	approval      approvalState
	external      <-chan tea.Msg
	autoScroll    bool
	skillNames    []string
	enabledSkills map[string]bool
	onSubmit      func(string)
	onApproval    func(bool)
	onSkillToggle func(string, bool)
}

func newModel(cfg Config, external <-chan tea.Msg) Model {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Ask steiner something"
	input.Focus()

	enabledSkills := make(map[string]bool, len(cfg.SkillNames))
	for _, name := range cfg.SkillNames {
		enabledSkills[name] = true
	}

	m := Model{
		width:         80,
		height:        24,
		viewport:      viewport.New(80, 22),
		input:         input,
		sidebar:       newSidebarState(),
		git:           newGitState(cfg.WorkingDir),
		keys:          defaultKeyMap(),
		external:      external,
		autoScroll:    true,
		skillNames:    append([]string(nil), cfg.SkillNames...),
		enabledSkills: enabledSkills,
		onSubmit:      cfg.OnSubmit,
		onApproval:    cfg.OnApproval,
		onSkillToggle: cfg.OnSkillToggle,
	}
	m.status.model = strings.TrimSpace(cfg.Model)
	m.sidebar.model = strings.TrimSpace(cfg.Model)
	m.sidebar.provider = strings.TrimSpace(cfg.ProviderBaseURL)
	m.sidebar.maxTurns = cfg.MaxTurns
	m.sidebar.workingDir = strings.TrimSpace(cfg.WorkingDir)
	m.git.Refresh(context.Background())
	m.syncSidebar()
	m.layout()
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus()}
	if m.external != nil {
		cmds = append(cmds, waitForExternalMsg(m.external))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil
	case runtimeEventMsg:
		m.applyEvent(msg.Event)
		if m.external != nil {
			return m, waitForExternalMsg(m.external)
		}
		return m, nil
	case bridgeClosedMsg:
		return m, nil
	case tea.MouseMsg:
		m.handleMouse(msg)
		return m, nil
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyCtrlB:
			m.sidebar.Toggle()
			m.layout()
			return m, nil
		case tea.KeyUp:
			m.scrollUp(1)
			return m, nil
		case tea.KeyDown:
			m.scrollDown(1)
			return m, nil
		case tea.KeyPgUp:
			m.scrollUp(maxInt(1, m.viewport.Height))
			return m, nil
		case tea.KeyPgDown:
			m.scrollDown(maxInt(1, m.viewport.Height))
			return m, nil
		case tea.KeyEnter:
			return m.handleEnter()
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m Model) View() string {
	contentWidth := maxInt(1, m.width)
	sidebarVisible := m.sidebar.Visible(m.width)
	if sidebarVisible {
		contentWidth = maxInt(1, m.width-sidebarWidth)
	}
	contentView := contentPaneStyle.Width(contentWidth).Render(m.viewport.View())
	if sidebarVisible {
		contentView = lipgloss.JoinHorizontal(lipgloss.Top, contentView, m.sidebar.View(m.width))
	}
	inputView := inputAreaStyle.Width(maxInt(1, m.width)).Render(m.input.View())
	statusView := m.status.view(m.width, m.keys.hints(m.approval.active))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		contentView,
		inputView,
		statusView,
	)
}

func (m *Model) layout() {
	contentHeight := m.height - 2
	if contentHeight < 1 {
		contentHeight = 1
	}
	contentWidth := m.width
	if m.sidebar.Visible(m.width) {
		contentWidth = m.width - sidebarWidth
	}
	if contentWidth < 1 {
		contentWidth = 1
	}
	m.viewport.Width = contentWidth
	m.viewport.Height = contentHeight
	m.input.Width = maxInt(0, contentWidth-4)
	m.syncViewport()
}

func (m *Model) syncViewport() {
	m.viewport.SetContent(m.content.String(m.viewport.Width))
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) applyEvent(event output.Event) {
	m.content.AppendEvent(event)

	switch payload := event.Payload.(type) {
	case output.RunStartedEvent:
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
		if payload.MaxTurns > 0 {
			m.sidebar.maxTurns = payload.MaxTurns
		}
		m.status.mode = "running"
	case output.RunFinishedEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
	case output.StopReasonEvent:
		m.status.mode = strings.TrimSpace(payload.Reason)
	case output.TurnStartedEvent:
		m.status.turn = payload.Turn
		m.sidebar.currentTurn = payload.Turn
		if payload.Model != "" {
			m.status.model = payload.Model
			m.sidebar.model = payload.Model
		}
	case output.ContextDiagnosticsEvent:
		if payload.Kind == "budget" && payload.BudgetBytes > 0 {
			m.status.context = fmt.Sprintf("ctx %d/%d", payload.UsedBytes, payload.BudgetBytes)
			m.sidebar.contextUsed = payload.UsedBytes
			m.sidebar.contextBudget = payload.BudgetBytes
		}
		if payload.Kind == "compaction" {
			switch {
			case strings.TrimSpace(payload.SummaryTitle) != "":
				m.sidebar.compaction = payload.SummaryTitle
			case payload.CompactedTurns > 0 || payload.CompactedMessages > 0:
				m.sidebar.compaction = fmt.Sprintf("compacted %d/%d", payload.CompactedTurns, payload.CompactedMessages)
			default:
				m.sidebar.compaction = "compacting"
			}
		}
	case output.ApprovalEvent:
		switch event.Type {
		case output.EventTypeApprovalRequested:
			m.approval = approvalState{
				active:  true,
				tool:    payload.Tool,
				mode:    payload.Mode,
				preview: payload.Preview,
			}
			m.status.mode = "approval"
			m.input.SetValue("")
			m.input.Prompt = "approve> "
			m.input.Placeholder = "yes or no"
		case output.EventTypeApprovalAccepted, output.EventTypeApprovalDenied:
			m.approval = approvalState{}
			m.status.mode = "running"
			m.input.Prompt = "> "
			m.input.Placeholder = "Ask steiner something"
		}
	}

	if event.Type == output.EventTypeToolCallFinished {
		m.git.Refresh(context.Background())
	}
	m.syncSidebar()
	m.syncViewport()
}

func (m *Model) syncSidebar() {
	m.sidebar.model = strings.TrimSpace(m.status.model)
	m.sidebar.provider = strings.TrimSpace(m.sidebar.provider)
	m.sidebar.currentTurn = m.status.turn
	m.sidebar.activeSkills = m.enabledSkillNames()
	if snap := m.git.Snapshot(); snap.ready {
		m.sidebar.branch = snap.branch
		m.sidebar.dirty = snap.dirty
	}
	m.sidebar.workingDir = strings.TrimSpace(m.sidebar.workingDir)
}

func (m Model) enabledSkillNames() []string {
	names := make([]string, 0, len(m.enabledSkills))
	for _, name := range m.skillNames {
		if m.enabledSkills[name] {
			names = append(names, name)
		}
	}
	return names
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	value := strings.TrimSpace(m.input.Value())
	if m.approval.active {
		allowed := isApprovalAccepted(value)
		if m.onApproval != nil {
			m.onApproval(allowed)
		}
		m.content.AppendLine(fmt.Sprintf("approval: %s", approvalDecisionText(allowed, m.approval.tool)))
		m.approval = approvalState{}
		m.status.mode = "running"
		m.input.Reset()
		m.input.Prompt = "> "
		m.input.Placeholder = "Ask steiner something"
		m.syncViewport()
		return m, nil
	}

	action := parseInput(value, m.enabledSkills)
	if action.quit {
		return m, tea.Quit
	}
	if action.clear {
		m.content.Clear()
		m.input.Reset()
		m.syncViewport()
		return m, nil
	}
	if action.listSkills {
		names := append([]string(nil), m.skillNames...)
		slices.Sort(names)
		if len(names) == 0 {
			m.content.AppendLine("status: no skills configured")
		} else {
			m.content.AppendLine("status: skills " + strings.Join(names, ", "))
		}
		m.input.Reset()
		m.syncViewport()
		return m, nil
	}
	if action.toggleSkill != "" {
		m.enabledSkills[action.toggleSkill] = action.toggleEnable
		if m.onSkillToggle != nil {
			m.onSkillToggle(action.toggleSkill, action.toggleEnable)
		}
		state := "disabled"
		if action.toggleEnable {
			state = "enabled"
		}
		m.content.AppendLine(fmt.Sprintf("status: skill %s %s", action.toggleSkill, state))
		m.input.Reset()
		m.syncSidebar()
		m.syncViewport()
		return m, nil
	}
	if action.submit != "" {
		if m.onSubmit != nil {
			m.onSubmit(action.submit)
		}
		m.content.AppendLine("you> " + action.submit)
		m.input.Reset()
		m.syncViewport()
	}
	return m, nil
}

func (m *Model) handleMouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.scrollUp(m.viewport.MouseWheelDelta)
	case tea.MouseButtonWheelDown:
		m.scrollDown(m.viewport.MouseWheelDelta)
	}
}

func (m *Model) scrollUp(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.autoScroll = false
	m.viewport.ScrollUp(lines)
}

func (m *Model) scrollDown(lines int) {
	if lines < 1 {
		lines = 1
	}
	m.viewport.ScrollDown(lines)
	if m.viewport.AtBottom() {
		m.autoScroll = true
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

func isApprovalAccepted(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func approvalDecisionText(allowed bool, toolName string) string {
	state := "denied"
	if allowed {
		state = "approved"
	}
	if toolName == "" {
		return state
	}
	return fmt.Sprintf("%s %s", toolName, state)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
