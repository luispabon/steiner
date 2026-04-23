# Stage 7 Theme & Input Polish — Research

Date: 2026-04-22
Versions verified against: steiner go.mod (bubbles v0.21.0, glamour v0.10.0, catppuccin/go main branch)

---

## Q1: catppuccin/go API

### Question
What is the correct Go import path, API to get Mocha palette colors, color type returned, and available color names?

### Findings

**Import path:**
```go
import catppuccin "github.com/catppuccin/go"
```
Module: `github.com/catppuccin/go` (go.mod confirmed). Package name in source is `catppuccingo`.

**Top-level API:**
- `catppuccin.Mocha` — a package-level `var` of type `catppuccin.Flavor` (interface)
- Also available: `catppuccin.Frappe`, `catppuccin.Macchiato`, `catppuccin.Latte`
- `catppuccin.Variant(name string) catppuccin.Theme` — look up by name string

**Color type returned:**
Methods on `Flavor` return `catppuccin.Color` (a plain struct, NOT `lipgloss.Color`):
```go
type Color struct {
    Hex string        // e.g. "#1e1e2e"
    RGB [3]uint8
    HSL [3]float32
}
```
The `Color` struct implements `color.Color` (stdlib `image/color`) via `RGBA()`. It does NOT return `lipgloss.Color` directly — the caller must convert:
```go
lgColor := lipgloss.Color(catppuccin.Mocha.Base().Hex)
```

**Flavor interface — all method names (Mocha implements all):**
```
Rosewater(), Flamingo(), Pink(), Mauve(), Red(), Maroon(), Peach(),
Yellow(), Green(), Teal(), Sky(), Sapphire(), Blue(), Lavender(),
Text(), Subtext1(), Subtext0(),
Overlay2(), Overlay1(), Overlay0(),
Surface2(), Surface1(), Surface0(),
Crust(), Mantle(), Base(),
Name() string
```
26 color methods total. The surface/overlay/base/mantle/crust colors are the key ones for theming backgrounds and UI chrome.

**Mocha hex values for key theming colors:**
| Name      | Hex       | Role                     |
|-----------|-----------|--------------------------|
| Base      | #1e1e2e   | Primary background       |
| Mantle    | #181825   | Slightly darker bg       |
| Crust     | #11111b   | Darkest bg               |
| Surface0  | #313244   | Elevated surface         |
| Surface1  | #45475a   | More elevated surface    |
| Surface2  | #585b70   | Highest surface          |
| Overlay0  | #6c7086   | Muted/overlay            |
| Overlay1  | #7f849c   | Mid overlay              |
| Overlay2  | #9399b2   | Light overlay            |
| Text      | #cdd6f4   | Primary text             |
| Subtext0  | #a6adc8   | Secondary text           |
| Subtext1  | #bac2de   | Tertiary text            |
| Sapphire  | #74c7ec   | Accent blue              |
| Blue      | #89b4fa   | Blue accent              |
| Mauve     | #cba6f7   | Purple accent            |
| Green     | #a6e3a1   | Success                  |
| Red       | #f38ba8   | Error                    |
| Yellow    | #f9e2af   | Warning                  |
| Peach     | #fab387   | Orange accent            |

### Implications
- Conversion to `lipgloss.Color` is a one-liner: `lipgloss.Color(catppuccin.Mocha.Text().Hex)`
- No dependency on lipgloss from the catppuccin package itself — clean separation
- A small helper function `mocha(f func(catppuccin.Flavor) catppuccin.Color) lipgloss.Color` could reduce boilerplate across the theme package

### Risks and Uncertainties
- The package is code-generated via Whiskers; the API is stable but the generated files could drift if regenerated
- No version tag was pinned in steiner's go.mod at time of research — `go get github.com/catppuccin/go` will pull `latest`; pin a specific commit/tag for reproducibility

### Sources
- https://github.com/catppuccin/go/blob/main/main.go (Color struct, Flavor interface)
- https://github.com/catppuccin/go/blob/main/mocha.go (all color methods + hex values)
- https://github.com/catppuccin/go/blob/main/go.mod (module path)

### Open Questions
- Is `github.com/catppuccin/go` already in steiner's go.mod? (Not present as of 2026-04-22 — needs `go get`)
- Should a `themes` or `style` internal package own the palette conversion, or inline at use site?

---

## Q2: charmbracelet/bubbles textarea

### Question
Import path, native keybindings, multi-line paste support, command history, and what requires custom implementation?

### Findings

**Import path (steiner uses v0.21.0, old-style path):**
```go
import "github.com/charmbracelet/bubbles/textarea"
```
Note: the upstream master has migrated to `charm.land/bubbles/v2` but steiner pins `github.com/charmbracelet/bubbles v0.21.0` — use the old path.

