# Overlay Consolidation Plan

Date: 2026-05-21

## Problem

1. **filePicker and slashOverlay dimensions stuck at 0** — `handleWindowSizeMsg` (`model_update.go:174`) updates palette, fileList, sessionPicker, contextOverlay, scratchpadOverlay, exitModal but skips filePicker and slashOverlay. At init (`model_init.go:142-153`), `m.width`/`m.height` are 0 (pre-WindowSizeMsg), so these overlays keep 0 dimensions forever. Causes wrong width (clamped to 40) and fragile height fallback in `PlaceBottomAnchoredAt`.

2. **Bottom-anchored offset under-counts by 1-2 rows** — `renderBottomAnchoredOverlays` (`model_view.go:121`) computes `offset = inputChromeHeight + activityRowHeight`. The main column bottom chrome is: hDivider(1) + activityRow(1) + inputView(N) + statusBar(1). The `-1` in `PlaceBottomAnchoredAt` covers one extra row, but statusBar + hDivider = 2. Off by 1.

3. **paletteModel and fileListOverlay don't embed OverlayShell** — They duplicate open/close state, dimensions, width clamping, frame rendering, divider/footer/chip helpers that OverlayShell already provides. Every other overlay (slashOverlay, sessionPicker, contextOverlay, scratchpadOverlay, exitModal) embeds it.

4. **Six different width strategies** — No shared way to specify preferred/max width. Each overlay hardcodes its own.

5. **exitModal, contextOverlay, scratchpadOverlay embed OverlayShell but still build frames manually** — They don't use `Render()` because it lacks `preferredWidth` support. They duplicate the `PaletteOverlay.Width(...).Padding(...).Render(body)` + `theme.WithBg(...)` pattern.

## Architecture

### OverlayShell width enhancement

Add `preferredWidth` to OverlayShell. The width computation becomes:

```
overlayWidth = preferredWidth (if set, else width-4)
clamped to: min 40, max width-4
```

This covers every overlay's width strategy through a single code path.

New method: `WithPreferredWidth(w int) OverlayShell`.

Update `Render()` to accept a `lipgloss.Style` (the box style) instead of `overlayStyles` struct — the struct only wraps one field and adds indirection.

### Overlay categories

After consolidation, all overlays embed OverlayShell and fall into two placement groups:

**Centered** (rendered via `composeCenteredOverlay`):
- palette (preferred 60)
- fileList (preferred 70)
- contextOverlay (preferred 120)
- scratchpadOverlay (preferred 80)
- exitModal (preferred 60)

**Bottom-anchored** (rendered via `PlaceBottomAnchoredAt`):
- filePicker (dynamic, capped 90 inner)
- slashOverlay (dynamic, capped 90 inner)
- sessionPicker (dynamic)

### Offset fix

Replace the fragile `offset + magic -1` with an explicit `bottomChromeHeight()` method that sums all components below the viewport:

```go
func (m Model) bottomChromeHeight(contentWidth int) int {
    h := 1 // hDivider
    h += m.activityRowHeight(contentWidth)
    h += m.inputChromeHeight(contentWidth)
    h += 1 // statusBar
    if m.approval.active {
        h += lipgloss.Height(m.renderApprovalTray(contentWidth))
    }
    return h
}
```

And remove the `-1` from `PlaceBottomAnchoredAt`'s startY formula — the caller provides the complete offset.

---

## Stages

### Stage 1: Bug fixes

**Goal**: Fix positioning bugs. Minimal, ship-safe.

**Files**:
- `internal/tui/model_update.go` — add filePicker + slashOverlay to `handleWindowSizeMsg`
- `internal/tui/model_view.go` — add `bottomChromeHeight()`, update `renderBottomAnchoredOverlays` to use it
- `internal/tui/overlay.go` — remove `-1` from `PlaceBottomAnchoredAt` startY formula
- `internal/tui/overlay_test.go` — add test for correct startY with explicit offset

**Changes**:

1. `model_update.go:174` `handleWindowSizeMsg` — after the fileList lines, add:
   ```go
   m.filePicker.OverlayShell = m.filePicker.WithDimensions(msg.Width, msg.Height)
   m.slashOverlay.OverlayShell = m.slashOverlay.WithDimensions(msg.Width, msg.Height)
   ```

2. `model_view.go` — add `bottomChromeHeight`:
   ```go
   func (m Model) bottomChromeHeight(contentWidth int) int {
       h := 1 // hDivider
       h += m.activityRowHeight(contentWidth)
       h += m.inputChromeHeight(contentWidth)
       h += 1 // status bar
       if tray := m.renderApprovalTray(contentWidth); tray != "" {
           h += lipgloss.Height(tray)
       }
       return h
   }
   ```

