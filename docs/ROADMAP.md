# steiner implementation roadmap

Below is a staged implementation roadmap derived from the current PRD direction. Stages 0-4 are implemented and stable. The immediate priority is building a professional terminal interface (stages 5-7) before returning to delegation work (stages 8-9).

## Guiding rule

Every stage must improve one or more of these without materially harming the others:

* context cleanliness
* execution safety
* debuggability
* local-model operability
* console usability
* future delegation readiness

Sub-agents are not the next deliverable. A professional TUI is.

---

## Stage 0 - Foundations and architecture skeleton (complete)

### Goal

Create the project skeleton and harden the core interfaces before behaviour exists.

### Deliverables

* repo layout in place
* config loading with precedence
* provider interface
* tool registry types
* agent loop state types
* prompt/context assembly types
* logging setup
* runtime scheduler abstraction for LLM request parallelism
* basic CLI shell with `steiner`, `steiner --exec`, `steiner version`, `steiner config`

### Key design decisions locked

* exact config schema
* provider request lifecycle abstraction
* scheduler location and API
* tool contract shape
* event/log format
* path resolution model relative to project root

### Exit criteria (met)

* resolved config can be loaded, merged, and printed
* provider config accepts `parallelism`
* CLI boots and validates config without running an agent
* internal packages compile cleanly

---

## Stage 1 - Core single-agent loop (complete)

### Goal

Ship the thinnest useful agent that can do real work end-to-end.

### Deliverables

* OpenAI-compatible provider implementation
* single-agent ReAct loop
* REPL mode
* `--exec` mode
* core termination controls: max turns, model call cancellation, tool timeout handling
* minimal system preamble
* AGENTS.md loading
* bounded project context loading
* skills discovery and explicit invocation
* core tools: read, glob, search, write, bash
* approval system
* plain logging of model calls, tool calls, and stop reasons

### Exit criteria (met)

* can fix a small bug in a toy repo
* can read files, edit one file, run a targeted test, and explain result
* tool approvals behave correctly
* path handling is project-root-relative and deterministic
* local setup with `parallelism: 1` behaves predictably on constrained hardware

---

## Stage 2 - Execution safety and safer mutation (complete)

### Goal

Reduce collateral damage and tighten the runtime safety model.

### Deliverables

* path confinement enforcement
* blocked path rules
* writable path allowlist support
* explicit cwd handling for bash
* max tool output byte capture
* truncation markers for stdout/stderr
* binary output detection
* better approval previews
* `edit` primitive as exact old/new replacement

### Exit criteria (met)

* agent can modify files without relying solely on blind overwrite
* large command output no longer pollutes conversation naively
* dangerous paths are rejected by policy, not only by user judgment
* shell tool behaviour is inspectable and constrained

---

## Stage 3 - Context discipline and compaction (complete)

### Goal

Make long sessions viable.

### Deliverables

* context assembler with explicit source ordering
* rolling retention policy implementation
* tool-output summary envelopes
* conversation compaction mechanism
* preservation of active constraints across compaction
* optional prompt inspection/debug command
* explicit context diagnostics in logs: source budgets, retained turns, compacted segments, dropped/truncated material

### Exit criteria (met)

* long exploratory sessions remain coherent
* prompt size does not grow linearly with every tool call
* user can inspect when compaction happened
* critical decisions survive compaction

---

## Stage 4 - Session visibility and control (complete)

### Goal

Make long-running single-agent sessions understandable and controllable.

### Deliverables

* context budget visibility in the console
* clearer compaction visibility
* turn/session inspection improvements
* better stop-reason presentation
* improved cancellation and interruption UX
* richer REPL control surface where needed for usability
* clearer surfaced summaries of recent diagnostic events

### Exit criteria (met)

* users can tell when context was trimmed or compacted
* users can understand why a run stopped without reading logs
* long exploratory sessions are inspectable enough to remain usable
* console controls improve usability without changing core prompt semantics

