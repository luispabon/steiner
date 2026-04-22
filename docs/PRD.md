# steiner - Product Requirements Document

**Version:** 0.4.0
**Status:** Draft
**Date:** 2026-04-22

This revision restructures delivery staging around the current priority: building a full terminal user interface before returning to delegation work. Stages 0-4 are implemented and stable. The new stages 5-7 define the TUI build. Delegation moves to stages 8-9.

---

## 1. Overview

steiner is a minimal, local-first coding agent written in Go. It accepts a user task, reasons about it using an LLM, executes tool calls against the local filesystem and shell, and iterates until the task is complete.

steiner is designed for real coding work over sessions that may become long, exploratory, or multi-step. Its central product concerns are disciplined context management over time and a terminal experience that keeps the agent understandable, controllable, and usable during real work.

steiner is not a framework. It is a single opinionated binary with sensible defaults, a plugin-first tool model, a skills system for explicit context injection, and an architecture that can later support delegated work through isolated sub-agents.

Sub-agents remain part of the long-term product direction because context isolation matters. They are not the immediate product focus. The immediate priority is a professional-grade terminal interface built on the context-discipline foundation already in place.

---

## 2. Product Goals and Non-Goals

### 2.1 Goals

steiner should:

* complete real coding tasks through iterative LLM reasoning and tool use
* remain usable over long sessions without uncontrolled context growth
* keep prompt construction deliberate, inspectable, and bounded
* provide a professional terminal interface that makes agent activity understandable and controllable
* allow users to inject reusable context explicitly rather than implicitly
* remain easy to install and run as a single statically-linked Go binary
* work with both local and remote OpenAI-compatible endpoints
* operate well on resource-constrained hardware, including systems hosting local LLMs
* preserve a clean path to future delegated work through isolated sub-agents

### 2.2 Non-Goals

steiner is not initially intended to be:

* a general-purpose agent framework
* a web application or hosted service
* a sandboxed execution platform in early stages
* a persistence-heavy assistant with durable memory in early stages
* a full MCP client in early stages
* a multi-user orchestration platform
* an autonomous background worker

---

## 3. Core Product Principles

### 3.1 Minimal Prompting

steiner should inject only the minimum instruction required to establish operating rules, context sources, and tool availability. The model's native capabilities should do the heavy lifting.

### 3.2 Local-First Operation

steiner should work well with local LLMs, local projects, and local tool execution. Remote APIs are supported, but local operation is a primary use case.

### 3.3 Context Cleanliness Over Convenience

The system must prefer bounded, explicit, and inspectable context over convenience features that silently bloat prompt state. Long-lived usefulness depends on this.

### 3.4 Plugin-First Tooling

Core tools and user-defined tools should share the same registration and execution model wherever practical. The system should not grow separate tool worlds for built-in and custom behaviour.

### 3.5 Safe-by-Default Execution

Direct execution is acceptable in early stages, but the execution model must still support confinement, approvals, output limits, and future sandboxing.

### 3.6 Provider Abstraction

LLM access should be abstracted from the beginning so the agent loop does not depend on a single provider implementation.

### 3.7 User-Driven Context

Skills and other optional context must never be silently surfaced to the model. The user should decide what additional context enters the run.

---

## 4. Context Architecture

Context management is a first-class architectural concern. steiner's quality over time depends on how well it controls what enters context, how long it remains there, how it is compacted, and how future delegated work stays isolated.

### 4.1 Context Sources

steiner may construct model context from the following sources:

1. fixed system preamble
2. global AGENTS.md conventions
3. project AGENTS.md conventions
4. auto-discovered project context
5. user-invoked skills
6. active conversation history
7. tool results
8. delegated sub-agent return payloads

Not all sources are always present.

### 4.2 Context Precedence

The system should apply a clear precedence model:

1. fixed system preamble
2. global AGENTS.md
3. project AGENTS.md
4. auto-discovered project context
5. user-invoked skills
6. active conversation state
7. tool results
8. delegated sub-agent results

This precedence governs conflict resolution conceptually. It does not require that all sources be encoded as the same message role.

### 4.3 Context Encoding Strategy

steiner should not treat every context source as an equivalent high-authority system instruction.

Recommended encoding model:

* fixed system preamble as the primary system message
* AGENTS.md content included as convention blocks beneath the system contract
* project context included as bounded reference context
* skills injected as explicit auxiliary context, not as peer system authority
* tool results appended as structured outputs
* delegated sub-agent results appended as compact summaries or structured returns

This prevents accidental over-authority of optional or user-added context.

### 4.4 Context Retention Policy

The system must define what is retained, compacted, or discarded.

Policy goals:

* recent turns remain verbatim
* large tool output is truncated and summarised
* stale exploratory noise does not remain indefinitely
* critical constraints and active decisions remain visible
* parent agents do not inherit full delegated transcripts by default

At minimum, steiner should support these policies conceptually from the beginning:

* retain recent user/assistant/tool turns verbatim
* enforce per-tool output byte limits
* replace oversized outputs with a summary envelope plus truncation notice
* bound auto-discovered project context by a configurable budget
* support rolling compaction of older conversation history
* keep sub-agent internals isolated from parent context unless explicitly requested

### 4.5 Context Boundaries

Context boundaries must be explicit.

* the main agent has its own working history
* each sub-agent has a separate working history
* tool output is not automatically important merely because it exists
* user-invoked skills remain explicit and inspectable
* project context is reference material, not permanent high-priority instruction
* delegated work returns compact results, not raw transcripts

### 4.6 Context Failure Modes

The design should explicitly guard against:

* prompt bloat from large tool output
* stale README or project metadata dominating context
* conflicting instructions across AGENTS.md and skills
* conversation drift during long sessions
* parent context pollution from delegated work
* repeated reinjection of low-value context
* degraded responsiveness from overly large prompts on local models

---

## 5. User Experience Model

### 5.1 Modes

steiner supports two primary operating modes:

* **interactive mode** - full TUI session in the current project
* **single-shot mode** - one task executed via `--exec`, plain stdout output

Single-shot mode must remain a simple stdout/stderr writer. It must not launch the TUI. This preserves piping, scripting, and CI usage.

### 5.2 User Controls

The user should be able to:

* start a session in a project directory
* run a one-shot task
* inspect available tools
* inspect available skills
* activate a skill explicitly
* clear the current conversation
* approve or deny tool calls
* override selected configuration values for the current session
* see why a run stopped
* inspect context budget and compaction state
* toggle the status sidebar
* access a keybind reference overlay
* later, inspect when delegation occurred

### 5.3 Visibility and Trust

The terminal interface should surface the information needed to keep the agent understandable:

* tool name and relevant arguments before execution
* approval prompts for risky tools
* truncation notices when output is reduced
* skill activation when skills are injected
* current turn and budget usage in the status sidebar
* clear stop reasons when limits are hit
* later, clear indication when a sub-agent has been used

### 5.4 Rendering Hierarchy

The TUI must establish a clear visual hierarchy for different content types:

* **assistant prose** renders at full brightness as the primary content the user needs to read
* **thinking blocks** render muted, visually subordinate to prose output
* **tool execution blocks** render muted and inline in the conversation flow, showing tool name, key arguments, approval state, result summary, and truncation status
* **status and diagnostic events** render in the sidebar or status bar, not inline with conversation content

This hierarchy ensures that model output intended for the user is never visually competing with internal reasoning or tool machinery.

### 5.5 Console Layout

The interactive TUI uses a persistent multi-pane layout:

**Content pane** (primary, left): scrollable area showing the conversation. Streamed assistant responses, tool execution blocks, and rendered markdown all appear here. Takes the majority of terminal width.

**Status sidebar** (right, collapsible): persistent panel showing live session metrics. Collapses automatically below a configurable terminal width threshold. Toggles via keybind. Contents include: model name, provider endpoint, context tokens used/budget with percentage, current turn/max turns, compaction state, git branch and dirty state, working directory, active skills, session timing.

**Input area** (bottom of content pane): visually distinct prompt region with cursor. Supports multi-line editing, command history, readline keybindings, slash-command completion, and paste handling.

**Status bar** (bottom of terminal): model name, build/version info, context summary, keybind hints.

### 5.6 Resource-Constrained Operation

Users running local models on limited hardware must be able to reduce runtime pressure deliberately. steiner should expose provider/model parallelism controls so users can limit the number of concurrent in-flight LLM requests.

This is especially important once delegated execution exists, but the control belongs in the provider configuration from the start.