3. `model_view.go:122` `renderBottomAnchoredOverlays` — replace:
   ```go
   offset := m.inputChromeHeight(contentWidth) + m.activityRowHeight(contentWidth)
   ```
   with:
   ```go
   offset := m.bottomChromeHeight(contentWidth)
   ```

4. `overlay.go:126` — change `startY := height - len(olLines) - inputHeight - 1` to:
   ```go
   startY := height - len(olLines) - inputHeight
   ```
   (the caller now provides the full chrome height; no magic `-1`)

5. Tests:
   - `overlay_test.go` — add `TestPlaceBottomAnchoredAtPosition` verifying overlay bottom edge sits at `height - offset`
   - Verify existing `TestComposeCenteredOverlayKeepsBaseContentOutsideOverlay` still passes

**Verification**: `go test ./internal/tui/... && go build ./...`

---

### Stage 2: OverlayShell width enhancement

**Goal**: Add `preferredWidth` so all overlays can express their width preference through the shell.

**Files**:
- `internal/tui/overlay.go` — add field + method, update `overlayWidth()` and `Render()`
- `internal/tui/overlay_test.go` — test preferred width clamping

**Changes**:

1. Add field to OverlayShell:
   ```go
   type OverlayShell struct {
       open           bool
       width          int
       height         int
       title          string
       preferredWidth int // 0 = dynamic (width-4). >0 = preferred, clamped to [40, width-4].
   }
   ```

2. Add builder method:
   ```go
   func (o OverlayShell) WithPreferredWidth(w int) OverlayShell {
       o.preferredWidth = w
       return o
   }
   ```

3. Update `overlayWidth()`:
   ```go
   func (o OverlayShell) overlayWidth() int {
       maxW := o.width - 4
       if maxW < 40 {
           maxW = 40
       }
       if o.preferredWidth > 0 {
           w := o.preferredWidth
           if w > maxW {
               w = maxW
           }
           if w < 40 {
               w = 40
           }
           return w
       }
       return maxW
   }
   ```

4. Simplify `Render()` signature — replace `overlayStyles` with `lipgloss.Style`:
   ```go
   func (o OverlayShell) Render(box lipgloss.Style, body string) string {
       return box.Width(o.InnerWidth()+2).Padding(1, 1).Render(body)
   }
   ```
   Update all callers of `Render(overlayStyles{box: ...}, body)` to `Render(style, body)`.

5. Add `RenderWithBg(box lipgloss.Style, body string, bg lipgloss.Color) string` combining `Render` + `theme.WithBg`:
   ```go
   func (o OverlayShell) RenderWithBg(box lipgloss.Style, body string, bg lipgloss.Color) string {
       return theme.WithBg(o.Render(box, body), bg)
   }
   ```

6. Tests: `TestOverlayShellPreferredWidth` — table-driven cases for dynamic, preferred, clamped-small, clamped-large.

**Verification**: `go test ./internal/tui/... && go build ./...`

**Callers to update** (existing users of `Render`):
- `session_picker.go:106` — `s.Render(overlayStyles{box: s.styles.PaletteOverlay}, body)` → `s.Render(s.styles.PaletteOverlay, body)`

---

### Stage 3: Migrate paletteModel → OverlayShell

**Goal**: Replace own state/frame with embedded OverlayShell.

**Files**:
- `internal/tui/palette.go` — embed OverlayShell, remove `open`/`width`/`height`, use shell helpers
- `internal/tui/model.go` — no struct change (field name stays `palette`)
- `internal/tui/model_update.go` — update dimension propagation (replace `m.palette.width = msg.Width` with `m.palette.OverlayShell = m.palette.WithDimensions(...)`)
- `internal/tui/model_init.go` — update init to use `WithPreferredWidth(60)`
- `internal/tui/model_view.go` — use `m.palette.IsOpen()` instead of `m.palette.open`
- `internal/tui/model_update_keys.go` — verify `m.palette.open` refs become `m.palette.IsOpen()`

**Changes**:

1. `palette.go` struct:
   ```go
   type paletteModel struct {
       OverlayShell
       query    string
       items    []paletteItem
       filtered []paletteItem
       cursor   int
       styles   theme.Styles
   }
   ```
   Remove fields: `open`, `width`, `height`.

2. `newPalette` — add `WithPreferredWidth(60)`:
   ```go
   func newPalette(styles theme.Styles, items []paletteItem) paletteModel {
       p := paletteModel{styles: styles, items: items}
       p.OverlayShell = p.WithPreferredWidth(60)
       p.filtered = append([]paletteItem(nil), items...)
       return p
   }
   ```