---

## Stage 5 - Agent event interface and TUI foundation

### Goal

Replace the line-oriented REPL with a Bubble Tea TUI application, built on a clean event-driven boundary between the agent core and all rendering.

### Deliverables

#### Agent loop event interface

* structured event types emitted by the agent loop through a channel or callback interface
* required events: TurnStarted, StreamChunk, StreamComplete, ThinkingChunk, ThinkingComplete, ToolCallRequested, ApprovalRequired, ApprovalResult, ToolExecuting, ToolResult, ContextUpdate, LimitHit, SkillActivated, CompactionFired, RunComplete, Error
* all direct stdout/stderr writes removed from the agent loop, provider, and tool executor
* all diagnostic and log output routed exclusively to the log file

#### Plain renderer

* subscribes to the event interface
* writes formatted text to stdout/stderr for `--exec` mode
* preserves current single-shot behaviour exactly
* validates that the event interface is sufficient to reconstruct the current output

#### TUI application

* Bubble Tea application replaces the existing REPL for interactive mode
* two-region layout: scrollable content pane and distinct input area
* status bar at the bottom showing model name, turn count, basic context stats, keybind hints
* streaming assistant responses rendered as plain text into the content pane as chunks arrive
* existing REPL commands (`/help`, `/tools`, `/skills`, `/history`, `/clear`, `/exit`) working in the TUI input area
* terminal resize handling with responsive layout reflow
* scroll wheel support for the content pane, no other mouse interaction

### Scope constraints

* no markdown rendering yet - streamed text is plain
* no sidebar yet
* no theme system yet
* no multi-line input editing yet
* no command completion yet
* mouse support limited to scroll wheel only - no clickable elements

### Implementation notes

* the event interface is the most important deliverable in this stage - get it right before building the TUI on top of it
* build the plain renderer first and validate it against current `--exec` output before touching the TUI
* the Bubble Tea model should consume events via `tea.Cmd` functions that read from the event channel and emit `tea.Msg` updates
* under TUI mode, nothing in the codebase may write to stdout or stderr directly - this must be enforced, not merely conventional
* the existing REPL package (`internal/repl/`) is replaced by `internal/tui/` - do not try to wrap the old REPL inside Bubble Tea
* keep the Bubble Tea model minimal: content buffer, input buffer, status bar state, viewport dimensions
* Lip Gloss styling in this stage should be minimal and hardcoded - just enough to visually separate the regions

### Key design decisions to lock now

* event type definitions and channel/callback shape
* subscriber interface that both renderers implement
* how approval interaction works in the TUI (approval prompt takes over the input area, user confirms/denies, input area returns to normal)
* how the TUI handles the agent loop running on a background goroutine while Bubble Tea owns the main thread

### Exit criteria

* the interactive console is a functional Bubble Tea application that streams responses, handles input, and displays status
* `--exec` mode works identically to before via the plain renderer
* no stdout/stderr writes exist anywhere in the agent loop, provider, or tool packages
* the event interface cleanly separates the agent loop from all rendering concerns
* scroll wheel works in the content pane
* terminal resize reflows the layout correctly
* approval prompts work correctly in the TUI input area

### Risks to avoid

* designing the event interface around TUI assumptions rather than keeping it renderer-agnostic
* letting the TUI reach into agent state directly instead of consuming events
* building a partial event interface and keeping some direct writes "for now"
* overbuilding the TUI layout before the event plumbing is proven
* adding mouse click handling - scroll wheel only

---

## Stage 6 - Markdown rendering and status sidebar

### Goal

Make the TUI content pane render styled markdown and provide persistent session visibility through a status sidebar.

### Deliverables

#### Markdown rendering

* Glamour integration for rendering assistant responses in the content pane
* code blocks with syntax highlighting via Chroma
* full markdown support: headers, bold, italic, inline code, code blocks, lists, tables, links
* streaming markdown strategy: completed blocks rendered with full styling, in-progress block shown as plain text until the next block boundary is detected

