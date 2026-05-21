package tui

import "strings"

type viewCacheState struct {
	scrollbar scrollbarRenderCache
	chrome    chromeRenderCache
}

type scrollbarRenderCache struct {
	height     int
	totalLines int
	yOffset    int
	rendered   string
	valid      bool
	buildCount int
}

type chromeRenderCache struct {
	hDivider      string
	hDividerWidth int

	vDivider       string
	vDividerHeight int

	activityKey      activityRenderKey
	activityRendered string
	activityValid    bool

	inputKey      inputRenderKey
	inputRendered string
	inputValid    bool

	statusKey      statusRenderKey
	statusRendered string
	statusValid    bool

	approvalKey      approvalTrayRenderKey
	approvalRendered string
	approvalValid    bool
}

type activityRenderKey struct {
	width    int
	label    string
	detail   string
	spinning bool
	accent   string
	frame    string
}

type inputRenderKey struct {
	width       int
	value       string
	placeholder string
	cursorLine  int
	cursorCol   int
	accent      string
}

type statusRenderKey struct {
	width          int
	model          string
	context        string
	mode           string
	approvalActive bool
	promptUsed     int
	contextBudget  int
	accent         string
}

type approvalTrayRenderKey struct {
	width          int
	active         bool
	tool           string
	mode           string
	preview        string
	selectedAction int
	accent         string
}

type sidebarRenderCache struct {
	key               sidebarRenderKey
	modifiedFiles     []gitModifiedFile
	sortedFilesSource []gitModifiedFile
	sortedFiles       []gitModifiedFile
	rendered          string
	valid             bool
	buildCount        int
	sortedBuildCount  int
}

type sidebarRenderKey struct {
	width               int
	height              int
	expanded            bool
	model               string
	version             string
	quant               string
	provider            string
	promptUsed          int
	budgetUsed          int
	contextBudget       int
	currentTurn         int
	maxTurns            int
	compaction          string
	compactionBlinkEven bool
	branch              string
	dirty               bool
	ahead               int
	workingDir          string
	homeDir             string
	scratchpadIntent    string
	scratchpadDecisions string
	scratchpadOpen      string
	scratchpadNext      string
	accent              string
}

func newViewCacheState() *viewCacheState {
	return &viewCacheState{}
}

func newSidebarRenderCache() *sidebarRenderCache {
	return &sidebarRenderCache{}
}

func (m Model) activityRenderKey(width int) activityRenderKey {
	return activityRenderKey{
		width:    width,
		label:    m.activity.label,
		detail:   m.activity.detail,
		spinning: m.activity.spinning,
		accent:   string(m.styles.AccentColor),
		frame:    m.activity.spinner.View(),
	}
}

func (m Model) inputRenderKey(width int) inputRenderKey {
	lineInfo := m.input.LineInfo()
	return inputRenderKey{
		width:       width,
		value:       m.input.Value(),
		placeholder: m.input.Placeholder,
		cursorLine:  m.input.Line(),
		cursorCol:   lineInfo.ColumnOffset,
		accent:      string(m.styles.AccentColor),
	}
}

func (m Model) statusRenderKey(width int) statusRenderKey {
	return statusRenderKey{
		width:          width,
		model:          m.status.model,
		context:        m.status.context,
		mode:           m.status.mode,
		approvalActive: m.status.approvalActive,
		promptUsed:     m.status.promptUsed,
		contextBudget:  m.status.contextBudget,
		accent:         string(m.styles.AccentColor),
	}
}

func (m Model) approvalTrayRenderKey(width int) approvalTrayRenderKey {
	return approvalTrayRenderKey{
		width:          width,
		active:         m.approval.active,
		tool:           m.approval.tool,
		mode:           m.approval.mode,
		preview:        m.approval.preview,
		selectedAction: m.approval.selectedAction,
		accent:         string(m.styles.AccentColor),
	}
}

func (s sidebarState) renderKey(width, height int) sidebarRenderKey {
	return sidebarRenderKey{
		width:               width,
		height:              height,
		expanded:            s.expanded,
		model:               s.model,
		version:             s.version,
		quant:               s.quant,
		provider:            s.provider,
		promptUsed:          s.promptUsed,
		budgetUsed:          s.budgetUsed,
		contextBudget:       s.contextBudget,
		currentTurn:         s.currentTurn,
		maxTurns:            s.maxTurns,
		compaction:          s.compaction,
		compactionBlinkEven: strings.TrimSpace(s.compaction) != "" && s.tickCount%2 == 0,
		branch:              s.branch,
		dirty:               s.dirty,
		ahead:               s.ahead,
		workingDir:          s.workingDir,
		homeDir:             s.homeDir,
		scratchpadIntent:    s.scratchpadIntent,
		scratchpadDecisions: s.scratchpadDecisions,
		scratchpadOpen:      s.scratchpadOpen,
		scratchpadNext:      s.scratchpadNext,
		accent:              string(s.styles.AccentColor),
	}
}

func sameModifiedFiles(a, b []gitModifiedFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneModifiedFiles(files []gitModifiedFile) []gitModifiedFile {
	if len(files) == 0 {
		return nil
	}
	return append([]gitModifiedFile(nil), files...)
}
