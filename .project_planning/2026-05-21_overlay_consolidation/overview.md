# Overlay Consolidation

Date: 2026-05-21

## Request

Formalize and execute the TUI overlay consolidation plan from `planning_docs/overlay_consolidation.md`. The work covers:

1. **Bug fixes** — `filePicker`/`slashOverlay` missing from `handleWindowSizeMsg`, bottom-anchored offset off by 1.
2. **OverlayShell width enhancement** — add `preferredWidth` so all overlays express width preference through the shell.
3. **Migrate `paletteModel` to embed OverlayShell** — replace own `open`/`width`/`height` fields.
4. **Migrate `fileListOverlay` to embed OverlayShell** — same pattern.
5. **Consolidate remaining overlays** — `exitModal`, `contextOverlay`, `scratchpadOverlay` use `Render()`/`RenderWithBg()` via `preferredWidth`; unify `.open` → `.IsOpen()` in routing.

## Overview

All TUI overlays share the same chrome pattern: a framed, placed panel. The `OverlayShell` type (in `overlay.go`) provides open/close state, dimensions, width clamping, frame rendering, divider/footer/chip helpers. However, adoption is inconsistent:

- **`sessionPicker`** is the model consumer — embeds `OverlayShell`, uses `IsOpen()`, `InnerWidth()`, `Divider()`, `RenderFooter()`, `Render()`.
- **`exitModal`**, **`contextOverlay`**, **`scratchpadOverlay`** embed `OverlayShell` but build frames manually with hardcoded widths, ignoring `Render()`/`RenderWithBg()`.
- **`paletteModel`** and **`fileListOverlay`** don't embed `OverlayShell` at all — they duplicate `open`/`width`/`height` fields and build frames manually.
- **`filePicker`** and **`slashOverlay`** embed but are missing dimension updates in `handleWindowSizeMsg`.

Two bugs exist: bottom-anchored overlays are positioned 1 row too high (offset misses `hDivider` + `statusBar`), and `filePicker`/`slashOverlay` never receive real terminal dimensions.

The consolidation adds `preferredWidth` to `OverlayShell`, migrates all overlays to embed it, replaces manual frame rendering with `RenderWithBg()`, and fixes the two bugs.

### Key design decisions

- **`preferredWidth` instead of per-overlay width fields.** Each overlay declares its preferred width (e.g. palette=60, fileList=70, exitModal=60). The shell clamps to `[40, width-4]`. Removes 6 different width strategies.
- **`Render()` takes `lipgloss.Style` instead of `overlayStyles` struct.** The struct wraps one field; removing it eliminates indirection.
- **`bottomChromeHeight()`** replaces fragile `inputChromeHeight + activityRowHeight + magic -1` with an explicit sum of all chrome rows below the viewport.
- **`.open` → `.IsOpen()`** everywhere, enforced by embedding `OverlayShell`.
- **exitModal keeps its own render call.** It uses `Padding(0, 1)` while the shell's `Render()` uses `Padding(1, 1)`. Rather than making padding configurable (or changing exitModal's padding), let exitModal keep its own render but use `InnerWidth()` for width. This is noted in the risk section of the source plan and adopted here.

### Scope boundaries

- Content rendering per overlay (items, filtering, scrolling) stays unchanged.
- `Update()` per overlay (key handling, action dispatch) stays unchanged.
- Two placement strategies (centered + bottom-anchored) stay unchanged.
- No `Content` interface or overlay stack — the embed pattern is simpler and sufficient.
- `renderOverlayView` routing stays as explicit switch.

## Verification Strategy

### Fast checks (run after each stage)

| Check | Command | Cost |
|-------|---------|------|
| Format Go code | `gofmt -w $(git ls-files '*.go')` | cheap, fix |
| Format imports | `goimports -w $(git ls-files '*.go')` | cheap, fix |
| Build | `go build ./...` | cheap |
| TUI package tests | `go test ./internal/tui/...` | cheap |
| Vet | `go vet ./...` | cheap |

### Full check (before commit)

| Check | Command | Cost |
|-------|---------|------|
| Full pre-submit gate | `make check` | expensive |

`make check` runs: `tidy-check → fmt-check → imports-check → build-binaries → test → test-race → vet → lint → vuln`.

### Manual checks (stage-dependent)

- Stage 1: Verify Ctrl+P opens palette, `/ls` shows files, file picker renders at correct position.
- Stage 3: Ctrl+P opens, filters, selects, closes.
- Stage 4: `/ls` opens, shows files, closes.

### Notes

- Run `make install-check-tools` first if `lint` or `vuln` haven't been run before.
- The authoritative pre-submit gate is `make check`. Before committing each stage, run `gofmt -w` + `goimports -w` then `make check` (or at minimum `go build ./...` + `go test ./internal/tui/...` + `go vet ./...`).

## Decision Log

| # | Decision | Rationale |
|---|----------|----------|
| D1 | **No research needed.** The work is repo-local, well-understood, and confirmed by codebase audit. | The `planning_docs/overlay_consolidation.md` is substantially accurate; codebase exploration verified all claims and found only one minor factual error (init dimensions are 80×24, not 0), which doesn't change the fix. |
| D2 | **Keep exitModal's own render call, use `InnerWidth()`.** | exitModal uses `Padding(0, 1)` vs shell's `Padding(1, 1)`. Making padding configurable in `Render()` adds complexity for one caller. The plan states this recommendation. |
| D3 | **Five sequential stages, shipped as separate commits.** Stage 1 is independently shippable (pure bug fix). Stages 2–5 are sequential because each changes `OverlayShell` API or struct layout. | Confirmed by plan risk notes. |
| D4 | **`overlayStyles` struct removed.** The indirection of a struct wrapping a single `lipgloss.Style` field is unnecessary. `Render()` takes `lipgloss.Style` directly. | Plan change description confirmed. |
| D5 | **`contextOverlay.fixedWidth` field removed.** Replaced by `WithPreferredWidth(120)`. | `fixedWidth` is a manual reimplementation of `preferredWidth`. |
| D6 | **`contextDivider()`/`contextRenderFooter()` removed.** These duplicate `OverlayShell.Divider()` and `OverlayShell.RenderFooter()`. | Dead code once contextOverlay uses shell helpers. |
| D7 | **Bottom-anchored offset: explicit `bottomChromeHeight()` replaces magic.** Sums hDivider(1) + activityRow + inputChrome + statusBar(1) + approvalTray if active. Removes the `-1` fudge. | Fragile offset formula was the root cause of positioning bug (off by 1). |