#### Status sidebar

* right-side panel showing live session metrics
* contents: model name, provider endpoint, context tokens used/budget with percentage, current turn/max turns, compaction state (whether fired, turns affected), git branch and dirty state, working directory, active skills
* automatic collapse below the terminal width threshold configured in `tui.sidebar_min_width`
* manual toggle via keybind
* sidebar updates driven by ContextUpdate, CompactionFired, SkillActivated, and TurnStarted events

#### Visual hierarchy

* assistant prose rendered at full brightness as the primary content
* tool execution rendered as muted inline blocks in the content pane: tool name, key arguments, approval state, result summary, truncation status
* thinking blocks rendered as muted, visually de-emphasised regions in the content pane
* approval prompts rendered by highlighting the pending tool call and activating approval interaction in the input area
* clear visual boundary between the content pane and the sidebar

### Scope constraints

* no theme abstraction yet - use hardcoded Catppuccin Mocha palette values directly
* no input improvements yet
* no help overlay yet

### Implementation notes

* Glamour renders complete markdown strings, not incremental chunks - the streaming strategy must buffer incoming chunks and track block boundaries (blank lines, fence markers, header markers) to decide when a block is "complete" and can be rendered through Glamour
* the in-progress block should render as unstyled text appended below the last rendered block - when the block completes, replace the unstyled text with the Glamour-rendered version
* for the sidebar, derive git branch and dirty state at session start and on file-write tool results - do not shell out to git on every render cycle
* sidebar width should be fixed (not proportional) - something like 30-35 characters is enough for the metrics it displays
* tool execution blocks should be collapsible or at minimum compact - showing full tool output inline defeats the purpose of the muted hierarchy
* the Glamour style sheet in this stage can be the built-in `dracula` or `catppuccin` preset if one exists, or a custom JSON sheet using Catppuccin Mocha palette values - it does not need the theme abstraction yet

### Exit criteria

* assistant prose renders as styled markdown with syntax-highlighted code blocks
* tool and thinking content is visually subordinate to assistant prose
* the sidebar shows accurate live context and session metrics
* the sidebar collapses and restores correctly on terminal resize and keybind toggle
* streaming responses show styled completed blocks above and plain in-progress text below
* the visual hierarchy is immediately legible - a user can glance at the screen and distinguish prose from tool noise

### Risks to avoid

* rendering markdown on every incoming chunk (expensive and produces flickering partial renders)
* making the sidebar too wide or too information-dense
* making tool blocks so muted they become invisible when users need to debug tool behaviour
* coupling Glamour rendering to the streaming path instead of the block-completion path
* blocking the TUI render loop on Glamour processing

---

## Stage 7 - Theme system and input polish

### Goal

Extract the hardcoded colour palette into a swappable theme abstraction and bring the input area up to a professional standard.

### Deliverables

#### Theme system

* `Theme` interface defining a semantic colour palette: background, foreground, accent, muted, border, error, warning, success, syntax highlight groups
* theme registry for holding available themes by name
* startup theme loading from `tui.theme` config value
* theme produces both Lip Gloss styles (for TUI chrome) and a Glamour style sheet (for markdown content) from a single palette definition
* Catppuccin Mocha as the default and only shipped theme, using `catppuccin/go` for palette values
* verified that adding a new theme requires only defining a palette struct and registering it - no other code changes

#### Input area improvements

* multi-line editing support
* command history navigation via up/down
* readline-style keybindings: ctrl+a (start of line), ctrl+e (end of line), ctrl+k (kill to end), ctrl+w (delete word back), ctrl+u (kill line), and standard cursor movement
* slash-command and skill name completion (tab or similar trigger)
* paste handling for multi-line and code block content

#### Help overlay

* keybind reference panel toggled via keybind
* displays available keybindings grouped by function (navigation, input, session control, sidebar)
* lightweight overlay rendered within the TUI, not a separate screen or mode

