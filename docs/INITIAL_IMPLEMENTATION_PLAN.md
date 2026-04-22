## Execution plan for steiner

This turns the staged roadmap into concrete engineering work. It assumes the reworked PRD v0.4.0 structure and current package layout direction.

---

## Working assumptions

* Go 1.24+
* single binary plus `steiner-core-tools`
* OpenAI-compatible chat completions first
* no persistence early
* no sub-agents until after context compaction is real and the TUI is professional-grade
* `provider.parallelism` enforced centrally, not inside agent code
* Charm stack (Bubble Tea + Lip Gloss + Glamour) for TUI, added from stage 5
* existing typed event system in `internal/output/log.go` extended, not rewritten
* `internal/repl/` deleted once TUI replaces it — command logic reimplemented in TUI input handler
* `internal/tui/` and `internal/tui/theme/` created from scratch

---

# Stage 0 - Foundations and architecture skeleton

## Objective

Get the repo into a shape where later stages do not need rewrites.

## Packages / files

### `cmd/steiner/main.go`

Implement:

* root CLI bootstrap
* subcommands:

  * default REPL entry
  * `version`
  * `config`

### `internal/config/`

Files:

* `config.go`
* `defaults.go`
* `env.go`
* `validate.go`

Implement:

* config structs
* default config
* load/merge logic
* env interpolation
* CLI override hooks
* validation

### `internal/provider/`

Files:

* `interface.go`
* `scheduler.go`
* `types.go`

Implement:

* `Provider` interface
* shared request/response types
* scheduler abstraction enforcing `provider.parallelism`

### `internal/agent/`

Files:

* `state.go`
* `limits.go`
* `types.go`

Implement:

* canonical message/event/state structs
* run state
* limit counters
* stop reason types

### `internal/tool/`

Files:

* `types.go`
* `registry.go`
* `schema.go`

Implement:

* tool definition structs
* registry loader from config
* OpenAI tool schema generation

### `internal/prompt/`

Files:

* `types.go`

Implement:

* context source types only
* no real assembly yet

### `internal/output/`

Files:

* `log.go`

Implement:

* structured logger setup
* event sink interface

## Concrete work items

1. Define canonical config schema.
2. Define internal message model.
3. Define provider request/response structs.
4. Implement concurrency scheduler with semaphore.
5. Implement config loading precedence.
6. Add config validation.
7. Add CLI stubs.

## Tests

### Unit

* config precedence
* env interpolation
* invalid config rejection
* tool schema generation
* scheduler blocks beyond configured parallelism

### Integration

* `steiner config` prints resolved merged config
* invalid config exits non-zero with actionable error

## Exit criteria

* project compiles
* scheduler proven with tests
* config stable enough not to churn later

---

# Stage 1 - Core single-agent loop

## Objective

Ship a minimal usable agent.

## Packages / files

### `internal/provider/openai_compat.go`

Implement:

* chat completion call
* streaming support
* tool call parsing
* usage extraction when present

### `internal/agent/loop.go`

Implement:

* main ReAct loop
* final text vs tool call branching
* turn counting
* provider call integration

### `internal/prompt/`

Files:

* `system.go`
* `agents.go`
* `context.go`
* `skills.go`
* `assemble.go`

Implement:

* fixed preamble
* load global/project `AGENTS.md`
* bounded project context loading
* skill loading/injection
* prompt assembly order

### `internal/skill/loader.go`

Implement:

* discover global skills
* load `SKILL.md`

### `internal/repl/`

Files:

* `repl.go`
* `commands.go`
* `completer.go`

Implement:

* interactive loop
* `/help`, `/tools`, `/skills`, `/clear`, `/exit`
* skill invocation
* history later can be basic if needed

### `internal/tool/`

Files:

* `executor.go`
* `approval.go`

Implement:

* direct subprocess executor
* approval flow
* JSON tool I/O contract

### `cmd/steiner-core-tools/`

Files:

* `main.go`
* `read.go`
* `write.go`
* `glob.go`
* `search.go`
* `bash.go`

Implement:

* core tools only
* strict JSON contract

### `internal/output/stream.go`

Implement:

* plain streaming to terminal
* simple tool execution headers

## Concrete work items

1. Implement OpenAI-compatible provider.
2. Normalize streaming and non-streaming responses.
3. Implement direct executor.
4. Implement five core tools.
5. Implement prompt assembly.
6. Implement single-agent run loop.
7. Implement REPL and `--exec`.
8. Implement approval prompts.
9. Emit structured events for:

   * model call start/end
   * tool call start/end
   * approval requested/accepted/denied
   * stop reason

## Tests

### Unit

* prompt assembly order
* skills not injected unless invoked
* tool JSON wrapping/parsing
* approval mode resolution
* provider response normalization
* max turn stopping

### Integration

* fake provider returns tool call -> tool executes -> final answer
* REPL command handling
* `--exec` path works headlessly
* core tools run against temp repo
* `parallelism: 1` scheduler serializes simultaneous provider requests

### Golden tests

* prompt assembly snapshot
* tool schema snapshot

## Exit criteria

* agent can inspect a small repo, change one file, run test, explain result

---

# Stage 2 - Execution safety and safer mutation

## Objective

Make it harder for the model to do dumb damage.

## Packages / files

### `internal/tool/policy.go`

Implement:

* project-root confinement
* blocked paths
* writable path rules
* cwd policy

### `internal/tool/output.go`

Implement:

* stdout/stderr capture caps
* truncation metadata
* binary detection

### `cmd/steiner-core-tools/edit.go`

Implement:

* `edit` tool with exact old/new replacement

### `internal/tool/preview.go`

Implement:

* diff/replacement preview generation for approvals

### `internal/agent/tool_result.go`

Implement:

* normalized tool result envelope with truncation flags

## Concrete work items

1. Add path policy layer before execution.
2. Enforce shell cwd under project root.
3. Cap tool output size.
4. Detect binary output.
5. Add `edit` tool.
6. Prefer `edit` in tool schemas and docs, keep `write`.
7. Improve approval previews.

## Tests

### Unit

* path traversal rejection
* blocked path rejection
* writable path allowlist
* tool output truncation behaviour
* binary output detection
* exact replacement success/failure cases for `edit`

### Integration

* bash rejected outside project root
* dangerous file writes denied by policy
* large command output truncated cleanly
* approval preview contains path/diff/timeout

## Exit criteria

* safe enough for normal repo use without relying only on user vigilance

---

# Stage 3 - Context discipline and compaction

## Objective

Stop context from rotting.

## Packages / files

### `internal/prompt/`

Files:

* `assembler.go`
* `budget.go`
* `retention.go`
* `compaction.go`
* `summary.go`

Implement:

* source budgets
* retention rules
* rolling compaction
* summary block model

### `internal/agent/context_state.go`

Implement:

* active constraints
* unresolved tasks
* retained summaries

### `internal/output/debug.go`

Implement:

* optional prompt/context diagnostics

## Concrete work items

1. Define internal context source enum and budgets.
2. Implement per-source budgeting.
3. Add rolling summary blocks for older turns.
4. Preserve:

   * user constraints
   * active file/task focus
   * recent turns
5. Truncate tool output into structured summaries.
6. Add optional debug command:

   * `/history`
   * maybe `/prompt` later
7. Log compaction events.

## Tests

### Unit

* compaction preserves active constraints
* recent turns retained verbatim
* old turns summarized
* project context budget enforced
* tool summaries not treated as system instructions

### Integration

* long synthetic conversation does not grow unbounded
* after many tool calls, final prompt size remains within expected range
* important constraints survive compaction

### Golden tests

* compacted conversation snapshot
* assembled prompt with summaries snapshot

## Exit criteria

* long sessions are materially more stable than naive replay

---

# Stage 4 - Console UX foundations

## Objective

Make the interactive console feel like a real terminal product without changing the single-agent architecture.

## Packages / files

### `internal/repl/`

Files:

* `repl.go`
* `commands.go`
* `completer.go`
* `terminal.go` if needed

Implement:

* line-editing capable terminal input layer
* history navigation
* cleaner command/help presentation
* stronger separation between prompt input, assistant replies, and status output

### `internal/output/`

Implement:

* richer terminal rendering path
* default dark theme tokens/styles
* streaming-aware assistant output renderer
* stronger approval/tool/status formatting

### `cmd/steiner/main.go`

Extend:

* terminal setup and mode wiring needed for richer interactive rendering
* keep `--exec` and interactive output paths consistent where practical

## Concrete work items

1. Replace line-buffered prompt handling with a real terminal input model.
2. Add prompt history navigation and cursor movement.
3. Split human-facing replies from status/event rendering more clearly.
4. Add streaming-capable output path without bypassing the existing event model.
5. Improve approval rendering so path, action, and key arguments are easier to scan.
6. Define and apply a strong default dark theme for terminal output.

## Tests

### Unit

* REPL command parsing still behaves correctly
* completion behavior still respects built-in commands vs skills
* output formatting preserves important approval/truncation details
* streaming renderer handles incremental chunks without corrupting terminal state

### Integration

* interactive prompt supports history and editing behavior
* streamed and non-streamed replies render coherently
* approvals remain usable during interactive sessions
* `--exec` output still remains deterministic and readable

## Exit criteria

* interactive use no longer feels like a basic `ReadString('\n')` loop
* approvals, tool activity, and assistant replies are clearly distinguishable
* richer rendering does not require changes to prompt assembly or agent state semantics

---

# Stage 5 - Agent event interface and TUI foundation

## Objective

Replace the line-oriented REPL with a Bubble Tea TUI application, built on a clean event-driven boundary between the agent core and all rendering.

## Dependencies

* go-readline-ny removed from go.mod after this stage (REPL deleted)
* new go.mod dependencies: `charmbracelet/bubbletea`, `charmbracelet/lipgloss`
* `charmbracelet/glamour` is NOT added yet — stage 6

## Packages / files

### `internal/output/log.go`

Extend the existing typed event system:

* add new event type constants alongside existing ones: `EventStreamChunk`, `EventStreamComplete`, `EventThinkingChunk`, `EventThinkingComplete`, `EventToolCallRequested`, `EventApprovalRequired`, `EventApprovalResult`, `EventToolExecuting`, `EventContextUpdate`, `EventLimitHit`, `EventSkillActivated`, `EventCompactionFired`, `EventRunComplete`
* add corresponding typed payload structs for each new event type:

  * `StreamChunkEvent` — incremental text content from streaming response
  * `StreamCompleteEvent` — streaming response finished
  * `ThinkingChunkEvent` — incremental thinking/reasoning content
  * `ThinkingCompleteEvent` — thinking block finished
  * `ToolCallRequestedEvent` — tool call pending, includes tool name, arguments, approval mode
  * `ApprovalRequiredEvent` — tool call awaiting user approval (may extend or replace existing `ApprovalEvent`)
  * `ApprovalResultEvent` — user approved or denied
  * `ToolExecutingEvent` — tool execution in progress
  * `ContextUpdateEvent` — context budget changed, includes tokens used/budget, compaction state
  * `LimitHitEvent` — a termination control fired, includes which limit and current values
  * `SkillActivatedEvent` — a skill was toggled on
  * `CompactionFiredEvent` — conversation history was compacted, includes turns affected
  * `RunCompleteEvent` — agent loop finished, includes stop reason
