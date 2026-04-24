# Overview: steiner TUI Redesign

## Request

Implement the Claude Design handoff (`tmp/design_handoff_tui/`) as a high-fidelity terminal port. Replaces all existing theming with a new OKLCH-based near-black amber "steiner" theme (only theme). Adds: collapsible tool calls + thinking blocks (mouse click); command palette (Ctrl+P); sidebar brand row + redesigned cards with token bar; inline approval pill; compaction banner; streaming cursor + indicator; status bar key chips; input `›` chevron + state-aware placeholder; 7 accent presets + show-thinking toggle persisted to `~/.config/steiner/prefs.yaml`.

---

## Overview

### Color system (Stage 1)

New `internal/tui/theme/steiner.go` implementing the `Theme` interface. All OKLCH design tokens converted to sRGB hex at compile time via a small `oklch.go` utility (OKLCH → linear sRGB → gamma-corrected hex). Remove `catppuccin.go`; "steiner" becomes the sole registered theme and the default.

Extend `theme.Styles` with all new tokens required by components:
- `BgElev`, `BgElev2` — elevated surface colors
- `UserBar`, `UserBg` — user message chrome
- `ThinkingBar` — thinking block left-bar
- `ToolTag*` — per-tool-kind tag pill styles (bash/read/write/edit/glob/grep/todo/default)
- `Added`, `Removed`, `Warn` — diff/status colors
- `FgDim`, `FgFaint`, `FgMute` — secondary/tertiary text tiers
- `AccentSoft`, `AccentLine` — fill and border computed from accent
- `KeyChip` — status bar chip style
- `PaletteOverlay`, `PaletteInput`, `PaletteItem`, `PaletteItemActive` — command palette

Accent is runtime-swappable: 7 presets (amber/rose/magenta/violet/cyan/mint/lime). Changing accent recomputes `AccentSoft` and `AccentLine` and triggers a full style rebuild.

### Transcript (Stages 2–3)

Rework `content.go`. New segment types:
- `segmentUser` — left-bar chrome, `--user-soft` bg, `›` prefix
- `segmentThinkingBlock` — collapsible; stores collapsed state + preview text
- `segmentToolCall` — collapsible; stores tool name, arg summary, meta, body kind
- `segmentDiff` — diff block with header + gutter rows
- `segmentApprovalPill` — inline y/n/a buttons; resolved state
- `segmentCompactionBanner` — progress bar + warn fill
- `segmentStreamingIndicator` — three dots + label, shown mid-stream

`contentBuffer` gains:
- `collapseState map[int]bool` — per-segment collapse (default collapsed for tool calls, thinking blocks)
- `segmentHeights []int` — cumulative rendered line heights for mouse Y→segment mapping
- `streamingPhase` — `thinking | tool | answer | none`

Mouse handler in `model.go` maps click Y to a segment via `segmentHeights`; if the segment is collapsible, toggle its `collapseState` entry and call `syncViewport()`.

**Tool call rendering:**
- Header: `▸`/`▾` chevron + tag pill (tool name, colored by kind) + arg summary + right-aligned meta
- Tag colors: `bash`→accent, `read`→soft blue, `write`/`edit`→added-green, `glob`/`grep`→soft magenta, `todo`→warn, default→tool-cyan
- Body (when open, indented 22px/cols):
  - **bash**: darker bg, `$` + command line, stdout fg, stderr red, footer `exit N` (green/red) + cwd mute
  - **file read**: `path · N lines` caption, code block with 4-char line-number gutter, chroma syntax highlight via existing `alecthomas/chroma/v2` dep
  - **diff**: header row (path + `+N` green `−N` red), hunk rows with 6-char gutter (line num + sign), `+` rows green-soft bg, `−` rows red-soft bg
  - **plain/list**: `bg-elev` panel, dim text

**Thinking block:**
- Closed: `▸ Thinking · <first 80 chars>…` in muted-gold italic
- Open: 2px left-bar in thinking color, italic body, dim

**Approval pill:**
- Border with accent-line, 2px left accent bar, `bg-elev` bg
- Three buttons: `[y] approve`, `[n] deny`, `[a] always` — right-aligned
- Resolved: opacity-dim border dashed (rendered as `·`), `✓ approved` or `✗ denied`

**Compaction banner:**
- Full-width, warn-tinted border, warn-soft fill
- "Compacting" label + dim subtitle + progress bar (4-row tall, dark track, warn fill)
- On finish: replaced by italic system note

**Streaming:**
- Blinking `█` cursor via 500ms tick cmd appended to stream preview
- Mid-stream indicator: three `•` dots (staggered via tick counter mod 3) + label

### Sidebar (Stage 4)

Width: 34 cols (fits 100+ terminal). Collapse threshold: 100 cols.

**Brand row:** Small filled accent square `▪` + bold "steiner" + dim version string, separator below.

**Cards** — label-above-content:
- Label: uppercase, `fg-mute`, letter-spacing approximated via spaced chars `M O D E L`
- Rows: `key:` in `fg-faint`, `val` in `fg`

**Model card:** name + dim quant (if present) + dim host.

**Context card:**
- Token bar: `name/total` label above, bar (10 rows tall via padding), 3-state fill:
  - ≤70%: accent color
  - >70%: warn color
  - >90%: removed/red color
