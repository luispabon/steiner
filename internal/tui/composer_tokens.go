package tui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
)

func (m *Model) openModelPickerFromSlashCommand() *Model {
	m.modelPicker = m.modelPicker.OpenEntries(m.modelPickerEntries(), m.primaryModel)
	m.modelPicker.width = m.width
	m.modelPicker.height = m.height
	return m
}

func (m *Model) openAccentPickerFromSlashCommand() *Model {
	m.accentPicker = m.accentPicker.Open(m.accentPreset)
	m.accentPicker.width = m.width
	m.accentPicker.height = m.height
	return m
}

func (m *Model) openProfilePickerFromSlashCommand() *Model {
	m.profilePicker = m.profilePicker.Open(m.profileNames, m.sidebar.profile)
	m.profilePicker.width = m.width
	m.profilePicker.height = m.height
	return m
}

func (m *Model) openSessionPickerFromSlashCommand() *Model {
	if m.sessionBusy() {
		m.refuseWhileBusy("switch sessions")
		return m
	}
	m.openSessionPicker()
	return m
}

func (m *Model) openOneshotResumePickerFromSlashCommand() *Model {
	m.openOneshotResumePicker()
	return m
}

func (m *Model) syncSlashOverlayWithComposer() {
	if !m.slashOverlay.IsOpen() {
		return
	}
	token, _, _, ok := m.activeComposerToken('/')
	if !ok {
		m.slashOverlay = m.slashOverlay.Close()
		return
	}
	m.slashOverlay.syncQuery(token)
}

func (m *Model) syncFilePickerWithComposer() {
	if !m.filePicker.IsOpen() {
		return
	}
	token, _, _, ok := m.activeComposerToken('@')
	if !ok {
		m.filePicker = m.filePicker.Close()
		return
	}
	m.filePicker.syncQuery(strings.TrimPrefix(token, "@"))
}

func (m *Model) replaceComposerToken(prefix rune, replacement string) {
	_, start, end, ok := m.activeComposerToken(prefix)
	if !ok {
		return
	}
	value := []rune(m.input.Value())
	next := string(value[:start]) + replacement + string(value[end:])
	m.input.SetValue(next)
	m.input.CursorEnd()
}

func (m *Model) activeComposerToken(prefix rune) (string, int, int, bool) {
	value := m.input.Value()
	cursor := composerCursorOffset(m.input)
	return composerTokenAtCursor(value, cursor, prefix)
}

func composerCursorOffset(input textarea.Model) int {
	lines := strings.Split(input.Value(), "\n")
	line := input.Line()
	if line < 0 {
		line = 0
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}
	offset := 0
	for i := 0; i < line; i++ {
		offset += len([]rune(lines[i])) + 1
	}
	lineInfo := input.LineInfo()
	col := lineInfo.StartColumn + lineInfo.ColumnOffset
	lineRunes := []rune(lines[line])
	if col < 0 {
		col = 0
	}
	if col > len(lineRunes) {
		col = len(lineRunes)
	}
	return offset + col
}

func composerTokenAtCursor(value string, cursor int, prefix rune) (string, int, int, bool) {
	runes := []rune(value)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	start := cursor
	for start > 0 && !unicode.IsSpace(runes[start-1]) {
		start--
	}
	if start >= len(runes) || runes[start] != prefix {
		return "", 0, 0, false
	}

	end := cursor
	for end < len(runes) && !unicode.IsSpace(runes[end]) {
		end++
	}
	return string(runes[start:end]), start, end, true
}