* retain all existing event types and payloads (`ModelCallStartedEvent`, `ModelCallFinishedEvent`, `ToolCallStartedEvent`, `ToolCallFinishedEvent`, `ApprovalEvent`, `StopReasonEvent`, `UserInputEvent`, `APIRequestEvent`, `APIResponseEvent`, `ContextDiagnosticsEvent`) — existing payloads may be refactored where the new types supersede them, but nothing is silently dropped
* add constructor functions for each new event type following the existing `NewXxxEvent()` pattern

### `internal/output/events.go` (new file)

Define the renderer subscriber interface:

* `EventSubscriber` interface with a `HandleEvent(Event)` method (or equivalent channel-based contract)
* this is the seam both renderers implement
* the agent loop emits events to all registered subscribers without knowing which renderer is active
* consider whether the interface is push-based (callback) or pull-based (channel) — the ROADMAP recommends channel with `tea.Cmd` consumption for the TUI, so a channel-based approach likely works best: the agent loop sends to a channel, subscribers read from it

### `internal/output/plain.go` (new file, extracted from stream.go)

Extract and refactor the existing `stream.go` rendering logic into a standalone plain renderer:

* implements `EventSubscriber`
* subscribes to the event channel
* writes formatted text to stdout/stderr for `--exec` mode
* preserves current single-shot output behaviour exactly — this is the regression gate
* the existing `renderEvent()` → Segment → formatted output pipeline moves here
* `stream.go` may be deleted or reduced to shared formatting utilities once extraction is complete

### `internal/output/stream.go`

Refactor:

* rendering logic extracted to `plain.go`
* any shared formatting helpers (segment rendering, colour utilities) remain here or move to a shared file
* the file may be deleted entirely if nothing remains after extraction

### `internal/agent/loop.go`

Refactor:

* all direct stdout/stderr writes removed
* all output goes through the event channel
* the loop emits the new event types at appropriate points: `StreamChunk` on each streaming delta, `StreamComplete` when the response finishes, `ThinkingChunk`/`ThinkingComplete` for reasoning content, `ToolCallRequested` before execution, `ContextUpdate` after prompt assembly, `LimitHit` on termination controls, `RunComplete` at loop exit
* the loop accepts an event channel (or subscriber list) at construction
* the loop must not import or reference any rendering package

### `internal/agent/events.go` (new file)

Implement:

* helper functions for emitting events from the agent loop
* keeps `loop.go` clean by centralising event emission logic

### `internal/provider/openai_compat.go`

Refactor:

* remove any direct stdout/stderr writes
* streaming deltas emitted as events through the agent loop's channel, not written directly
* all diagnostic output goes to the logger only

### `internal/tool/executor.go`

Refactor:

* remove any direct stdout/stderr writes
* tool execution status and results emitted as events
* approval interaction no longer handled by the tool executor directly — it emits `ApprovalRequired` and waits for an `ApprovalResult` (mechanism TBD: could be a response channel, a callback, or a blocking call gated by the TUI)

### `internal/tui/` (new package)

Files:

* `app.go`
* `model.go`
* `content.go`
* `input.go`
* `statusbar.go`
* `keys.go`

#### `app.go`

Implement:

* `Run()` function that initialises the Bubble Tea program, registers the TUI as an event subscriber, starts the agent loop on a background goroutine, and hands control to Bubble Tea on the main thread
* program setup with `tea.WithAltScreen()` and `tea.WithMouseCellMotion()` (scroll wheel only)
* clean shutdown on `/exit`, ctrl+c, or agent loop completion

#### `model.go`

Implement:

* root `Model` struct implementing `tea.Model` (`Init`, `Update`, `View`)
* model state: content buffer, input buffer, status bar state, viewport dimensions, approval state, agent-running flag
* `Init()` returns a `tea.Cmd` that starts reading from the event channel
* `Update()` handles: `tea.KeyMsg` (input), `tea.WindowSizeMsg` (resize), `tea.MouseMsg` (scroll wheel), and custom `tea.Msg` types wrapping agent events
* `View()` composes the layout: content pane above, input area below, status bar at the bottom
* event-to-msg bridge: a `tea.Cmd` that reads the next event from the channel and wraps it as a `tea.Msg`, then returns another such `tea.Cmd` to keep the subscription alive

#### `content.go`

Implement:

* content buffer model that accumulates rendered content
* appends streamed text chunks as they arrive (plain text in this stage, no markdown rendering)
* appends tool execution summaries as compact text blocks
* appends thinking block content as plain text
* scrollable viewport: keyboard navigation (page up/down, home/end, arrow keys) and scroll wheel
* auto-scroll to bottom on new content, with scroll-lock if user has scrolled up

#### `input.go`

Implement:

* text input area at the bottom of the content pane
* visually distinct from content (Lip Gloss border or background colour)
* single-line input in this stage (multi-line deferred to stage 7)
* enter sends the input as a user message to the agent loop
* slash commands recognised and dispatched: `/help`, `/tools`, `/skills`, `/history`, `/clear`, `/exit`
* skill invocation via `/<skill-name>`
* command logic reimplemented here — not extracted from `internal/repl/`
* during approval prompts: input area switches to approval mode (y/n/details), then returns to normal input after resolution
* input disabled while agent is processing (visual indicator)

#### `statusbar.go`

Implement:

* bottom bar showing: model name, current turn/max turns, basic context token stats (used/budget), keybind hints
* updated via `ContextUpdate` and `TurnStarted` events
* Lip Gloss styling: muted background, compact layout

#### `keys.go`

Implement:

* keybinding definitions using `charmbracelet/bubbles/key` or direct key matching
* bindings for: quit (ctrl+c), scroll (page up/down, home/end), submit input (enter), approval (y/n)

### `cmd/steiner/main.go`

Extend:

* mode selection: if `--exec`, use plain renderer; otherwise launch TUI
* plain renderer wired to event channel for `--exec` path
* TUI `Run()` called for interactive path
* `internal/repl/` no longer imported for interactive mode

### `internal/repl/` (deleted)

* entire package removed after TUI is functional and all command logic is reimplemented in `internal/tui/input.go`
* `go-readline-ny` dependency removed from go.mod

## Concrete work items

1. Define new event type constants and payload structs in `internal/output/log.go`.
2. Define `EventSubscriber` interface in `internal/output/events.go`.
3. Extract `stream.go` rendering logic into `plain.go` as a standalone `EventSubscriber` implementation.
4. Validate plain renderer against current `--exec` output — this is the regression gate before any TUI work begins.
5. Refactor `internal/agent/loop.go` to emit all output as events. Remove every direct stdout/stderr write.
6. Audit `internal/provider/openai_compat.go` and `internal/tool/executor.go` for direct writes. Remove all of them.
7. Design the approval interaction contract: how does the TUI (or plain renderer) receive an approval request, collect user input, and return the result to the blocked agent loop? Likely a pair of channels or a callback with a response channel.
8. Add bubbletea and lipgloss to go.mod.
9. Implement `internal/tui/model.go` with the event-to-msg bridge.
10. Implement `internal/tui/content.go` with plain text streaming and scrollable viewport.
11. Implement `internal/tui/input.go` with single-line input, command dispatch, and approval mode.
12. Implement `internal/tui/statusbar.go`.
13. Implement `internal/tui/app.go` with program setup and agent loop goroutine.
14. Wire mode selection in `cmd/steiner/main.go`.
15. Delete `internal/repl/` and remove `go-readline-ny` from go.mod.
16. Enforce the no-direct-writes rule: grep the entire codebase for `fmt.Print`, `fmt.Fprint`, `os.Stdout`, `os.Stderr` outside of `internal/output/plain.go` and `cmd/` entry points. Any remaining direct writes are a bug.

## Tests

### Unit

* new event type constructors produce correct types and payloads
* plain renderer produces identical output to pre-refactor `stream.go` for all existing event types
* event-to-msg bridge converts each event type to the correct `tea.Msg`
* content buffer appends and scrolls correctly
* input handler dispatches slash commands correctly
* input handler switches to approval mode and back
* status bar formats context stats correctly
* keybindings resolve correctly

### Integration

* `--exec` with fake provider produces identical output to pre-refactor baseline (golden test)
* TUI launches, receives streamed events, renders content, and accepts input via programmatic `tea.Msg` injection
* approval flow works end-to-end: agent emits `ApprovalRequired`, TUI shows prompt, user responds, agent receives result and continues
* terminal resize reflows layout correctly
* scroll wheel scrolls content pane
* `/clear` resets content buffer
* `/exit` shuts down cleanly
* no stdout/stderr writes exist outside the plain renderer (enforced by grep or build-time check)

### Golden tests

* plain renderer output snapshot for a synthetic multi-turn conversation with tool calls, approvals, and streaming

## Exit criteria

* the interactive console is a functional Bubble Tea application that streams responses, handles input, and displays status
* `--exec` mode works identically to before via the plain renderer
* no stdout/stderr writes exist anywhere in the agent loop, provider, or tool packages
* the event interface cleanly separates the agent loop from all rendering concerns
* scroll wheel works in the content pane
* terminal resize reflows the layout correctly
* approval prompts work correctly in the TUI input area

---

# Stage 6 - Markdown rendering and status sidebar

## Objective

Make the TUI content pane render styled markdown and provide persistent session visibility through a status sidebar.

## Dependencies

* new go.mod dependency: `charmbracelet/glamour`
* stage 5 complete: functional TUI with proven event interface and both renderers working

## Packages / files

### `internal/tui/content.go`

Extend:

* integrate Glamour for rendering assistant response blocks
* streaming markdown strategy:

  * buffer incoming `StreamChunk` events
  * track block boundaries: blank lines, code fence markers (```` ``` ````), header markers (`#`), list item starts
  * when a block boundary is detected, render the completed block through Glamour and append the styled output to the content buffer
  * the in-progress block (text after the last boundary) renders as plain unstyled text below the last styled block
  * when `StreamComplete` fires, render any remaining buffered text through Glamour
* code blocks rendered with syntax highlighting via Chroma (Glamour uses Chroma internally)
* full markdown support: headers, bold, italic, inline code, code blocks, lists, tables, links
* tool execution blocks rendered as muted compact inline blocks: tool name, key arguments, approval state, result summary, truncation status — visually subordinate to assistant prose
* thinking blocks rendered as muted, visually de-emphasised regions — visually subordinate to assistant prose
* approval prompts rendered by highlighting the pending tool call block and activating approval interaction in the input area

### `internal/tui/sidebar.go` (new file)

Implement:

* right-side panel showing live session metrics
* contents:

  * model name
  * provider endpoint
  * context tokens used / budget with percentage
  * current turn / max turns
  * compaction state: whether fired, turns affected
  * git branch and dirty state
  * working directory
  * active skills
* sidebar width fixed at 30-35 characters
* automatic collapse below the terminal width threshold configured in `tui.sidebar_min_width` (default 120)
* manual toggle via keybind (e.g. ctrl+b or similar)
* clear visual boundary between content pane and sidebar (Lip Gloss border)
* sidebar state updated by consuming `ContextUpdate`, `CompactionFired`, `SkillActivated`, and `TurnStarted` events

### `internal/tui/model.go`

Extend:

* model state gains: sidebar visible flag, sidebar state struct, markdown block buffer
* `View()` updated to compose three-region layout: content pane (left), sidebar (right, conditional), status bar (bottom)
* sidebar visibility toggled by keybind and by terminal width on resize
* layout reflow on `tea.WindowSizeMsg`: content pane width adjusts when sidebar appears/disappears

### `internal/tui/keys.go`

Extend:

* add sidebar toggle keybind

### `internal/tui/git.go` (new file)

Implement:

* git branch detection (read `.git/HEAD`)
* git dirty state detection (shell out to `git status --porcelain` or read git index)
* called at session start and after `ToolResult` events where the tool is `write` or `edit`
* must not shell out on every render cycle — cache result and refresh on relevant events only

### `internal/tui/render.go` (new file)

Implement:

* Glamour style sheet configuration: use the built-in Catppuccin or Dracula preset if available, or construct a custom JSON-style sheet using hardcoded Catppuccin Mocha palette values
* this is NOT the theme abstraction (stage 7) — hardcoded palette values are acceptable here
* Lip Gloss style definitions for: content pane chrome, sidebar chrome, sidebar labels/values, tool execution blocks (muted), thinking blocks (muted), assistant prose (full brightness), approval highlight, input area, status bar
* all colour values hardcoded as hex strings in this stage — stage 7 extracts them into the theme interface

## Concrete work items

1. Add glamour to go.mod.
2. Implement the streaming markdown block-boundary detection in `content.go`.
3. Implement Glamour rendering of completed blocks with Chroma syntax highlighting.
4. Implement in-progress block display as plain text below styled content.
5. Implement muted rendering for tool execution blocks: compact format showing tool name, key args, approval state, result summary, truncation status.
6. Implement muted rendering for thinking blocks.
7. Implement the sidebar panel in `sidebar.go` with all specified metrics.
8. Implement sidebar collapse/restore on terminal width changes.
9. Implement sidebar toggle keybind.
10. Implement git branch and dirty state detection in `git.go`.
11. Wire sidebar updates from `ContextUpdate`, `CompactionFired`, `SkillActivated`, and `TurnStarted` events.
12. Define hardcoded Lip Gloss styles in `render.go` using Catppuccin Mocha palette hex values.
13. Configure Glamour style sheet with matching palette.
14. Update `model.go` layout to support three-region composition.
15. Verify visual hierarchy: assistant prose at full brightness, tool/thinking content visually subordinate.

## Tests

### Unit

* block boundary detection correctly identifies: paragraph breaks, code fence open/close, header lines, list items
* completed blocks render through Glamour without error
* in-progress text appended below styled content
* sidebar formats all metric fields correctly
* sidebar collapse logic triggers at the correct width threshold
* git branch parsing handles detached HEAD, normal branch, missing `.git/`
* tool execution block formatting includes all required fields (name, args, approval, result, truncation)

### Integration

* streamed multi-paragraph response renders with correct markdown styling (bold, italic, headers, code blocks)
* code blocks display syntax highlighting
* long responses with interleaved tool calls show correct visual hierarchy
* sidebar shows accurate live metrics during a synthetic agent run
* sidebar collapses and restores correctly on programmatic resize events
* sidebar toggles via keybind
* approval prompt highlights the pending tool call block
* streaming response with incomplete code block shows plain text until fence closes, then renders styled

### Golden tests

* rendered markdown output snapshot for a response containing: prose, code block, inline code, list, table
* sidebar layout snapshot at various terminal widths (above threshold, at threshold, below threshold)

## Exit criteria

* assistant prose renders as styled markdown with syntax-highlighted code blocks
* tool and thinking content is visually subordinate to assistant prose
* the sidebar shows accurate live context and session metrics
* the sidebar collapses and restores correctly on terminal resize and keybind toggle
* streaming responses show styled completed blocks above and plain in-progress text below
* the visual hierarchy is immediately legible — a user can glance at the screen and distinguish prose from tool noise

---

# Stage 7 - Theme system and input polish

## Objective

Extract the hardcoded colour palette into a swappable theme abstraction and bring the input area up to a professional standard.

## Dependencies

* new go.mod dependency: `catppuccin/go`
* stage 6 complete: markdown rendering and sidebar working with hardcoded palette values
* all colour values identifiable for extraction into the theme interface

## Packages / files

### `internal/tui/theme/theme.go` (new file)

Implement:

* `Theme` interface defining a semantic colour palette:

  * `Background`, `Foreground` — base terminal colours
  * `Accent` — primary highlight colour
  * `Muted` — de-emphasised content (tool blocks, thinking blocks)
  * `Border` — panel and region borders
  * `Error`, `Warning`, `Success` — diagnostic colours
  * `SyntaxKeyword`, `SyntaxString`, `SyntaxComment`, `SyntaxFunction`, `SyntaxNumber`, `SyntaxOperator` — syntax highlight groups
* keep the palette to 12-15 named colours — more than that becomes unmanageable
* `Theme` exposes methods to derive Lip Gloss styles and Glamour style sheets from the palette:

  * `LipGlossStyles() Styles` — returns a struct of pre-built Lip Gloss styles for all TUI chrome regions
  * `GlamourStyleSheet() glamour.TermRendererOption` — returns a Glamour style configuration derived from the palette
* the `Styles` struct holds named Lip Gloss styles for: content pane, sidebar, sidebar labels, sidebar values, tool block, thinking block, assistant prose, approval highlight, input area, status bar, border, error, warning, success

### `internal/tui/theme/registry.go` (new file)

Implement:

* theme registry: `map[string]Theme`
* `Register(name string, theme Theme)` — adds a theme to the registry
* `Get(name string) (Theme, error)` — retrieves a theme by name
* `Default() Theme` — returns Catppuccin Mocha
* themes registered at init time via `init()` functions in their respective files

### `internal/tui/theme/catppuccin.go` (new file)

Implement:

* Catppuccin Mocha theme implementing the `Theme` interface
* palette values sourced from the `catppuccin/go` package, not hardcoded hex strings
* Lip Gloss style derivation from palette values
* Glamour style sheet generation from palette values
* registered in the theme registry as `"catppuccin-mocha"`

### `internal/tui/render.go`

Refactor:

* remove all hardcoded hex colour values
* replace with theme-derived styles: at TUI startup, load the active theme from config (`tui.theme`), call `LipGlossStyles()` and `GlamourStyleSheet()`, and pass the results to all components that need them
* every Lip Gloss style reference in the TUI must come from the theme, not from inline colour definitions

### `internal/tui/content.go`

Refactor:

* Glamour renderer initialised with the theme-derived style sheet instead of the hardcoded one
* tool block and thinking block styles sourced from the theme

### `internal/tui/sidebar.go`

Refactor:

* all sidebar styles sourced from the theme

### `internal/tui/statusbar.go`

Refactor:

* all status bar styles sourced from the theme

### `internal/tui/input.go`

Extend:

* multi-line editing support:

  * evaluate Bubble Tea's `textarea` bubble as a starting point
  * if `textarea` handles multi-line, paste, and readline bindings well enough, use it
  * if not, implement a custom input model
* command history navigation via up/down arrow keys
* readline-style keybindings:

  * ctrl+a — start of line
  * ctrl+e — end of line
  * ctrl+k — kill to end of line
  * ctrl+w — delete word back
  * ctrl+u — kill entire line
  * standard cursor movement (left/right, word-jump with ctrl+left/right)
* slash-command and skill name completion:

  * triggered by tab after `/`
  * completes against known built-in commands and discovered skill names
  * contextual: completion only activates after `/` prefix, otherwise no completion
* paste handling:

  * multi-line paste detected and handled correctly
  * code block content preserved with formatting
  * no accidental command dispatch on pasted lines starting with `/`
* input area styles sourced from the theme

### `internal/tui/help.go` (new file)

Implement:

* help overlay toggled via keybind (`?` or `ctrl+?`)
* displays available keybindings grouped by function:

  * Navigation: scroll up/down, page up/down, home/end
  * Input: submit, history up/down, readline bindings
  * Session: clear, exit, sidebar toggle
  * Approval: approve, deny
* lightweight overlay rendered within the TUI — not a separate screen or mode
* Lip Gloss styled, semi-transparent or bordered overlay on top of content pane
* dismissed by pressing the same keybind or escape
* styles sourced from the theme

### `internal/tui/keys.go`

Extend:

* add help overlay toggle keybind
* add readline keybindings
* add history navigation keybindings

### `internal/tui/model.go`

Extend:

* model state gains: help overlay visible flag, theme reference, input history buffer
* theme loaded from config at startup via registry lookup
* `Update()` handles help overlay toggle
* `View()` conditionally renders help overlay on top of normal layout

### `internal/tui/app.go`

Extend:

* theme loading from `tui.theme` config value at startup
* theme passed to all components during initialisation
* error handling if configured theme name is not found in registry (fall back to default with warning)

## Concrete work items

1. Add `catppuccin/go` to go.mod.
2. Define `Theme` interface in `theme/theme.go` with semantic colour palette and style derivation methods.
3. Define `Styles` struct with named Lip Gloss styles for all TUI regions.
4. Implement theme registry in `theme/registry.go`.
5. Implement Catppuccin Mocha theme in `theme/catppuccin.go` using `catppuccin/go` palette values.
6. Refactor `render.go`: remove all hardcoded hex values, replace with theme-derived styles.
7. Refactor `content.go`, `sidebar.go`, `statusbar.go`: all styles from theme.
8. Grep entire `internal/tui/` for hardcoded colour values — any remaining are a bug.
9. Wire theme loading from config in `app.go`.
10. Implement multi-line input editing in `input.go` (evaluate `textarea` bubble first).
11. Implement command history navigation.
12. Implement readline-style keybindings.
13. Implement tab completion for slash commands and skill names.
14. Implement paste handling with multi-line and code block awareness.
15. Implement help overlay in `help.go`.
16. Wire help overlay toggle in `model.go` and `keys.go`.
17. Verify theme swappability: temporarily create a second minimal test theme, confirm all styling changes with no code changes outside the theme definition and registry, then remove the test theme.

## Tests

### Unit

* Catppuccin Mocha theme produces valid Lip Gloss styles for all named regions
* Catppuccin Mocha theme produces a valid Glamour style sheet
* theme registry stores, retrieves, and returns default correctly
* a second test theme can be registered and retrieved with no code changes outside the theme file
* multi-line input handles enter (with shift/alt modifier for newline vs submit), cursor movement, and line wrapping
* command history navigation cycles through previous inputs
* readline keybindings produce correct cursor/text mutations
* tab completion after `/` suggests correct commands and skill names
* tab completion without `/` prefix does nothing
* paste detection handles multi-line input correctly
* pasted lines starting with `/` are not dispatched as commands
* help overlay content includes all registered keybindings

### Integration

* TUI renders correctly under Catppuccin Mocha: content pane, sidebar, status bar, input area all use theme colours
* markdown rendering uses theme-derived Glamour style sheet (code blocks, headers, emphasis)
* tool blocks and thinking blocks render in the theme's muted colour
* sidebar labels and values use correct theme styles
* input area multi-line editing works: type multiple lines, navigate with arrows, submit with enter
* command history cycles correctly through previously submitted inputs
* tab completion after `/he` completes to `/help`
* paste a multi-line code block into input area, submit, verify it arrives as a single message
* help overlay displays and dismisses cleanly without disrupting content or input state
* help overlay keybind groupings are legible

### Golden tests

* screenshot or rendered output snapshot of a sample conversation under Catppuccin Mocha theme

## Exit criteria

* the TUI renders correctly under Catppuccin Mocha with consistent styling across chrome, markdown content, and syntax highlighting
* all colour values come from the theme interface, not hardcoded literals
* a second theme can be added by defining a palette struct and registering it, with no other code changes required
* multi-line input, command history, readline keybindings, and paste handling all work correctly
* tab completion suggests commands and skill names after `/`
* the help overlay displays and dismisses cleanly

---

# Stage 8 - Delegation scaffolding

## Objective

Build the seam, not yet the full feature.

## Packages / files

### `internal/delegation/`

Files:

* `contract.go`
* `task.go`
* `result.go`
* `scaffold.go`
* `limits.go`

Implement:

* parent->child task contract
* child result envelope
* scoped context handoff builder
* sub-agent config/limit model

### `internal/agent/subagent_state.go`

Implement:

* isolated agent state type

### `internal/provider/scheduler.go`

Extend:

* all agent instances share scheduler budget
* future-safe for parent/child use

### `internal/output/log.go`

Extend:

* add delegation event types: `EventDelegationStarted`, `EventDelegationComplete`, `EventDelegationFailed`
* add corresponding typed payloads: `DelegationStartedEvent`, `DelegationCompleteEvent`, `DelegationFailedEvent`

### `internal/tui/content.go`

Extend:

* TUI awareness of delegation events for future rendering (placeholder handling — log to content pane as muted blocks)

## Concrete work items

1. Define delegation request struct.
2. Define delegation result struct.
3. Define what context can be passed to child.
4. Build child state lifecycle behind interface.
5. Ensure scheduler gates all model calls across parent/child.
6. Add delegation event types and payloads.
7. Add placeholder delegation event rendering in TUI.

## Tests

### Unit

* delegation contract serialization
* child state is isolated from parent state
* allowed-tools filtering
* limit inheritance/override rules

### Integration

* instantiate child run behind internal interface without surfacing to model yet
* scheduler still enforces `parallelism` across multiple agent instances
* delegation events flow through the event interface and appear in TUI

## Exit criteria

* sub-agent execution can be added without refactoring the loop architecture

---

# Stage 9 - Sub-agent execution v1

## Objective

Real delegation with isolation.

## Packages / files

### `internal/agent/subagent.go`

Implement:

* synchronous child execution
* bounded context handoff
* result-only return

### `internal/tool/spawn_agent.go` or internal synthetic tool exposure

Implement:

* `spawn_agent` schema
* mapping from tool call to delegation runtime

### `internal/tui/content.go`

Extend:

* delegation rendering: muted inline block showing task sent, active spinner or indicator during execution, compact result display on completion

## Concrete work items

1. Expose `spawn_agent` to model.
2. Build context handoff from parent state.
3. Run child loop synchronously.
4. Return compact child result to parent.
5. Enforce:

   * no nesting
   * child tool subset
   * child limit overrides
6. Keep child transcript out of parent history.
7. Implement TUI delegation rendering.

## Tests

### Unit

* parent/child context separation
* child result envelope integration
* nesting denied
* child tool subset enforced

### Integration

* model delegates search/exploration task and continues using child result
* parent prompt remains smaller than equivalent non-delegated run
* `parallelism: 1` still works deterministically
* no child tool chatter appears in parent transcript
* delegation activity is visible in the TUI without polluting the conversation pane

## Exit criteria

* delegation actually reduces context pressure

---

# Stage 10 - Hardening and ergonomics

## Objective

Make it pleasant enough to keep using.

## Packages / files

### `internal/provider/retry.go`

Implement:

* retry policy for transient failures

### `internal/config/validate.go`

Extend:

* deeper config validation
* conflicting settings detection

### `internal/output/`

Files:

* `events_jsonl.go`
* `pretty.go`

Implement:

* optional JSONL event log
* nicer terminal summaries

### `internal/git/`

Optional:

* `status.go`
* `diff.go`

Implement:

* repo detection
* changed files summary
* diff preview helpers

### `internal/tui/theme/`

Extend:

* additional themes: Dracula, Tokyo Night, Gruvbox Dark, Nord, One Dark, Kanagawa, Solarized Dark
* runtime theme switching via keybind

## Concrete work items

1. Add transient provider retry rules.
2. Improve config errors.
3. Add `--dry-run` if useful.
4. Add JSONL run event log.
5. Add better failure taxonomy.
6. Add optional git-aware summaries.
7. Tighten provider capability flags if needed.
8. Implement additional themes.
9. Implement runtime theme switching keybind.

## Tests

### Unit

* retry backoff and stop behaviour
* config conflict detection
* JSONL event emission
* git diff helper parsing
* each additional theme produces valid styles

### Integration

* simulated transient provider failures recover
* debug artifacts written correctly
* dry-run does not mutate files
* theme switching applies new palette to all TUI regions without restart

## Exit criteria

* repeated day-to-day usage no longer feels fragile

---

# Recommended implementation order inside each stage

## Stage 0

1. config structs
2. config loading
3. internal types
4. scheduler
5. CLI stubs
6. validation
7. tests

## Stage 1

1. provider client
2. core tools
3. tool executor
4. prompt assembly
5. agent loop
6. `--exec`
7. REPL
8. approvals
9. tests

## Stage 2

1. path policy
2. output caps
3. binary detection
4. `edit`
5. approval previews
6. tests

## Stage 3

1. context source budgets
2. retained state model
3. compaction logic
4. debug visibility
5. tests

## Stage 4

1. terminal input model
2. assistant/status rendering split
3. streaming-capable output path
4. approval/tool rendering
5. default theme
6. tests

## Stage 5

1. new event types and payload structs in `internal/output/log.go`
2. `EventSubscriber` interface in `internal/output/events.go`
3. plain renderer extracted from `stream.go` into `plain.go`
4. validate plain renderer against `--exec` baseline (golden test)
5. refactor agent loop to emit all output as events (no direct writes)
6. audit and remove direct writes from provider and tool executor
7. design and implement approval interaction contract
8. Bubble Tea model with event-to-msg bridge
9. content pane with plain text streaming and scroll
10. input area with command dispatch and approval mode
11. status bar
12. app entry point with mode selection
13. delete `internal/repl/`, remove go-readline-ny
14. enforce no-direct-writes rule (grep audit)
15. tests

## Stage 6

1. Glamour integration with hardcoded Catppuccin Mocha palette
2. streaming markdown block-boundary detection
3. completed block rendering through Glamour
4. in-progress block plain text display
5. muted tool execution blocks
6. muted thinking blocks
7. sidebar with live metrics
8. sidebar collapse/restore on resize and keybind
9. git branch/dirty state detection
10. hardcoded Lip Gloss styles for visual hierarchy
11. tests

## Stage 7

1. `Theme` interface and `Styles` struct
2. theme registry
3. Catppuccin Mocha implementation with `catppuccin/go`
4. refactor all hardcoded colours to theme-derived styles
5. theme loading from config
6. multi-line input editing
7. command history
8. readline keybindings
9. tab completion for commands and skills
10. paste handling
11. help overlay
12. verify theme swappability with test theme
13. tests

## Stage 8

1. contract structs
2. child state model
3. scheduler integration checks
4. delegation event types
5. TUI delegation event awareness
6. tests

## Stage 9

1. synchronous child execution
2. parent/child handoff
3. model-facing `spawn_agent`
4. TUI delegation rendering
5. visibility/events
6. tests

## Stage 10

1. retries
2. diagnostics/logging
3. config hardening
4. optional git helpers
5. additional themes
6. runtime theme switching
7. tests

---

# Package ownership / responsibility map

## `internal/config`

Only config loading, merge, validation, defaults.

## `internal/provider`

Only model transport, normalization, scheduling.

## `internal/agent`

Loop orchestration, state, limits, event emission helpers. No transport details. No rendering.

## `internal/tool`

Registry, schema, policy, execution, previews, output shaping.

## `internal/prompt`

Context gathering, budgeting, assembly, compaction.

## `internal/skill`

Skill discovery and loading only.

## `internal/output`

Event type definitions, event subscriber interface, plain renderer (`--exec` mode), shared formatting utilities. This package owns the event contract and the non-TUI rendering path.

## `internal/tui`

Bubble Tea application, TUI model, content pane, input area, sidebar, status bar, help overlay, keybindings, render utilities. Consumes events via `EventSubscriber`. Does not import `internal/agent` — receives all information through events.

## `internal/tui/theme`

Theme interface, theme registry, concrete theme implementations (Catppuccin Mocha initially). Does not import any other internal package.

## `internal/delegation`

Delegation contracts and execution scaffolding.

## `internal/repl` (deleted after stage 5)

Was: interactive UX. Replaced entirely by `internal/tui`.

That separation matters. If `internal/tui` imports `internal/agent` directly (not through events), that is a violation. If `internal/tui/theme` imports anything outside the standard library and its own dependencies, that is a violation. If `internal/agent` imports `internal/tui`, that is a catastrophic violation.

---

# Test strategy by layer

## Fast unit tests

Use heavily for:

* config
* schema generation
* policy checks
* compaction logic
* delegation contracts
* scheduler semantics
* event type construction and payload correctness
* theme palette validation and style derivation
* markdown block boundary detection
* input handler command dispatch and keybinding resolution
* sidebar formatting

## Integration tests with fake provider

Use for:

* agent loop
* tool calls
* approvals
* prompt assembly
* delegation flow
* event emission completeness (all expected events emitted for a given scenario)
* plain renderer output correctness

## Integration tests with programmatic TUI

Use for:

* Bubble Tea model updates via injected `tea.Msg` sequences
* event-to-msg bridge correctness
* content pane rendering under streamed events
* sidebar state updates from events
* approval flow through TUI
* layout reflow on resize
* help overlay toggle
* input history cycling
* tab completion

Do not attempt to test the TUI by capturing terminal output — test through the Bubble Tea model's `Update`/`View` methods with synthetic messages.

## Integration tests with temp repos

Use for:

* file reads/writes/edits
* glob/search
* bash execution
* path confinement
* repo-like workflows
* git branch/dirty state detection

## Golden tests

Use sparingly for:

* prompt assembly
* tool schema
* approval previews
* compacted context blocks
* plain renderer output for synthetic conversations
* rendered markdown blocks (Glamour output for known input)

## End-to-end smoke tests

Use for:

* `--exec` against a small fixture repo
* TUI startup and clean shutdown (may need `tea.TestModel` or equivalent)

---

# Minimal fixture repos to create

## `testdata/repos/go_tiny_bug/`

Small Go repo with:

* one bug
* one test
* README
* AGENTS.md optional

## `testdata/repos/multi_file_search/`

Repo for:

* glob/search usefulness
* no edits needed

## `testdata/repos/large_output/`

Repo or scripts for:

* truncation tests
* noisy shell commands

## `testdata/repos/delegation_fixture/`

Later, for:

* parent delegates exploration
* child returns compact result

## `testdata/tui/`

Synthetic event sequences for TUI testing:

* `streaming_conversation.json` — multi-turn conversation with interleaved streaming chunks, tool calls, thinking blocks, and context updates
* `approval_flow.json` — sequence requiring approval interaction
* `markdown_blocks.md` — known markdown input for Glamour rendering golden tests
* `sidebar_events.json` — sequence of context update and compaction events for sidebar state testing
