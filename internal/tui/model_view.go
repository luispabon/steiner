package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/luispabon/steiner/internal/tui/theme"
)

// View renders the full TUI frame for the current model state.
func (m *Model) View() tea.View {
	if m.frame == nil {
		return m.view()
	}
	start := time.Now()
	v := m.view()
	m.frame.recordView(time.Since(start))
	return v
}

func (m *Model) view() tea.View {
	contentWidth := m.contentWidth()
	sidebarVisible := m.sidebar.Visible(m.width)

	base := m.renderBaseView(contentWidth, sidebarVisible)
	result := m.renderOverlayView(base, contentWidth)

	// Keep the pre-highlight frame during a drag; screenLines are stripped from it on release.
	if m.selection.active {
		m.screenFrame, m.screenFramePending = result, true
	}
	if m.selection.hasSelection() {
		if m.highlightCacheStyles != m.styles {
			m.highlightCache.reset()
			m.highlightCacheStyles = m.styles
		}
		regionLeft, regionRight := m.selectionHighlightBounds()
		result = m.highlightCache.apply(result, m.screenSelection(), m.styles.SelectionStyle, regionLeft, regionRight)
	}

	v := tea.View{
		Content:         result,
		AltScreen:       true,
		MouseMode:       m.rosterHoverMouseMode(),
		BackgroundColor: lipgloss.Color(m.resolvedPalette().ContentBG),
	}

	return v
}

func (m *Model) renderBaseView(contentWidth int, sidebarVisible bool) string {
	mainColumn := m.renderMainColumn(contentWidth)
	if !sidebarVisible {
		return mainColumn
	}

	var vDivider string
	if m.vDividerCacheStyles == m.styles && m.vDividerCacheHeight == m.height && m.vDividerCacheRendered != "" {
		vDivider = m.vDividerCacheRendered
	} else {
		vDivider = m.styles.VDivider.Height(m.height).Render("")
		m.vDividerCacheStyles = m.styles
		m.vDividerCacheHeight = m.height
		m.vDividerCacheRendered = vDivider
	}
	if m.sidebarPosition == "right" {
		return zipColumns(mainColumn, vDivider, m.renderSidebar(m.width, m.height))
	}
	return zipColumns(m.renderSidebar(m.width, m.height), vDivider, mainColumn)
}

func (m *Model) renderMainColumn(contentWidth int) string {
	viewportView := m.renderViewportView(contentWidth)
	var hDivider string
	if m.hDividerCacheWidth == contentWidth && m.hDividerCacheRendered != "" {
		hDivider = m.hDividerCacheRendered
	} else {
		hDivider = lipgloss.NewStyle().
			Background(lipgloss.Color(m.resolvedPalette().ContentBG)).
			Foreground(lipgloss.Color(theme.BorderSoft)).
			Render(strings.Repeat("─", contentWidth))
		m.hDividerCacheWidth = contentWidth
		m.hDividerCacheRendered = hDivider
	}

	mainComponents := []string{viewportView, hDivider}
	mainComponents = append(mainComponents, m.renderActivityRow(contentWidth))
	if queuedBox := m.renderQueuedSteerBox(contentWidth); queuedBox != "" {
		mainComponents = append(mainComponents, queuedBox)
	}
	mainComponents = append(mainComponents,
		m.renderInputView(contentWidth),
		m.renderStatus(contentWidth),
	)

	mainColumn := strings.Join(mainComponents, "\n")
	return theme.TruncateAndPadVertical(mainColumn, contentWidth, m.height, m.resolvedPalette().ContentBG)
}

func (m *Model) renderViewportView(contentWidth int) string {
	scrollY := m.viewport.YOffset()
	scrollbar := m.renderScrollbar()
	hasScrollbar := scrollbar != ""

	if m.vpViewCache != "" &&
		m.vpViewCacheScrollY == scrollY &&
		m.vpViewCacheWidth == contentWidth &&
		m.vpViewCacheHasScrollbar == hasScrollbar {
		return m.withHelpOverlay(m.vpViewCache, contentWidth)
	}

	viewportInner := m.visibleViewportContent()
	viewportContent := viewportInner
	if hasScrollbar {
		viewportContent = m.renderViewportWithScrollbar(viewportInner, scrollbar)
	}

	viewportView := theme.ApplyPanePadding(viewportContent, contentWidth, hasScrollbar, m.resolvedPalette().ContentBG)
	m.vpViewCache = viewportView
	m.vpViewCacheScrollY = scrollY
	m.vpViewCacheWidth = contentWidth
	m.vpViewCacheHasScrollbar = hasScrollbar
	return m.withHelpOverlay(viewportView, contentWidth)
}