3. `Open()`/`Close()` — delegate to shell:
   ```go
   func (p paletteModel) Open() paletteModel {
       p.OverlayShell = p.openShell()
       p.query = ""
       p.cursor = 0
       p.filtered = append([]paletteItem(nil), p.items...)
       return p
   }
   func (p paletteModel) Close() paletteModel {
       p.OverlayShell = p.closeShell()
       return p
   }
   ```

4. `Update()` — replace `p.open` check with `p.IsOpen()`.

5. `View()` — replace manual width calculation and frame rendering:
   ```go
   func (p paletteModel) View() string {
       if !p.IsOpen() { return "" }
       innerWidth := p.InnerWidth()
       // ... build lines using innerWidth (same content logic) ...
       // ... use p.Divider() instead of manual divider ...
       // ... use FooterChip() instead of inline chip function ...
       // ... use p.RenderFooter() for footer line ...
       body := lipgloss.JoinVertical(lipgloss.Left, lines...)
       return p.RenderWithBg(p.styles.PaletteOverlay, body, lipgloss.Color(theme.BgElev))
   }
   ```

6. `model_update.go:177-178` — replace:
   ```go
   m.palette.width = msg.Width
   m.palette.height = msg.Height
   ```
   with:
   ```go
   m.palette.OverlayShell = m.palette.WithDimensions(msg.Width, msg.Height)
   ```

7. `model_view.go:102` — `m.palette.open` → `m.palette.IsOpen()`

8. `model_update_keys.go` — grep all `m.palette.open` refs, change to `m.palette.IsOpen()`.

**Verification**: `go test ./internal/tui/... && go build ./...` + manual test: Ctrl+P opens, filters, selects, closes.

---

### Stage 4: Migrate fileListOverlay → OverlayShell

**Goal**: Same pattern as palette migration.

**Files**:
- `internal/tui/file_list.go` — embed OverlayShell, remove `open`/`width`/`height`/`err`, use shell helpers
- `internal/tui/model_update.go` — update dimension propagation
- `internal/tui/model_init.go` — update init to use `WithPreferredWidth(70)`
- `internal/tui/model_view.go` — `m.fileList.open` → `m.fileList.IsOpen()`
- `internal/tui/file_list_test.go` — update tests for new struct shape

**Changes**:

1. `file_list.go` struct:
   ```go
   type fileListOverlay struct {
       OverlayShell
       root    string
       entries []string
       err     string
       styles  theme.Styles
   }
   ```
   Remove fields: `open`, `width`, `height`.

2. `newFileListOverlay` — add `WithPreferredWidth(70)`.

3. `Open()`/`Close()` — delegate to shell (`openShell`/`closeShell`).

4. `View()` — replace manual width and frame with shell helpers:
   ```go
   func (f fileListOverlay) View() string {
       if !f.IsOpen() { return "" }
       innerWidth := f.InnerWidth()
       // ... same content logic ...
       // ... use f.Divider(), f.RenderFooter(), FooterChip() ...
       body := lipgloss.JoinVertical(lipgloss.Left, ...)
       return f.RenderWithBg(f.styles.PaletteOverlay, body, lipgloss.Color(theme.BgElev))
   }
   ```

5. `model_update.go:179-180` — replace `m.fileList.width/height = msg.Width/Height` with `m.fileList.OverlayShell = m.fileList.WithDimensions(...)`.

6. `model_view.go:104` — `m.fileList.open` → `m.fileList.IsOpen()`

**Verification**: `go test ./internal/tui/... && go build ./...` + manual test: `/ls` opens, shows files, closes.

---

### Stage 5: Consolidate remaining overlays + routing

**Goal**: exitModal, contextOverlay, scratchpadOverlay use `Render()`/`RenderWithBg()` via preferredWidth. Clean up renderOverlayView routing.

**Files**:
- `internal/tui/exit_modal.go` — use `WithPreferredWidth(60)`, replace manual frame
- `internal/tui/context_overlay.go` — use `WithPreferredWidth(120)`, replace `fixedWidth` field and manual frame
- `internal/tui/scratchpad_overlay.go` — use `WithPreferredWidth(80)`, replace `scratchpadOverlayWidth` const and manual frame
- `internal/tui/model_view.go` — unify `renderOverlayView` to use `IsOpen()` everywhere

**Changes for exitModal** (`exit_modal.go`):

1. `openExitModal` — add `WithPreferredWidth(60)`.
2. `renderExitModal` — remove manual overlayWidth clamping (lines 48-55), use `s.InnerWidth()` and `s.RenderWithBg(...)`.

**Changes for contextOverlay** (`context_overlay.go`):