---

## 6. Agent Loop Event Interface

### 6.1 Purpose

The agent loop must emit structured events rather than writing directly to any output target. This is the critical architectural seam that enables the TUI, preserves plain `--exec` output, and supports future rendering targets without modifying the core loop.

### 6.2 Event Model

The agent loop should emit events through a channel or callback interface. Each event carries enough information for any renderer to present it appropriately.

Required event types:

* **TurnStarted** - new turn beginning, includes turn number
* **StreamChunk** - incremental text from a streaming response
* **StreamComplete** - streaming response finished
* **ThinkingChunk** - incremental thinking/reasoning content (if supported by provider)
* **ThinkingComplete** - thinking block finished
* **ToolCallRequested** - tool call pending, includes tool name, arguments, approval mode
* **ApprovalRequired** - tool call awaiting user approval
* **ApprovalResult** - user approved or denied
* **ToolExecuting** - tool execution in progress
* **ToolResult** - tool execution complete, includes output, truncation status, timing
* **ContextUpdate** - context budget changed, includes tokens used/budget, compaction state
* **LimitHit** - a termination control fired, includes which limit and current values
* **SkillActivated** - a skill was toggled on
* **CompactionFired** - conversation history was compacted, includes turns affected
* **RunComplete** - agent loop finished, includes stop reason
* **Error** - error occurred, includes context

### 6.3 Rendering Subscribers

Two rendering subscribers must coexist:

* **TUI renderer** - consumes events and updates the Bubble Tea model, drives the full interactive interface
* **Plain renderer** - consumes events and writes formatted text to stdout/stderr for `--exec` mode

Both renderers implement the same subscriber interface. The agent loop does not know which renderer is active.

### 6.4 Logging Under TUI

Once the TUI owns the terminal, nothing may write directly to stdout or stderr. All diagnostic and log output must route exclusively to the log file. The TUI subscribes to whichever events it wants to surface in the sidebar or as inline status. The existing `--log-file` path handles this, but the constraint must be explicit: the agent loop, provider, tool executor, and all supporting packages must never write to stdout/stderr directly. All output goes through the event interface or the logger.

---

## 7. TUI Technology Stack

### 7.1 Framework

The TUI is built on the Charm stack:

* **Bubble Tea** (charmbracelet/bubbletea) - terminal application framework, manages layout, input, events, and rendering
* **Lip Gloss** (charmbracelet/lipgloss) - declarative styling for TUI chrome (borders, panels, status bar, prompt)
* **Glamour** (charmbracelet/glamour) - markdown rendering with syntax-highlighted code blocks via Chroma

These libraries are mature, well-maintained, designed to compose, and widely used in the Go TUI ecosystem.

### 7.2 Terminal Interaction Model

**Keyboard-first.** All functionality is accessible via keyboard. The TUI is designed for developers working in a terminal.

**Minimal mouse support.** Scroll wheel for the content pane. No clickable buttons, no mouse-driven UI controls. Mouse interaction beyond scrolling is explicitly out of scope - it conflicts with terminal selection behaviour and is a source of friction in TUI tools that overreach.

**Resize handling.** Bubble Tea emits `tea.WindowSizeMsg` on terminal resize. The layout must reflow responsively: the sidebar collapses below a configurable width threshold, the content pane takes full width when the sidebar is hidden.

### 7.3 Theme System

The theme system must support swappable colour palettes with a clean abstraction layer.

**Architecture:**

A `Theme` interface defines a palette of semantically named colours (background, foreground, accent, muted, border, error, warning, success, and syntax highlight groups). Each concrete theme implements this interface as a data struct mapping semantic names to hex colour values.

A theme registry holds available themes. The active theme is selected via configuration. At startup, the TUI loads the active theme and derives both Lip Gloss styles (for chrome) and a Glamour style sheet (for markdown content) from the same palette.

**Shipped themes:**

* Catppuccin Mocha (default) - using the official `catppuccin/go` package for palette values

The theme abstraction must be ready to accept additional themes (Dracula, Tokyo Night, Gruvbox Dark, Nord, One Dark, Kanagawa, Solarized Dark) with no architectural changes - adding a new theme should require only defining a new palette struct and registering it. Additional themes are deferred.

**Configuration:**

