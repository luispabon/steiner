## Question
What current Bubble Tea, Bubbles viewport, and Lip Gloss patterns best support Stage 5 for `steiner`: a renderer-agnostic agent event interface, a Bubble Tea TUI foundation that replaces the interactive REPL, and a separate plain renderer that preserves `--exec` output?

## Findings
- Bubble Tea’s real boundary type is `tea.Msg`, and `Program.Send(msg)` is the supported way to inject messages into `Update` from outside the program. The docs also say `Send` blocks if the program has not started yet and becomes a no-op after termination, so it is a natural bridge for a goroutine-driven event stream, but only if startup/shutdown are managed explicitly.
- The most Bubble Tea-native pattern is a message bridge, not direct model access. A producer can live in a goroutine, translate domain events into concrete `tea.Msg` values, and hand them to `Program.Send`; the UI then stays decoupled from agent internals and only understands messages.
- Bubble Tea’s `View` is where terminal features are declared. The current docs show `AltScreen`, `MouseMode`, `ReportFocus`, and other terminal behavior on the view itself, which fits a minimal model structure and keeps rendering concerns local to the TUI layer.
- Resize handling is first-class: `WindowSizeMsg` is sent once at startup and again on every terminal resize. The viewport API provides `SetWidth`, `SetHeight`, `GotoBottom`, `PastBottom`, `EnsureVisible`, `ScrollUp/Down`, and paging helpers, so the Stage 5 model should treat terminal size changes as state transitions, not cosmetic redraws.
- Bubbles’ viewport component explicitly supports pager keybindings and mouse wheel support. For the planned “scroll wheel only” interaction model, Bubble Tea should enable mouse input at the program/view level but ignore click and motion events rather than building a richer mouse interaction surface.
- Bubble Tea’s shutdown path is split between in-loop and external control. Inside the model, `tea.Quit` is the normal exit command; from outside, `Program.Quit()` is the supported escape hatch. That makes it feasible to keep the agent loop in a background goroutine while Bubble Tea remains the main-thread UI owner.
- `WithoutRenderer()` is useful, but it is not the same thing as a dedicated plain renderer. The docs describe it as disabling redraw logic so output/logging behaves like a non-TUI command-line tool. That makes it a good regression check for `--exec`, but the Stage 5 architecture still needs a separate renderer over the shared event stream if plain and TUI output are meant to stay intentionally different.
- Lip Gloss remains the right companion for terminal layout/styling, but it is not a markdown renderer. That aligns with the Stage 5 constraint to defer markdown rendering entirely.

## Implications
- Treat the agent event interface as the source of truth and keep renderers as subscribers. The TUI and the plain `--exec` renderer should both consume the same structured events, not inspect agent state directly.
- Prefer a small event contract with explicit message types for stream chunks, status, approvals, resize, errors, and termination. That keeps the renderer boundary stable and makes approval prompts a UI concern instead of a blocking I/O concern in the agent loop.
- Use a thin TUI model: viewport state, input/approval state, status bar content, and a mode flag are enough for Stage 5. The model should not own agent transport, provider state, or stdout writes.
- Make the plain renderer a first-class implementation, not a special case hidden inside Bubble Tea. `WithoutRenderer()` can support tests and fallback CLI behavior, but a separate renderer object will keep `--exec` regression coverage honest.
- On resize, preserve scroll intent deliberately. If the viewport is pinned to the bottom while streaming, `GotoBottom()` is usually the right response; if the user has scrolled up, keep their position stable and use `EnsureVisible()` only when needed.

## Risks and Uncertainties
- `Program.Send` blocking before `Run()` can deadlock startup if the producer begins too early. A start barrier or a single forwarder goroutine is likely necessary.
- Unbounded streaming can outrun the UI. Plain chunk events, tool output, and status updates may need buffering, coalescing, or backpressure rules so a slow renderer does not stall the agent.
- Approval flow is the biggest interaction risk. If approval prompts share the same event lane as streaming output, the agent loop can keep producing data while the UI is waiting for an answer, which can create confusing state or deadlocks.
- Direct `fmt.Print*` or stderr writes from tools or agent code can corrupt the terminal experience in alt-screen mode. Bubble Tea’s unmanaged output helpers are only safe in specific cases, so the Stage 5 design should route all visible output through renderers.
- Mouse handling can easily drift beyond scope. Once click, motion, and focus behaviors are enabled, terminal compatibility issues and extra UI state follow quickly. The current scope should stay wheel-only.
- Viewport behavior around wide content and resize is easy to get subtly wrong. The current docs make it clear that `PastBottom()` can happen after height changes, so the implementation needs explicit resize tests with long streamed output.

## Sources
- [Bubble Tea docs](https://pkg.go.dev/charm.land/bubbletea/v2)
- [Bubble Tea README](https://github.com/charmbracelet/bubbletea)
- [Bubbles viewport docs](https://pkg.go.dev/charm.land/bubbles/v2/viewport)
- [Bubbles README](https://github.com/charmbracelet/bubbles)
- [Lip Gloss docs](https://pkg.go.dev/github.com/charmbracelet/lipgloss)

## Open Questions
- Should the event bridge use a callback interface, a channel interface, or both, with `Program.Send` only at the final Bubble Tea boundary?
- Should approval prompts pause the event stream entirely, or should the renderer buffer chunks while waiting for the user to answer?
- Should `--exec` be implemented as a separate plain renderer from day one, or as a compatibility layer that can also run through Bubble Tea’s `WithoutRenderer()` mode for tests?
- What is the minimum Stage 5 message set that still keeps the architecture stable: chunk, status, approval request/response, resize, error, and quit, or something narrower?