### Scope constraints

* ship only Catppuccin Mocha - additional themes are deferred to stage 10
* no runtime theme switching via keybind - theme is set in config and applied at startup
* no vim-style modal editing

### Implementation notes

* the theme refactor should extract all hardcoded colour values from stages 5-6 into the theme interface - grep for hex values and Lip Gloss colour calls
* for Catppuccin Mocha, use `catppuccin/go` to get typed palette values rather than hardcoding hex strings
* the Glamour style sheet should be generated from the theme palette programmatically, not maintained as a separate JSON file - this ensures palette consistency when themes are swapped
* for input editing, consider Bubble Tea's `textarea` bubble or `textinput` bubble as a starting point - evaluate whether they handle multi-line, paste, and readline bindings well enough or whether a custom input model is needed
* tab completion should be contextual: after `/`, complete against known commands and skill names; otherwise no completion
* the help overlay can be a simple full-screen or half-screen Lip Gloss render toggled by a boolean in the TUI model state

### Exit criteria

* the TUI renders correctly under Catppuccin Mocha with consistent styling across chrome, markdown content, and syntax highlighting
* all colour values come from the theme interface, not hardcoded literals
* a second theme can be added by defining a palette struct and registering it, with no other code changes required
* multi-line input, command history, readline keybindings, and paste handling all work correctly
* tab completion suggests commands and skill names after `/`
* the help overlay displays and dismisses cleanly

### Risks to avoid

* making the theme interface too granular (dozens of semantic names nobody can keep track of) - 12-15 named colours is enough
* implementing a custom input model when Bubble Tea's builtins are sufficient
* spending too long on theme polish when only one theme ships
* letting the help overlay interfere with the input area or content pane event handling

---

## Stage 8 - Delegation scaffolding

### Goal

Build the seams for sub-agents without actually shipping full delegated execution yet.

### Deliverables

* delegation package
* explicit parent/subtask contract
* scoped context handoff builder
* structured sub-agent result envelope
* limit inheritance/override rules
* scheduler integration so all future agent activity respects provider parallelism
* delegation events integrated into the event interface (DelegationStarted, DelegationComplete, DelegationFailed)
* TUI awareness of delegation events for future rendering

### Contract should define

#### Parent sends

* task
* scoped context
* allowed tools
* limits
* optional artifact references

#### Sub-agent returns

* final answer
* compact summary
* status
* optional structured outputs
* optional touched file list

### Scope constraints

* no nested delegation
* no parallel delegation yet
* no full sub-agent transcript return
* no delegation-specific UI polish yet

### Exit criteria

* you can instantiate a sub-agent state object and execute its lifecycle behind an internal interface
* the main agent loop does not need redesign to call delegated runs later
* scheduler correctly gates all LLM calls through the global/provider parallelism limit
* delegation events flow through the event interface

### Risks to avoid

* leaking parent transcript wholesale
* reusing shared mutable state between parent and child
* encoding delegation as a hacky tool shortcut without a real contract

---

## Stage 9 - Sub-agent execution v1

### Goal

Ship the first real delegated execution path.

### Deliverables

* `spawn_agent` exposed internally and then to the model
* synchronous sub-agent execution only
* isolated sub-agent conversation history
* parent passes bounded context only
* result-only integration back to parent
* separate limits for sub-agent turns/tokens/runtime
* TUI indication when delegation occurs: muted inline block showing task sent, active spinner or indicator during execution, compact result display on completion

### Behavioural rules

* child cannot spawn another child
* child only sees explicitly passed context
* parent only receives compact child result payload
* child tool chatter never enters parent history verbatim

### Recommendation

Do not expose delegation to the model until:

* compaction works (stage 3 - done)
* truncation works (stage 2 - done)
* scheduler works (stage 0 - done)
* safer edits work (stage 2 - done)
* console visibility is strong enough to explain delegation clearly (stages 5-7)

