## Question
What Go terminal libraries best support Stage 4 console UX foundations for `steiner` without introducing a full TUI or coupling rendering into provider logic?

## Findings
- Best line-editing candidate: `reeflective/readline`. It is pure Go, cross-platform, and explicitly supports multiline editing, history, Emacs/Vim modes, `.inputrc`, and multiple history sources. pkg.go.dev shows a published release on Jan 15, 2026, which makes it the strongest balance of capability and maintenance.
- Best lightweight fallback: `peterh/liner`. It is simpler and very small, with history, cursor movement, reverse search, and prompt refresh support, but pkg.go.dev shows its latest release on Jul 15, 2022. That makes it mature but comparatively stale.
- `nao1215/prompt` is active and more featureful than `liner`, with history, completion, theming, and multiline input. It is a reasonable option if we want a friendlier API, but it is more opinionated than Stage 4 likely needs.
- Best styled output stack: `lipgloss` + `termenv`. `lipgloss` gives expressive, dependency-light styling and layout; `termenv` handles terminal capability detection, color downsampling, and dark-background detection. That combination fits append-only rendering and terminal-aware default dark styling.
- Useful optional layer for status/tool/approval output: `charmbracelet/log`. It uses Lip Gloss, supports text/json/logfmt, and disables styling when output is not a TTY, which makes it a good fit for non-assistant channels.
- Full TUI frameworks are not the default fit here. `bubbletea` and `tview` are mature, but they pull the design toward a whole app-state/render loop. That is broader than Stage 4 needs and risks violating the “don’t couple streaming/rendering into provider code” constraint.

## Implications
- Preferred stack for Stage 4: `reeflective/readline` for input, `lipgloss` + `termenv` for rendering, and optionally `charmbracelet/log` for structured status output.
- If minimizing surface area matters more than feature depth, `liner` is the smallest workable prompt library, but the 2022 release age makes it a maintenance risk.
- A modest spec change is advisable: define an explicit terminal output contract with separate channels for `assistant`, `status`, `tool`, `approval`, and `error`, and require the renderer to own prompt refresh after external writes.
- That spec change softens the red line against rendering coupling while keeping the core architecture intact: input owns raw mode and history, output stays append-only, and provider code remains unaware of terminal repaint mechanics.

## Risks and Uncertainties
- `reeflective/readline` is the best fit on paper, but it is still more prompt machinery than a minimal shell primitive; validate that its history and refresh behavior stay simple enough for `steiner`.
- `liner` is attractive for its tiny scope, but its maintenance lag makes it a weaker long-term foundation.
- `charmbracelet/log` is convenient for system/status output, but it should not become the transcript renderer for assistant content.
- Terminal behavior still varies by platform and emulator, so prompt refresh, bracketed paste, and resize handling need local verification after a library choice.

## Sources
- [reeflective/readline](https://github.com/reeflective/readline)
- [peterh/liner](https://pkg.go.dev/github.com/peterh/liner)
- [nao1215/prompt](https://github.com/nao1215/prompt)
- [charmbracelet/lipgloss](https://pkg.go.dev/github.com/charmbracelet/lipgloss)
- [muesli/termenv](https://pkg.go.dev/github.com/muesli/termenv)
- [charmbracelet/log](https://github.com/charmbracelet/log)
- [charmbracelet/bubbletea](https://github.com/charmbracelet/bubbletea)
- [rivo/tview](https://github.com/rivo/tview)

## Open Questions
- Should Stage 4 mandate a minimal prompt-plus-output-router only, or allow a richer prompt subsystem now?
- Should history persistence stay in-memory for Stage 4, or become file-backed before hardening?
- Should dark styling default purely by project convention, or also adapt to terminal background detection through `termenv` when available?