```yaml
tui:
  theme: catppuccin-mocha
  sidebar_min_width: 120
```

---

## 8. CLI and Interaction Surface

### 8.1 Commands

Current core surface:

```text
steiner
steiner --exec "task"
steiner --config path/to/config.yaml
steiner --model model-name
steiner --log-file path/to/session.log
steiner version
steiner tools
steiner skills
steiner config
```

### 8.2 REPL Commands

Built-in commands available in the TUI input area:

```text
/help
/tools
/skills
/history
/clear
/exit
```

Skill invocation uses the same `/name` namespace, with built-in commands reserved.

### 8.3 Input Behaviour

The TUI input area should support:

* multi-line editing
* command history navigation (up/down)
* readline-style keybindings (ctrl+a, ctrl+e, ctrl+k, ctrl+w, etc.)
* slash-command and skill name completion
* paste handling for code blocks and multi-line content

### 8.4 Output Behaviour

The TUI content pane should:

* stream assistant responses incrementally as chunks arrive
* render completed markdown blocks with full styling (headers, bold, italic, code blocks with syntax highlighting, lists, tables)
* show in-progress streaming text as plain text until block boundaries are reached
* render tool execution as muted inline blocks showing tool name, key arguments, approval state, result summary, and truncation status
* render thinking blocks as muted collapsible or de-emphasised regions
* present approval prompts by temporarily highlighting the pending tool call and activating the approval interaction in the input area

### 8.5 Help Overlay

A keybind reference panel toggled via `?` or `ctrl+?` should display available keybindings and their functions. This is a lightweight overlay rendered within the TUI, not a separate screen.

---

## 9. Functional Architecture

### 9.1 Core Agent Loop

The base agent loop follows a ReAct-style cycle:

1. assemble prompt input from configured context sources
2. call the LLM
3. if the response contains tool calls:

   * validate and approve as required
   * execute each tool call
   * append tool results
   * emit appropriate events
   * continue
4. if the response is textual only:

   * emit the response
   * in interactive mode, wait for further input
   * in single-shot mode, exit

The loop terminates when the model produces a final text response or when a termination control fires.

The loop must emit all activity as structured events through the event interface defined in section 6. It must never write directly to stdout, stderr, or any rendering target.

### 9.2 Provider Abstraction Layer

All model communication should go through an abstract provider interface.

```go
type Provider interface {
    ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    StreamChatCompletion(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error)
    SupportsUsageStats() bool
}
```

v1 ships with `openai_compat` and may support additional steiner-owned provider adapters without changing the main agent loop.

### 9.3 Provider Runtime Parallelism

Provider configuration must support a `parallelism` setting that limits how many LLM requests steiner may have in flight at once for that provider/model configuration.

This is a runtime concurrency guard, not a sampling parameter.

Purpose:

* prevent overload on resource-constrained local machines
* avoid excessive contention on small GPUs or integrated graphics
* cap concurrent parent/sub-agent model calls once delegation exists
* give users explicit control over responsiveness vs throughput

Semantics:

* default should be conservative
* `parallelism: 1` means only one LLM request may be active at a time
* higher values permit more concurrent model requests
* the scheduler must respect this limit across all agent activity using that configured provider instance

### 9.4 Tool System

#### 9.4.1 Tool Model

steiner exposes tools to the LLM through structured function schemas derived from config.

Core tools ship with the project, but user-defined tools should fit the same registration model wherever possible.

#### 9.4.2 Tool Registration

Tools are declared in YAML config with:

* name
* executable path
* optional subcommand
* description
* parameter schema
* timeout
* approval mode
* optional execution constraints

#### 9.4.3 Tool Execution Contract

The tool contract must be explicit and consistent.

Primary contract:

* structured JSON input
* structured JSON output
* non-zero exit codes treated as errors

If simple script-style tooling is to be supported, that should be an explicit adapter mode rather than an implicit contradiction of the JSON contract.

#### 9.4.4 Core Tools

The core tool set should remain intentionally small:

* `read`
* `glob`
* `search`
* `write`
* `bash`
* `edit`

`edit` is the preferred mutation primitive for in-place changes. `write` remains available for whole-file replacement where appropriate.

#### 9.4.5 Tool Output Policy

Tool output must be bounded.

The execution layer should support:

* max stdout/stderr bytes captured
* truncation markers
* summary envelopes for large results
* binary output detection
* clear error propagation

This is required for context hygiene.

### 9.5 Editing Model

File mutation deserves its own design treatment.

The product should prefer safer edit primitives such as:

* exact replace
* patch application
* append
* structured file edits where appropriate

Full-file overwrite is acceptable as a compatibility primitive, but should not remain the default mutation path for reliable coding tasks.

### 9.6 Execution Safety Model

Even before sandboxing, steiner should define execution safety rules.

The execution layer should support:

* project-root-based path resolution
* path confinement by default
* configurable writable path rules
* timeout enforcement
* cancellation propagation
* optional restrictions on dangerous paths
* explicit working directory handling for shell commands

This layer should be abstract enough to permit future containerised execution without redesigning the agent loop.

---

## 10. Skills and Convention System

### 10.1 AGENTS.md Conventions

steiner should support two optional convention sources:

1. global: `~/.config/steiner/AGENTS.md`
2. project: `./AGENTS.md`

Both may be included in the prompt assembly, with global conventions preceding project conventions.

### 10.2 Skills

Skills are reusable context snippets that the user explicitly injects.

Global skill location:

```text
~/.config/steiner/skills/
  skill-name/
    SKILL.md
```

Each skill is identified by its directory name and loaded only when explicitly invoked.

### 10.3 Skill Invocation

In interactive mode:

```text
/skill-name
```

In single-shot mode, skills should be explicitly selectable by CLI/config surface when exposed there.

### 10.4 Skill Authority

Skills must not be treated as peer system authority. They are explicit contextual assistance blocks added below the fixed system contract and conventions hierarchy.

This is important to avoid accidental prompt corruption through optional user-provided material.

---

## 11. Project Context Discovery

steiner may auto-discover bounded project context to help initial grounding.

### 11.1 Sources

Suggested discovery categories:

| Category               | Candidate files                                                      |
| ---------------------- | -------------------------------------------------------------------- |
| Project description    | `README.md`, `README`, `README.txt`                                  |
| Build/runtime metadata | `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `Makefile` |

### 11.2 Behaviour

Project context should be:

* discovered automatically
* bounded by a configurable budget
* truncated or excerpted when large
* treated as reference context rather than permanent instruction
* overridable through config

### 11.3 Configuration

The project context system should support:

* max budget
* extra files
* ignore files
* future per-category policies

---

## 12. Configuration Model

### 12.1 Hierarchy

Configuration should resolve in this order:

1. compiled defaults
2. global config - `~/.config/steiner/config.yaml`
3. project config - `.steiner/config.yaml`
4. environment variables
5. CLI flags

Later layers override earlier ones.

### 12.2 Full Config Schema

```yaml
provider:
  type: openai_compat
  base_url: ${STEINER_BASE_URL:-http://localhost:11434/v1}
  api_key: ${STEINER_API_KEY}
  model: ${STEINER_MODEL:-qwen3-35b-a3b}
  temperature: 0.2
  max_completion_tokens: 8192
  parallelism: 1

limits:
  max_turns: 50
  max_tokens: 500000
  tool_timeout_default: 30s
  tool_timeouts:
    bash: 120s
    read: 5s
    write: 5s
    edit: 5s
  tool_output_max_bytes: 65536

approval:
  default: prompt
  overrides:
    read: auto
    glob: auto
    search: auto
    write: prompt
    bash: prompt
    edit: prompt

sub_agent:
  enabled: false
  max_turns: 15
  max_tokens: 100000
  allowed_tools: [read, glob, search, write, bash, edit]
  allow_nesting: false
  max_concurrent: 1

tools: {}

project_context:
  max_tokens: 2000
  extra_files: []
  ignore_files: []

paths:
  project_root_only: true
  writable_paths: []
  blocked_paths: []

tui:
  theme: catppuccin-mocha
  sidebar_min_width: 120

logging:
  level: info
  file: ~/.local/share/steiner/steiner.log
```

### 12.3 Parallelism Configuration

`provider.parallelism` sets the maximum number of simultaneous LLM requests steiner may have in flight for that configured provider/model.

Examples:

* `1` - safest for constrained local setups
* `2` - permits limited concurrency
* `N` - upper concurrency bound for all active agent work using that provider

This is especially relevant for future delegation and should be respected by any request scheduler.

### 12.4 Environment Variable Mapping

Environment variables use the `STEINER_` prefix.

Examples:

| Variable                       | Config path            |
| ------------------------------ | ---------------------- |
| `STEINER_API_KEY`              | `provider.api_key`     |
| `STEINER_BASE_URL`             | `provider.base_url`    |
| `STEINER_MODEL`                | `provider.model`       |
| `STEINER_PROVIDER_PARALLELISM` | `provider.parallelism` |
| `STEINER_MAX_TURNS`            | `limits.max_turns`     |
| `STEINER_LOG_LEVEL`            | `logging.level`        |

### 12.5 Runtime-Important Config

The most operationally significant config includes:

* provider type, model, and base URL
* provider parallelism
* max turns and token budgets
* tool approvals
* tool timeouts
* tool output size limits
* project context budget
* writable path rules
* TUI theme and sidebar threshold
* sub-agent limits

---

## 13. Termination, Cancellation, and Recovery

### 13.1 Termination Controls

Independent controls may stop a run:

* max turns
* cumulative token budget
* tool timeouts

### 13.2 Cancellation

The design should support:

* cancelling the current model call
* cancelling the current tool execution
* aborting the current run
* reporting cancellation clearly via the event interface
* preserving deterministic session state after interruption

### 13.3 Recovery Behaviour

On failure, steiner should surface actionable information via events:

* which limit or error fired
* which tool or provider call failed
* what partial work was completed
* whether the session may continue

---

## 14. Delegation Architecture

Delegation exists to support context isolation, not novelty.

### 14.1 Purpose of Delegation

Sub-agents exist to:

* isolate exploratory or bulky work
* keep parent context compact
* allow bounded subtask execution with separate limits
* return concise results to the parent agent

### 14.2 Sub-Agent Model

A sub-agent is an isolated agent loop instance with:

* its own empty conversation history
* its own task prompt
* its own limits
* an allowed tool subset
* no automatic access to the parent transcript beyond what is explicitly passed

The parent agent should only receive the sub-agent's final result payload unless more detail is explicitly requested.

### 14.3 Parent/Sub-Agent Contract

A delegation contract should define what the parent sends:

* task
* scoped context
* allowed tools
* limits
* optional execution metadata

And what the sub-agent returns:

* final answer
* optional compact summary
* optional artifact references
* status metadata

It should not return its full internal transcript by default.

### 14.4 Isolation Rules

* sub-agents do not inherit full parent history
* parent agents do not ingest sub-agent tool chatter
* sub-agents cannot nest by default
* delegated work is bounded by separate turn/token/runtime limits
* provider parallelism limits still apply globally

### 14.5 Deferred Delegation Features

These are intentionally deferred:

* nested sub-agents
* parallel sub-agents
* shared memory between agents
* parent inspection of full sub-agent transcript by default
* complex delegation graphs

Sub-agents are a core architectural requirement, but not an immediate implementation requirement.

---

## 15. Roadmap-Aligned Delivery Stages

This section describes capability sequencing, not detailed project planning. Stages 0-4 are the implemented foundation and are stable. Stages 5-7 define the TUI build. Stages 8-9 cover delegation. Stage 10 covers future extensions.

### Stage 0 - Foundations Skeleton (complete)

Deliver:

* config loading and validation
* provider abstraction and scheduler
* core state types
* CLI skeleton and package boundaries

Exit condition:

* the architecture is ready for a single-agent loop without violating package boundaries or concurrency constraints

### Stage 1 - Core Single-Agent Execution (complete)

Deliver:

* REPL mode
* single-shot `--exec`
* OpenAI-compatible provider
* provider/model configuration including `parallelism`
* core prompt assembly
* bounded project context injection
* AGENTS.md loading
* skill discovery and explicit invocation
* core tools: read, glob, search, write, bash
* approval prompts
* timeouts and max-turn limits
* basic logging

Exit condition:

* the agent can complete a small multi-step coding task end-to-end with explicit tool use and bounded context assembly

### Stage 2 - Safer Mutation and Context Discipline (complete)

Deliver:

* improved edit primitive beyond full overwrite
* tool output truncation rules
* binary output handling
* clearer context retention rules in implementation
* better approval previews
* cancellation improvements
* stronger path confinement

Exit condition:

* the agent remains usable during longer sessions without uncontrolled prompt growth from tool chatter

### Stage 3 - Context Compaction Foundations (complete)

Deliver:

* rolling conversation compaction
* summarised retention of older turns
* preservation of active constraints and recent work
* compacted prompt assembly diagnostics

Exit condition:

* long sessions remain coherent without naive full-history replay

### Stage 4 - Session Visibility and Control (complete)

Deliver:

* context budget diagnostics surfaced to the user
* session and turn inspection
* basic cancellation and interruption handling
* REPL control surface for session management

Exit condition:

* users can understand what the agent is doing, why context was trimmed, and how to intervene in a running session

### Stage 5 - Agent Event Interface and TUI Foundation

Deliver:

* agent loop event interface as defined in section 6, replacing all direct stdout/stderr writes from the agent loop, provider, and tool executor
* plain renderer subscribing to events for `--exec` mode, preserving current single-shot behaviour
* TUI renderer subscribing to events for interactive mode
* Bubble Tea application with two-region layout: scrollable content pane and distinct input area
* status bar at the bottom of the terminal showing model name, turn count, basic context stats, and keybind hints
* streaming assistant responses rendered as plain text into the content pane as chunks arrive
* existing REPL commands (`/help`, `/tools`, `/skills`, `/history`, `/clear`, `/exit`) working in the TUI input area
* terminal resize handling with responsive layout reflow
* scroll wheel support for the content pane, no other mouse interaction
* all logging routed exclusively to the log file under TUI mode

Exit condition:

* the interactive console is a functional Bubble Tea application that streams responses, handles input, and displays status, without any direct stdout writes from the agent core
* single-shot `--exec` mode works identically to before via the plain renderer
* the event interface cleanly separates the agent loop from all rendering concerns

### Stage 6 - Markdown Rendering and Status Sidebar

Deliver:

* Glamour integration for rendering assistant responses in the content pane
* code blocks with syntax highlighting via Chroma
* full markdown support: headers, bold, italic, inline code, code blocks, lists, tables, links
* streaming markdown rendering strategy: completed blocks rendered with full styling, in-progress block shown as plain text until the next block boundary
* status sidebar showing: model name, provider endpoint, context tokens used/budget with percentage, current turn/max turns, compaction state (whether fired, turns affected), git branch and dirty state, working directory, active skills
* sidebar collapses automatically below the terminal width threshold configured in `tui.sidebar_min_width`
* sidebar toggles via keybind
* tool execution rendered as muted inline blocks in the content pane: tool name, key arguments, approval state, result summary, truncation status
* thinking blocks rendered as muted, visually de-emphasised regions in the content pane
* approval prompts rendered by highlighting the pending tool call and activating approval interaction in the input area

Exit condition:

* assistant prose is rendered as styled markdown with syntax-highlighted code blocks
* tool and thinking content is visually subordinate to assistant prose
* the status sidebar provides live context and session metrics without cluttering the conversation
* the sidebar responds correctly to terminal width changes and manual toggling

### Stage 7 - Theme System and Input Polish

Deliver:

* `Theme` interface defining a semantic colour palette (background, foreground, accent, muted, border, error, warning, success, syntax highlight groups)
* theme registry for holding available themes
* startup theme loading from config, producing both Lip Gloss styles and a Glamour style sheet from the active palette
* Catppuccin Mocha as the default and initially only shipped theme, using the `catppuccin/go` package
* theme abstraction verified as ready for additional themes with no structural changes (adding a theme requires only a new palette definition and registry entry)
* input area: multi-line editing support
* input area: command history navigation via up/down
* input area: readline-style keybindings (ctrl+a, ctrl+e, ctrl+k, ctrl+w, etc.)
* input area: slash-command and skill name completion
* input area: paste handling for multi-line and code block content
* help overlay toggled via keybind, displaying available keybindings and their functions

Exit condition:

* the TUI renders correctly under the Catppuccin Mocha theme with consistent styling across chrome, markdown, and syntax highlighting
* a second theme can be added by defining a palette struct and registering it, with no other code changes
* the input area behaves like a competent text editor for prompt entry
* new users can discover available keybindings through the help overlay

### Stage 8 - Delegation Foundations

Deliver:

* internal task handoff contract
* structured sub-agent result envelope
* isolated execution scaffolding
* scheduler that respects provider parallelism across all agent activity
* delegation events integrated into the event interface

Exit condition:

* the system is architecturally ready for sub-agent execution without redesigning the main loop or the TUI

### Stage 9 - Sub-Agent Execution

Deliver:

* synchronous sub-agent spawning
* isolated sub-agent histories
* bounded tool subsets for delegated runs
* result-only return to parent
* separate delegation limits
* TUI indication when a sub-agent is active and when results are returned

Exit condition:

* delegated work reduces parent context growth rather than increasing it
* sub-agent activity is visible in the TUI without polluting the conversation pane

### Stage 10 - Advanced Extensions

Potential later work:

* parallel sub-agents
* persistence
* sandboxed executors
* MCP support
* richer tool ecosystems
* more native provider implementations
* additional themes (Dracula, Tokyo Night, Gruvbox Dark, Nord, One Dark, Kanagawa, Solarized Dark)
* runtime theme switching via keybind

---

## 16. Success Criteria

steiner is successful when it demonstrates the following behaviours:

1. it can complete a small coding task end-to-end using tool calls
2. it can operate against both local and remote OpenAI-compatible backends
3. optional context sources enter the prompt only when explicitly or deterministically intended
4. large tool output does not accumulate unboundedly in model context
5. risky operations are visible and controllable through approvals
6. path and execution rules prevent obvious unsafe behaviour by default
7. configuration precedence resolves deterministically
8. local-model users can constrain concurrency through provider parallelism settings
9. long sessions remain usable through bounded context assembly and compaction
10. the terminal interface renders streamed responses with styled markdown and syntax-highlighted code
11. tool and thinking activity is visually subordinate to assistant prose
12. context budget, session state, and agent activity are visible in the status sidebar
13. the agent loop emits structured events consumed by independent renderers without coupling
14. single-shot mode produces clean stdout output without launching the TUI
15. delegated work, once implemented, returns compact results without polluting parent history
16. failures and limits are surfaced clearly enough for the user to understand what happened
17. user-defined tools can be added without changing core steiner code

---

## 17. Deferred / Explicitly Out of Scope

These are deliberately deferred:

* container-based sandbox execution
* full MCP client support
* non-OpenAI native provider implementations
* conversation persistence
* cost reporting beyond immediate run data
* nested sub-agents
* parallel sub-agents
* shared memory between agents
* project-local skills
* hierarchical subdirectory AGENTS.md discovery
* web or GUI interface
* atomic multi-file transaction support
* additional colour themes beyond Catppuccin Mocha
* runtime theme switching
* clickable mouse UI elements

The architecture should leave room for these later, but they are not required for early delivery.

---

## 18. Suggested Project Structure

```text
steiner/
  cmd/
    steiner/
      main.go
    steiner-core-tools/
      main.go
      read.go
      write.go
      bash.go
      glob.go
      search.go
      edit.go
  internal/
    agent/
      loop.go
      state.go
      limits.go
      events.go
    provider/
      interface.go
      openai_compat.go
      scheduler.go
    tool/
      executor.go
      registry.go
      schema.go
      output.go
    config/
      config.go
      defaults.go
    prompt/
      system.go
      context.go
      agents.go
      skills.go
      compaction.go
    skill/
      loader.go
    tui/
      app.go
      model.go
      content.go
      input.go
      sidebar.go
      statusbar.go
      help.go
      render.go
    tui/theme/
      theme.go
      registry.go
      catppuccin.go
    output/
      plain.go
      events.go
    delegation/
      contract.go
      scaffold.go
  testdata/
    repos/
  go.mod
  go.sum
  README.md
```

---

## 19. Notes on v1 Discipline

The long-term product direction still includes delegated execution, but the near-term product must deliver a professional terminal experience first.

The next shipped stages should optimise for:

* a clean event-driven boundary between the agent core and all rendering
* streaming, styled, visually hierarchical terminal output
* live visibility into context state and session health
* input behaviour that respects how developers work in terminals
* a theme foundation that can grow without rework
* low operational friction for local-model users
* preserving architectural seams needed for later delegation

The easiest way to derail the product is to add too much orchestration before the terminal experience is solid.