**Native readline-style keybindings (from DefaultKeyMap):**
All of the following are handled natively — no custom implementation needed:

| Key           | Action (KeyMap field)    |
|---------------|--------------------------|
| ctrl+a / home | `LineStart`              |
| ctrl+e / end  | `LineEnd`                |
| ctrl+k        | `DeleteAfterCursor`      |
| ctrl+u        | `DeleteBeforeCursor`     |
| ctrl+w / alt+backspace | `DeleteWordBackward` |
| ctrl+f / right | `CharacterForward`      |
| ctrl+b / left  | `CharacterBackward`     |
| ctrl+n / down  | `LineNext`              |
| ctrl+p / up    | `LinePrevious`          |
| ctrl+d / delete | `DeleteCharacterForward` |
| ctrl+h / backspace | `DeleteCharacterBackward` |
| ctrl+t        | `TransposeCharacterBackward` |
| ctrl+v        | `Paste` (clipboard)     |
| alt+f / alt+right | `WordForward`       |
| alt+b / alt+left  | `WordBackward`      |
| alt+d / alt+delete | `DeleteWordForward` |
| alt+< / ctrl+home | `InputBegin`        |
| alt+> / ctrl+end  | `InputEnd`          |
| enter / ctrl+m | `InsertNewline`         |

All five classic readline bindings (ctrl+a, ctrl+e, ctrl+k, ctrl+w, ctrl+u) are handled natively.

**Multi-line paste detection:**
Paste is supported via `ctrl+v` (clipboard integration using `github.com/atotto/clipboard`). The component uses internal `pasteMsg`/`pasteErrMsg` message types, indicating OS clipboard paste is async. There is no bracketed-paste / terminal escape detection for detecting typed-vs-pasted multi-line input. If you need to detect "this was pasted, not typed" (e.g. to skip sending on newline), you would need to intercept at the BubbleTea `Update` level and compare clipboard content against typed characters.

**Command history navigation (up/down arrow):**
`LinePrevious` (up/ctrl+p) and `LineNext` (down/ctrl+n) are bound but they navigate lines within the current multi-line buffer, NOT command history. textarea has no built-in history stack. History (recalling previous submissions) requires custom implementation.

**What textarea does NOT handle:**
1. Command history — no history stack, no recall of previous inputs
2. Submit-on-Enter vs newline disambiguation — Enter always inserts newline; submit requires a different key (e.g. ctrl+s, ctrl+enter, or alt+enter) handled in the parent BubbleTea model
3. Bracketed paste detection — clipboard paste works via ctrl+v but there's no "was this pasted?" signal distinguishable from typed text at the textarea API level
4. Syntax highlighting of input content
5. Placeholder/prompt prefix rendering beyond the built-in `Placeholder` string

### Implications
- The five core readline bindings (ctrl+a/e/k/w/u) are free — no custom key handling needed for those
- History requires a `[]string` history slice + index in the parent model, with up/down intercepted before passing to textarea
- Submit-on-Enter must be disabled (rebind `InsertNewline` away from `enter`) or handled by checking the model state on enter
- The `Paste` binding (ctrl+v) is wired to OS clipboard — test on Linux (xclip/xsel dependency via atotto/clipboard)

### Risks and Uncertainties
- v0.21.0 may differ slightly from master's `DefaultKeyMap` — the tag `v0.21.0` returned empty when fetching via raw GitHub URL, suggesting the tag may be `v0.21.0` but the file structure differs; bindings confirmed from master and are unlikely to have changed materially for these core keys
- `github.com/atotto/clipboard` requires xclip or xsel on Linux headless — may fail in CI or SSH sessions; handle `pasteErrMsg` gracefully

### Sources
- https://github.com/charmbracelet/bubbles/blob/master/textarea/textarea.go (KeyMap struct + DefaultKeyMap)
- https://github.com/charmbracelet/bubbles/blob/master/go.mod (module path confirmation, now v2 at charm.land)
- steiner go.mod: `github.com/charmbracelet/bubbles v0.21.0`

### Open Questions
- Does v0.21.0 have `InputBegin`/`InputEnd` bindings? These appear in master — confirm once actual v0.21.0 source is readable
- What key should be the submit trigger? (alt+enter, ctrl+s, or a custom binding?)
- Should history be bounded (e.g. last 100 entries)?

---

## Q3: Glamour style sheets

### Question
How to pass a custom style sheet, what fields control colors, and is there a Go API for programmatic construction?

### Findings

**Versions verified:** glamour v0.10.0 (`github.com/charmbracelet/glamour v0.10.0`); API identical to master.

**Passing a custom style — three options:**