### Exit criteria

* delegated search/exploration tasks reduce parent prompt growth
* parent can continue productively after delegated result returns
* local-model users with `parallelism: 1` still get deterministic behaviour
* no transcript leakage from child to parent unless explicitly enabled for debug
* delegation activity is visible in the TUI without polluting the conversation pane

### Risks to avoid

* returning too little from child and forcing repeated delegation
* returning too much and defeating isolation
* concurrency creeping in early

---

## Stage 10 - Hardening and ergonomics

### Goal

Make the agent reliable enough for repeated use on real repos.

### Deliverables

* improved error taxonomy
* retry policy for transient provider failures
* better tool-call validation
* better session diagnostics
* optional `--dry-run`
* optional prompt inspection command
* optional save transcript / JSONL event log
* improved config validation
* additional themes: Dracula, Tokyo Night, Gruvbox Dark, Nord, One Dark, Kanagawa, Solarized Dark
* runtime theme switching via keybind

### Nice additions here

* git-aware helper logic for diff display
* touched files summary
* better approval defaults per tool
* targeted model capability flags

### Exit criteria

* bad configs fail fast with useful messages
* users can debug why a run failed without digging through code
* real-repo behaviour is understandable, not opaque

---

## Stage 11 - Advanced capabilities

### Possible branches after core is solid

* parallel sub-agents
* native providers beyond openai_compat
* persistence
* sandboxed executors
* MCP support
* project-local skills
* hierarchical AGENTS.md
* richer edit primitives
* artifact/report generation

### Important constraint

Do not start this stage until the product is already good at:

* bounded context
* safe execution
* deterministic scheduling
* comprehensible failure modes
* professional terminal experience

---

# Cross-stage dependency graph

## Must happen before Stage 1

* config hierarchy
* provider abstraction
* scheduler abstraction
* state model

## Must happen before Stage 2

* working single-agent loop
* tool registry
* approval system

## Must happen before Stage 3

* stable prompt assembly
* stable tool output capture
* clear state model

## Must happen before Stage 4

* compaction strategy
* explicit context source ordering
* scheduler enforcement
* basic event/log model

## Must happen before Stage 5

* session visibility and control (stage 4)
* stable agent loop and tool execution
* stable context diagnostics and compaction
* clear stop-reason events

## Must happen before Stage 6

* functional TUI with streaming (stage 5)
* proven event interface with both renderers working
* approval interaction working in TUI input area

## Must happen before Stage 7

* markdown rendering and sidebar working (stage 6)
* all colour values identifiable for extraction into theme interface
* input area functional enough to validate improvements against

## Must happen before Stage 8

* professional TUI complete (stages 5-7)
* stable event interface ready to accept delegation event types
* clear context-diagnostic presentation in sidebar

## Must happen before Stage 9

* delegation contract and scaffolding (stage 8)
* isolated child state
* provider parallelism enforcement
* output truncation and compaction

## Must happen before Stage 10

* first delegated execution path (stage 9)
* useful delegated-event visibility in TUI

## Must happen before Stage 11

* bounded context
* safe execution
* deterministic scheduling
* comprehensible failure modes
* professional TUI
* working delegation

---

# Suggested milestone breakdown

## Milestone A (complete)

Stages 0-1
Outcome: usable minimal agent

## Milestone B (complete)

Stage 2
Outcome: safer editing and safer execution

## Milestone C (complete)

Stage 3
Outcome: long-session viability

## Milestone D (complete)

Stage 4
Outcome: session visibility and control

## Milestone E

Stage 5
Outcome: event-driven architecture and functional TUI with streaming

## Milestone F

Stage 6
Outcome: styled markdown rendering and live session sidebar

## Milestone G

Stage 7
Outcome: theme abstraction and professional input handling

## Milestone H

Stages 8-9
Outcome: delegated execution without context pollution

## Milestone I

Stages 10-11
Outcome: robustness, polish, and advanced features

---