- Tokens used / total + `N%` below bar
- Compact row: gray `●` "auto @ 90%" idle / pulsing amber `●` "compacting…" active

**Repository card:** workdir (ellipsed, dim), branch + `●` dirty marker (amber), ahead count (dim).

**Modified files card:** header with count. Each row: status glyph `M`/`A`/`D`/`U` in warn/added/removed/mute color + path (dim, ellipsed) + `+N −N` stats.

### Status bar (Stage 5)

Each segment: muted label + value, separated by `│` borders. Key chips: `bg-elev-2` bg, 1px border, `fg-faint`.

Segments (L→R):
1. `model` + accent-colored model name
2. `turn` + dim number
3. `ctx` + `used/total · N%` (accent ≤70%, warn >70%, red >90%)
4. `[⏎] send` or `[esc] interrupt`
5. `[⇧⏎] newline`
6. `[^P] commands`
7. `[^B] sidebar`
8. `[/model] switch`
9. `[?] help`

Wraps to second row on narrow terminals.

### Input (Stage 5)

- `›` chevron in accent (replaces `>`)
- Placeholder: `"ask steiner — / for commands, @ for files"` idle; `"streaming… esc to interrupt"` while streaming
- Focus: border shifts to `accent-line`
- `Esc` while streaming: interrupt + "interrupted" system note

### Command palette (Stage 6)

New `internal/tui/palette.go`. `paletteModel` struct with:
- `open bool`, `query string`, `items []paletteItem`, `filtered []paletteItem`, `cursor int`
- `paletteItem{command, name, description string, action paletteAction}`

Rendered as a lipgloss overlay via `lipgloss.Place` over the full TUI. Width: min(92% terminal width, 72 cols). Top: 4 rows from top.

Layout:
- Search row: `⌘` icon + text input + soft bottom border
- Item list (max 12 rows, scrollable): command in accent + name in fg + description in mute (right-aligned)
- Active item: `accent-soft` fill
- Footer: `[↵] run  [↑↓] navigate  [esc] close`

Ctrl+P opens; `↑`/`↓` navigate; `↵` runs action; `Esc` closes.

Commands: `/model`, `/clear`, `/compact`, `/skill`, `/history`, `/diff`, `/exit`, `/help`, `/yank`, `/replay` + any steiner slash commands already wired.

`model.go` handles `tea.KeyCtrlP` → open palette; palette key events handled before other key routing when `palette.open`.

### Tweaks + persistence (Stage 7)

`internal/tui/prefs/prefs.go` — load/save `~/.config/steiner/prefs.yaml`:
```yaml
accent: amber       # amber|rose|magenta|violet|cyan|mint|lime
show_thinking: true
```

Loaded at TUI startup; passed into `Config`. Accent change triggers style rebuild + viewport sync. Show-thinking toggle hides `segmentThinkingBlock` segments entirely in `String()`.

---

## Verification Strategy

### Sources
- `/home/luis/Projects/AI/steiner/CLAUDE.md`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatter
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -l <changed files>`
- use_check_only_when:
  - never (fix is always safe)

#### build
- preferred_mode: check
- check:
  - `go build ./...`
- use_check_only_when:
  - always (build is check-only by nature)

#### vet
- preferred_mode: check
- check:
  - `go vet ./...`
- use_check_only_when:
  - always

#### tests
- preferred_mode: check
- check:
  - `go test ./internal/tui/...`
  - `go test ./...` (end of implementation)
- use_check_only_when:
  - always

#### binaries
- preferred_mode: check
- check:
  - `make build-binaries`
- use_check_only_when:
  - end of implementation only

### Tiers
- cheap:
  - formatter
  - build
  - vet
- medium:
  - tests
- expensive:
  - binaries

### Required Boundaries
- step_level_exceptions:
  - run `go build ./...` after each step to catch compilation errors early
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatter
  - build
  - vet
  - tests
  - binaries
- reviewer_after_fix:
  - re-run `go build ./...` and `go vet ./...` after any fix

### Assumptions
- Terminal is truecolor (24-bit); OKLCH → hex conversion is done at startup
- BubbleTea mouse events are already enabled in `app.go`
- `alecthomas/chroma/v2` already in go.mod (confirmed) — use for syntax highlighting
- No new Go dependencies needed

### Uncertainties
- Whether mouse events are currently enabled in `tea.NewProgram` options — need to verify in `app.go` before Stage 3

---

## Decision Log

- **Catppuccin removed entirely:** User confirmed new "steiner" theme supersedes all existing theming; no multi-theme support needed.
- **Mouse click for collapsible:** User confirmed. Requires Y-coordinate-to-segment mapping in contentBuffer.
- **Command palette in scope:** Full Ctrl+P palette included now, not deferred.
- **Prefs at `~/.config/steiner/`:** User confirmed. New `internal/tui/prefs` package.
- **Dependencies unchanged:** BubbleTea + Lipgloss + Glamour remain; no dep changes needed to implement the design.
- **OKLCH conversion at startup:** Small utility converts design token OKLCH values to hex sRGB once at init. Values are static design tokens, not dynamic, so compile-time constants are acceptable.
- **Sidebar brand version:** Hardcoded to `v0.0.1` for now.