Option A: Programmatic Go struct (recommended for theme integration):
```go
import (
    "github.com/charmbracelet/glamour"
    "github.com/charmbracelet/glamour/ansi"
)

style := ansi.StyleConfig{
    Document: ansi.StyleBlock{
        StylePrimitive: ansi.StylePrimitive{
            BackgroundColor: ptr("#1e1e2e"),
            Color:           ptr("#cdd6f4"),
        },
    },
    // ... other fields
}
r, err := glamour.NewTermRenderer(
    glamour.WithStyles(style),
    glamour.WithWordWrap(80),
)
```

Option B: JSON bytes at runtime:
```go
r, err := glamour.NewTermRenderer(
    glamour.WithStylesFromJSONBytes(jsonBytes),
)
```

Option C: JSON file path:
```go
r, err := glamour.NewTermRenderer(
    glamour.WithStylesFromJSONFile("/path/to/style.json"),
)
```

**`StyleConfig` top-level fields:**
```go
type StyleConfig struct {
    Document   StyleBlock      // overall document wrapper
    BlockQuote StyleBlock
    Paragraph  StyleBlock
    List       StyleList
    Heading    StyleBlock      // base heading style
    H1–H6      StyleBlock      // per-level overrides
    Text       StylePrimitive
    Strikethrough, Emph, Strong, HorizontalRule StylePrimitive
    Item, Enumeration  StylePrimitive
    Task       StyleTask
    Link, LinkText     StylePrimitive
    Image, ImageText   StylePrimitive
    Code       StyleBlock      // inline code
    CodeBlock  StyleCodeBlock  // fenced code blocks
    Table      StyleTable
    DefinitionList, DefinitionTerm, DefinitionDescription StyleBlock/StylePrimitive
    HTMLBlock, HTMLSpan StyleBlock
}
```

**`StylePrimitive` color fields:**
```go
type StylePrimitive struct {
    Color           *string   // foreground hex e.g. "#cdd6f4"
    BackgroundColor *string   // background hex e.g. "#1e1e2e"
    Bold            *bool
    Italic          *bool
    Underline       *bool
    CrossedOut      *bool
    Faint           *bool
    Conceal         *bool
    Inverse         *bool
    Blink           *bool
    Upper, Lower, Title *bool
    Prefix, Suffix  string
    BlockPrefix, BlockSuffix string
    Format          string
}
```
Colors are `*string` (pointer to hex string), not `lipgloss.Color`. Use pointer helpers:
```go
func ptr(s string) *string { return &s }
func boolPtr(b bool) *bool { return &b }
```

**`StyleCodeBlock` for fenced code:**
```go
type StyleCodeBlock struct {
    StyleBlock            // inherits Color, BackgroundColor etc.
    Theme  string         // Chroma theme name e.g. "monokai", "dracula"
    Chroma *Chroma        // fine-grained token colors
}
```
For Catppuccin code blocks, set `Theme` to a matching Chroma theme (e.g. `"catppuccin-mocha"` if available in chroma, otherwise `"dracula"` or `"monokai"` as approximation), or populate `Chroma` field manually.

**Is the Go API for programmatic construction available?**
Yes — `glamour.WithStyles(ansi.StyleConfig)` accepts a fully populated Go struct. No JSON required. This is the cleanest path for a theme system that derives colors from catppuccin/go at runtime.

### Implications
- Full theme can be constructed in Go without any JSON files: derive hex strings from `catppuccin.Mocha.*().Hex`, wrap in `ptr()`, assign to `StyleConfig` fields, pass to `glamour.WithStyles()`
- `StyleCodeBlock.Theme` is a Chroma theme name string — check chroma's built-in theme list for a catppuccin variant; if absent, use a dark theme approximation
- Colors in glamour are hex strings (`*string`), same format as `catppuccin.Color.Hex` — direct assignment works without further conversion
- A `NewTermRenderer` must be re-created if the theme changes at runtime (no hot-swap)

### Risks and Uncertainties
- Chroma may not have a built-in `catppuccin-mocha` theme — need to verify chroma's theme registry (alecthomas/chroma v2); fallback to `"dracula"` or populate `Chroma` struct manually
- `StyleCodeBlock.BackgroundColor` may conflict with the terminal background if the renderer doesn't clear it — test against both light and dark terminal backgrounds
- `WithStyles` replaces the entire style (no merge/overlay) — must provide a complete config or extend an existing one

### Sources
- https://github.com/charmbracelet/glamour/blob/v0.10.0/glamour.go (WithStyles, WithStylesFromJSONBytes, NewTermRenderer)
- https://github.com/charmbracelet/glamour/blob/v0.10.0/ansi/style.go (StyleConfig, StylePrimitive, StyleBlock, StyleCodeBlock, Chroma)

### Open Questions
- Does chroma v2.14.0 (steiner's pinned version) include a catppuccin-mocha theme?
- Should the glamour renderer be constructed once at startup or recreated on theme change?
- Is `Document.BackgroundColor` needed, or should it be left nil to inherit the terminal's own background?