// withHelpOverlay draws the help panel over the viewport when help is visible.
// The cached viewport view stays the plain one, so closing help is a cache hit.
func (m *Model) withHelpOverlay(viewportView string, contentWidth int) string {
	if !m.helpVisible {
		return viewportView
	}
	help := m.renderHelpMemo(max(20, contentWidth-4))
	return m.helpCompose.compose(viewportView, help, contentWidth, lipgloss.Height(viewportView), false)
}

// visibleViewportContent slices the visible window out of the scroll model's
// single line slice without re-deriving the full content. setViewportLines
// is the only writer of that slice, and the scrollbar's line count derives
// from it, so the window and the scroll position cannot disagree.
func (m *Model) visibleViewportContent() string {
	start := m.viewport.YOffset()
	height := m.viewport.Height()
	if start < 0 {
		start = 0
	}
	lines := m.viewport.Lines()
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}
	if start >= end {
		return ""
	}
	if m.reflow.pending {
		return strings.Join(m.adaptedWindow(start, end), "\n")
	}
	return strings.Join(lines[start:end], "\n")
}

func (m *Model) renderViewportWithScrollbar(viewportInner, scrollbar string) string {
	var b strings.Builder
	b.Grow(len(viewportInner) + len(scrollbar) + 64)

	// Emit a background-filled leading line so transparent terminals don't show
	// a gap. The viewport content is m.viewport.Width() wide; the scrollbar
	// occupies one additional column.
	leadBg := lipgloss.NewStyle().Background(lipgloss.Color(m.resolvedPalette().ContentBG)).
		Render(strings.Repeat(" ", m.viewport.Width()))
	leadSc := m.styles.ScrollbarTrack.Render(" ")
	b.WriteString(leadBg + leadSc + "\n")

	// Walk both strings line by line simultaneously, without intermediate slice
	// allocations. strings.Split always yields at least one element, so the
	// "done" condition is tracked via a boolean rather than byte-index exhaustion.
	vpIdx, scIdx := 0, 0
	vpLen, scLen := len(viewportInner), len(scrollbar)
	vpDone, scDone := false, false
	first := true
	for !vpDone && !scDone {
		// Consume one viewport line (strings.Split semantics: last segment after
		// final '\n' is an empty string, not absent).
		var vpLine string
		vpLineEnd := strings.IndexByte(viewportInner[vpIdx:], '\n')
		if vpLineEnd < 0 {
			vpLine = viewportInner[vpIdx:]
			vpIdx = vpLen
			vpDone = true
		} else {
			vpLine = viewportInner[vpIdx : vpIdx+vpLineEnd]
			vpIdx += vpLineEnd + 1
		}

		// Consume one scrollbar line.
		var scLine string
		scLineEnd := strings.IndexByte(scrollbar[scIdx:], '\n')
		if scLineEnd < 0 {
			scLine = scrollbar[scIdx:]
			scIdx = scLen
			scDone = true
		} else {
			scLine = scrollbar[scIdx : scIdx+scLineEnd]
			scIdx += scLineEnd + 1
		}

		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(vpLine)
		b.WriteString(scLine)
	}

	return b.String()
}

func (m *Model) renderOverlayView(base string, contentWidth int) string {
	if view := m.exclusiveOverlayView(); view != nil {
		return m.overlayCache.compose(base, view(), m.width, m.height, true)
	}
	base = m.renderBottomAnchoredOverlays(base, contentWidth)
	if view := m.modalOverlayView(); view != nil {
		// Bottom-anchored overlays can leave rows wider than the screen.
		return m.overlayCache.compose(base, view(), m.width, m.height, !m.hasOpenBottomOverlay())
	}
	return base
}