1. Remove `fixedWidth` field from struct.
2. `openContextOverlay` — add `WithPreferredWidth(120)`.
3. `contextInnerWidth()` — simplify to just `s.InnerWidth()` (preferred width handles the fixed-width behavior).
4. `renderContextOverlay` — replace manual `boxStyle.Width(contextOverlayWidth).Render(full)` + `theme.WithBg(...)` with `s.RenderWithBg(...)`.

**Changes for scratchpadOverlay** (`scratchpad_overlay.go`):

1. Remove `scratchpadOverlayWidth` const and `scratchpadInnerWidth()` method.
2. `openScratchpadOverlay` — add `WithPreferredWidth(80)`.
3. `renderScratchpadOverlay` — use `s.InnerWidth()` and `s.RenderWithBg(...)`.

**Changes for renderOverlayView** (`model_view.go:100`):

Replace direct `.open` field access with `.IsOpen()`:
```go
func (m Model) renderOverlayView(base string, contentWidth int) string {
    switch {
    case m.palette.IsOpen():
        return composeCenteredOverlay(base, m.palette.View(), m.width, m.height)
    case m.fileList.IsOpen():
        return composeCenteredOverlay(base, m.fileList.View(), m.width, m.height)
    }

    base = m.renderBottomAnchoredOverlays(base, contentWidth)
    switch {
    case m.contextOverlay.IsOpen():
        return composeCenteredOverlay(base, m.renderContextOverlay(), m.width, m.height)
    case m.scratchpadOverlay.IsOpen():
        return composeCenteredOverlay(base, m.scratchpadOverlay.renderScratchpadOverlay(), m.width, m.height)
    case m.exitModal.IsOpen():
        return composeCenteredOverlay(base, m.renderExitModal(), m.width, m.height)
    default:
        return base
    }
}
```

**Verification**: `go test ./internal/tui/... && go build ./... && make check`

---

## Post-consolidation state

After all stages, every overlay:
- Embeds `OverlayShell`
- Uses `IsOpen()`, `openShell()`, `closeShell()`
- Uses `WithDimensions()` from `handleWindowSizeMsg`
- Uses `WithPreferredWidth()` for its width preference
- Uses `InnerWidth()`, `Divider()`, `FooterChip()`, `RenderFooter()`
- Uses `Render()` or `RenderWithBg()` for frame rendering
- Uses `PlaceBottomAnchoredAt` (bottom-anchored) or `composeCenteredOverlay` (centered) for placement

Remaining per-overlay responsibility: content rendering (what's inside the frame) and input handling (`Update()`). This is intentional — the OverlayShell is chrome, not a content abstraction.

### Dead code to remove

After stage 5:
- `overlayStyles` struct (overlay.go:219) — no longer used if `Render` takes `lipgloss.Style` directly
- `contextOverlayWidth` const (context_overlay.go:14)
- `scratchpadOverlayWidth` const (scratchpad_overlay.go:109)
- `scratchpadInnerWidth()` method (scratchpad_overlay.go:112)
- `contextInnerWidth()` method (context_overlay.go:34)
- `fixedWidth` field from contextOverlayState

### What stays unchanged

- Content rendering per overlay (items, filtering, scrolling)
- `Update()` per overlay (key handling, action dispatch)
- Two placement strategies (centered + bottom-anchored)
- No `Content` interface or overlay stack — the embed pattern is simpler and sufficient
- `renderOverlayView` routing stays as explicit switch (not a generic loop)

## Risk notes

- Stage 1 is safe to ship independently — pure bug fix, no API changes.
- Stages 2-4 change struct layouts — any code that directly accesses `palette.open`, `palette.width`, `fileList.open`, `fileList.width` etc. needs updating. Grep for all direct field access before each migration.
- Stage 5 touches contextOverlay and scratchpadOverlay rendering — test scrolling behavior in these overlays after the change since `InnerWidth()` replaces hardcoded widths.
- exitModal has `Padding(0, 1)` not `Padding(1, 1)` — it uses tighter vertical padding. `Render()` hardcodes `Padding(1, 1)`. Either make padding configurable in `Render()` or let exitModal keep its own render call. Recommend: keep exitModal's own render for now, just use `InnerWidth()` for the width calculation.

## Effort estimate

| Stage | Scope | Est |
|-------|-------|-----|
| 1 | Bug fixes | 1h |
| 2 | preferredWidth | 1h |
| 3 | Palette migration | 2h |
| 4 | FileList migration | 1h |
| 5 | Remaining + cleanup | 2h |
| **Total** | | **~7h** |

Stages can be shipped as separate commits/PRs. Stage 1 is independent. Stages 2-5 are sequential.