// exclusiveOverlayView returns the renderer of the open centered overlay that
// replaces the bottom-anchored ones (file list, MCP, LSP, error log), or nil.
func (m *Model) exclusiveOverlayView() func() string {
	switch {
	case m.fileList.IsOpen():
		return func() string { return m.fileList.memoView(&m.overlayMemos.fileList) }
	case m.mcpOverlay.IsOpen():
		return func() string { return m.mcpOverlay.memoView(&m.overlayMemos.mcp) }
	case m.lspOverlay.IsOpen():
		return func() string { return m.lspOverlay.memoView(&m.overlayMemos.lsp) }
	case m.devLogOverlay.IsOpen():
		return func() string { return m.devLogMemoView() }
	}
	return nil
}

// modalOverlayView returns the renderer of the open centered modal drawn over
// the bottom-anchored overlays, or nil.
func (m *Model) modalOverlayView() func() string {
	switch {
	case m.workflowHandoff.IsOpen():
		return m.renderWorkflowHandoffModal
	case m.delegateCancelModal.IsOpen():
		return m.renderDelegateCancelModal
	case m.contextOverlay.IsOpen():
		return m.renderContextOverlay
	case m.usageOverlay.IsOpen():
		return m.usageOverlay.View
	}
	if s := m.openConfirmModalView(); s != nil {
		return func() string { return s.render(m.styles) }
	}
	return nil
}

// anyOverlayOpen reports whether any overlay, picker or modal is drawn over
// the base view.
func (m *Model) anyOverlayOpen() bool {
	return m.exclusiveOverlayView() != nil || m.modalOverlayView() != nil || m.hasOpenBottomOverlay()
}

func (m *Model) hasOpenBottomOverlay() bool {
	return m.slashOverlay.IsOpen() || m.filePicker.IsOpen() ||
		m.sessionPicker.IsOpen() || m.subAgentPicker.IsOpen() || m.oneshotResumePicker.IsOpen() ||
		(m.modelPicker.IsOpen() && !m.modelPicker.IsWorkflowHandoff()) ||
		m.reasoningPicker.IsOpen() ||
		m.planPicker.IsOpen() || m.accentPicker.IsOpen() || m.profilePicker.IsOpen() ||
		m.orchestrationPicker.IsOpen()
}

// bottomAnchoredOverlay is a picker or overlay placed above the prompt box.
type bottomAnchoredOverlay interface {
	IsOpen() bool
	View() string
	PlaceBottomAnchoredAt(base, overlay string, inputHeight, xOffset int) string
	shellHeight() int
}

// bottomOverlays returns the bottom-anchored overlays in render order (later
// overlays draw over earlier ones). The model picker is skipped while it is
// serving a workflow handoff, which renders elsewhere.
func (m *Model) bottomOverlays() []bottomAnchoredOverlay {
	overlays := []bottomAnchoredOverlay{
		&m.slashOverlay, &m.filePicker, &m.sessionPicker, &m.subAgentPicker, &m.oneshotResumePicker,
	}
	if !m.modelPicker.IsWorkflowHandoff() {
		overlays = append(overlays, &m.modelPicker)
	}
	return append(overlays, &m.reasoningPicker, &m.planPicker, &m.accentPicker, &m.profilePicker, &m.orchestrationPicker)
}

func (m *Model) renderBottomAnchoredOverlays(base string, contentWidth int) string {
	if !m.hasOpenBottomOverlay() {
		return base
	}

	offset := m.overlayAnchorOffset(contentWidth)

	// When the sidebar occupies the left side, push the slash overlay right so
	// it appears above the prompt box rather than over the sidebar.
	xOffset := 0
	if m.width-contentWidth > 1 && m.sidebarPosition != "right" {
		xOffset = m.width - contentWidth
	}

	for i, o := range m.bottomOverlays() {
		if o.IsOpen() {
			base = m.overlayCache.placeBottom(i, o, base, m.bottomOverlayView(o), offset, xOffset)
		}
	}
	return base
}

// bottomOverlayView renders o, skipping the render for the overlays that
// memoise it while their state is unchanged.
func (m *Model) bottomOverlayView(o bottomAnchoredOverlay) string {
	switch v := o.(type) {
	case *slashOverlay:
		return v.memoView(&m.overlayMemos.slash)
	case *filePickerOverlay:
		return v.memoView(&m.overlayMemos.filePick)
	case *modelPickerOverlay:
		return v.memoView(&m.overlayMemos.modelPick)
	}
	return o.View()
}
