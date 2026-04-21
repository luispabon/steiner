# steiner - Product Requirements Document

**Version:** 0.3.0
**Status:** Draft
**Date:** 2026-04-21

This revision keeps the existing architecture constraints, preserves the implemented stage 0-3 foundation, and reorders the product story around the current priority: making the single-agent terminal experience stronger before returning to delegation.

---

## 1. Overview

steiner is a minimal, local-first coding agent written in Go. It accepts a user task, reasons about it using an LLM, executes tool calls against the local filesystem and shell, and iterates until the task is complete.

steiner is designed for real coding work over sessions that may become long, exploratory, or multi-step. Its central product concern is disciplined context management over time together with a console UX that keeps the agent understandable, controllable, and usable during real terminal work.

steiner is not a framework. It is a single opinionated binary with sensible defaults, a plugin-first tool model, a skills system for explicit context injection, and an architecture that can later support delegated work through isolated sub-agents.

Sub-agents remain part of the long-term product direction because context isolation matters. They are not the immediate product focus after stages 0-3. The immediate priority is a stronger single-agent console experience built on the context-discipline work already in place.

---

## 2. Product Goals and Non-Goals

### 2.1 Goals

steiner should:

* complete real coding tasks through iterative LLM reasoning and tool use
* remain usable over long sessions without uncontrolled context growth
* keep prompt construction deliberate, inspectable, and bounded
* make terminal interaction understandable and controllable during live use
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

* **interactive mode** - terminal session in the current project
* **single-shot mode** - one task executed via `--exec`

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
* inspect recent context diagnostics and budget pressure
* later, inspect when delegation occurred

### 5.3 Visibility and Trust

The terminal UX should surface the information needed to keep the agent understandable:

* tool name and relevant arguments before execution
* approval prompts for risky tools
* truncation notices when output is reduced
* skill activation when skills are injected
* current turn and budget usage where available
* clear stop reasons when limits are hit
* later, clear indication when a sub-agent has been used

### 5.4 Console UX Priorities

After stages 0-3, the product should prioritise a stronger terminal experience before delegation. That work should focus on:

* streaming assistant output where supported
* console input that behaves like a real shell prompt
* markdown-aware rendering for replies, diffs, and structured status
* clear visibility into context pressure and compaction
* a strong default dark terminal theme

Theme switching is a later enhancement, not a current requirement.

### 5.5 Resource-Constrained Operation

Users running local models on limited hardware must be able to reduce runtime pressure deliberately. steiner should expose provider/model parallelism controls so users can limit the number of concurrent in-flight LLM requests.

This is especially important once delegated execution exists, but the control belongs in the provider configuration from the start.

---

## 6. CLI and Interaction Surface

### 6.1 Commands

Current core surface:

```text
steiner
steiner --exec "task"
steiner --config path/to/config.yaml config
steiner --model model-name config
steiner --log-file path/to/session.log
steiner version
steiner tools
steiner skills
steiner config
```

### 6.2 REPL Commands

Current built-in commands:

```text
/help
/tools
/skills
/history
/clear
/exit
```

Skill invocation uses the same `/name` namespace, with built-in commands reserved.

### 6.3 Input Behaviour

The current REPL already supports command parsing, skill toggling, history inspection, and approval prompting. The next console UX pass should add:

* command history navigation
* common line-editing keybindings
* cursor movement within the current prompt
* better handling of pasted multi-line input
* improved skill and command completion

### 6.4 Output Behaviour

The terminal should favour legibility and trust over heavy visual ornament.

Current and near-term expectations:

* final assistant replies are shown clearly in both interactive and single-shot modes
* model, tool, approval, stop, and context-diagnostic events can be surfaced separately from human-facing output
* tool execution should show tool name, key arguments, approval state, truncation status, and result summary
* the default console presentation should use an intentional dark theme
* future streaming output should fit the same event model rather than bypassing it

---

## 7. Functional Architecture

### 7.1 Core Agent Loop

The base agent loop follows a ReAct-style cycle:

1. assemble prompt input from configured context sources
2. call the LLM
3. if the response contains tool calls:

   * validate and approve as required
   * execute each tool call
   * append tool results
   * continue
4. if the response is textual only:

   * display it
   * in interactive mode, wait for further input
   * in single-shot mode, exit

The loop terminates when the model produces a final text response or when a termination control fires.

### 7.2 Provider Abstraction Layer

All model communication should go through an abstract provider interface.

```go
type Provider interface {
    ChatCompletion(ctx context.Context, req ChatRequest) (*ChatResponse, error)
    StreamChatCompletion(ctx context.Context, req ChatRequest) (<-chan ChatChunk, error)
    SupportsUsageStats() bool
}
```

v1 ships with `openai_compat` and may support additional steiner-owned provider adapters without changing the main agent loop.

### 7.3 Provider Runtime Parallelism

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

### 7.4 Tool System

#### 7.4.1 Tool Model

steiner exposes tools to the LLM through structured function schemas derived from config.

Core tools ship with the project, but user-defined tools should fit the same registration model wherever possible.

#### 7.4.2 Tool Registration

Tools are declared in YAML config with:

* name
* executable path
* optional subcommand
* description
* parameter schema
* timeout
* approval mode
* optional execution constraints

#### 7.4.3 Tool Execution Contract

The tool contract must be explicit and consistent.

Primary contract:

* structured JSON input
* structured JSON output
* non-zero exit codes treated as errors

If simple script-style tooling is to be supported, that should be an explicit adapter mode rather than an implicit contradiction of the JSON contract.

#### 7.4.4 Core Tools

The core tool set should remain intentionally small:

* `read`
* `glob`
* `search`
* `write`
* `bash`
* `edit`

`edit` is the preferred mutation primitive for in-place changes. `write` remains available for whole-file replacement where appropriate.

#### 7.4.5 Tool Output Policy

Tool output must be bounded.

The execution layer should support:

* max stdout/stderr bytes captured
* truncation markers
* summary envelopes for large results
* binary output detection
* clear error propagation

This is required for context hygiene.

### 7.5 Editing Model

File mutation deserves its own design treatment.

The product should prefer safer edit primitives such as:

* exact replace
* patch application
* append
* structured file edits where appropriate

Full-file overwrite is acceptable as a compatibility primitive, but should not remain the default mutation path for reliable coding tasks.

### 7.6 Execution Safety Model

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

## 8. Skills and Convention System

### 8.1 AGENTS.md Conventions

steiner should support two optional convention sources:

1. global: `~/.config/steiner/AGENTS.md`
2. project: `./AGENTS.md`

Both may be included in the prompt assembly, with global conventions preceding project conventions.

### 8.2 Skills

Skills are reusable context snippets that the user explicitly injects.

Global skill location:

```text
~/.config/steiner/skills/
  skill-name/
    SKILL.md
```

Each skill is identified by its directory name and loaded only when explicitly invoked.

### 8.3 Skill Invocation

In interactive mode:

```text
/skill-name
```

In single-shot mode, skills should be explicitly selectable by CLI/config surface when exposed there.

### 8.4 Skill Authority

Skills must not be treated as peer system authority. They are explicit contextual assistance blocks added below the fixed system contract and conventions hierarchy.

This is important to avoid accidental prompt corruption through optional user-provided material.

---

## 9. Project Context Discovery

steiner may auto-discover bounded project context to help initial grounding.

### 9.1 Sources

Suggested discovery categories:

| Category               | Candidate files                                                      |
| ---------------------- | -------------------------------------------------------------------- |
| Project description    | `README.md`, `README`, `README.txt`                                  |
| Build/runtime metadata | `go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`, `Makefile` |

### 9.2 Behaviour

Project context should be:

* discovered automatically
* bounded by a configurable budget
* truncated or excerpted when large
* treated as reference context rather than permanent instruction
* overridable through config

### 9.3 Configuration

The project context system should support:

* max budget
* extra files
* ignore files
* future per-category policies

---

## 10. Configuration Model

### 10.1 Hierarchy

Configuration should resolve in this order:

1. compiled defaults
2. global config - `~/.config/steiner/config.yaml`
3. project config - `.steiner/config.yaml`
4. environment variables
5. CLI flags

Later layers override earlier ones.

### 10.2 Full Config Schema

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

logging:
  level: info
  file: ~/.local/share/steiner/steiner.log
```

### 10.3 Parallelism Configuration

`provider.parallelism` sets the maximum number of simultaneous LLM requests steiner may have in flight for that configured provider/model.

Examples:

* `1` - safest for constrained local setups
* `2` - permits limited concurrency
* `N` - upper concurrency bound for all active agent work using that provider

This is especially relevant for future delegation and should be respected by any request scheduler.

### 10.4 Environment Variable Mapping

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

### 10.5 Runtime-Important Config

The most operationally significant config includes:

* provider type, model, and base URL
* provider parallelism
* max turns and token budgets
* tool approvals
* tool timeouts
* tool output size limits
* project context budget
* writable path rules
* sub-agent limits

---

## 11. Termination, Cancellation, and Recovery

### 11.1 Termination Controls

Independent controls may stop a run:

* max turns
* cumulative token budget
* tool timeouts

### 11.2 Cancellation

The design should support:

* cancelling the current model call
* cancelling the current tool execution
* aborting the current run
* reporting cancellation clearly
* preserving deterministic session state after interruption

### 11.3 Recovery Behaviour

On failure, steiner should surface actionable information:

* which limit or error fired
* which tool or provider call failed
* what partial work was completed
* whether the session may continue

---

## 12. Delegation Architecture

Delegation exists to support context isolation, not novelty.

### 12.1 Purpose of Delegation

Sub-agents exist to:

* isolate exploratory or bulky work
* keep parent context compact
* allow bounded subtask execution with separate limits
* return concise results to the parent agent

### 12.2 Sub-Agent Model

A sub-agent is an isolated agent loop instance with:

* its own empty conversation history
* its own task prompt
* its own limits
* an allowed tool subset
* no automatic access to the parent transcript beyond what is explicitly passed

The parent agent should only receive the sub-agent's final result payload unless more detail is explicitly requested.

### 12.3 Parent/Sub-Agent Contract

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

### 12.4 Isolation Rules

* sub-agents do not inherit full parent history
* parent agents do not ingest sub-agent tool chatter
* sub-agents cannot nest by default
* delegated work is bounded by separate turn/token/runtime limits
* provider parallelism limits still apply globally

### 12.5 Deferred Delegation Features

These are intentionally deferred:

* nested sub-agents
* parallel sub-agents
* shared memory between agents
* parent inspection of full sub-agent transcript by default
* complex delegation graphs

Sub-agents are a core architectural requirement, but not an immediate implementation requirement.

---

## 13. Roadmap-Aligned Delivery Stages

This section describes capability sequencing, not detailed project planning. Stages 0-3 are the current implemented foundation and should remain stable while later stages are refocused around console UX before delegation.

### Stage 0 - Foundations Skeleton

Deliver:

* config loading and validation
* provider abstraction and scheduler
* core state types
* CLI skeleton and package boundaries

Exit condition:

* the architecture is ready for a single-agent loop without violating package boundaries or concurrency constraints

### Stage 1 - Core Single-Agent Execution

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

### Stage 2 - Safer Mutation and Context Discipline

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

### Stage 3 - Context Compaction Foundations

Deliver:

* rolling conversation compaction
* summarised retention of older turns
* preservation of active constraints and recent work
* compacted prompt assembly diagnostics

Exit condition:

* long sessions remain coherent without naive full-history replay

### Stage 4 - Console UX Foundations

Deliver:

* streaming-capable terminal output path
* shell-like prompt editing and history navigation
* clearer separation of assistant output from status events
* stronger approval and tool activity presentation
* default dark terminal theme

Exit condition:

* the interactive console feels responsive and legible during real coding sessions, without changing the core single-agent architecture

### Stage 5 - Session Visibility and Control

Deliver:

* clearer context budget visibility
* session and turn inspection improvements
* better cancellation and interruption UX
* richer REPL control surface where needed for usability

Exit condition:

* users can understand what the agent is doing, why context was trimmed, and how to control a long-running session

### Stage 6 - Delegation Foundations

Deliver:

* internal task handoff contract
* structured sub-agent result envelope
* isolated execution scaffolding
* scheduler that respects provider parallelism across all agent activity

Exit condition:

* the system is architecturally ready for sub-agent execution without redesigning the main loop

### Stage 7 - Sub-Agent Execution

Deliver:

* synchronous sub-agent spawning
* isolated sub-agent histories
* bounded tool subsets for delegated runs
* result-only return to parent
* separate delegation limits

Exit condition:

* delegated work reduces parent context growth rather than increasing it

### Stage 8 - Advanced Extensions

Potential later work:

* parallel sub-agents
* persistence
* sandboxed executors
* MCP support
* richer tool ecosystems
* more native provider implementations

---

## 14. Success Criteria

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
10. the console makes agent activity understandable during real terminal use
11. delegated work, once implemented, returns compact results without polluting parent history
12. failures and limits are surfaced clearly enough for the user to understand what happened
13. user-defined tools can be added without changing core steiner code

---

## 15. Deferred / Explicitly Out of Scope

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
* user-selectable theme packs

The architecture should leave room for these later, but they are not required for early delivery.

---

## 16. Suggested Project Structure

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
    repl/
      repl.go
      completer.go
    output/
      stream.go
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

## 17. Notes on v1 Discipline

The long-term product direction still includes delegated execution, but the near-term product should stay narrow.

The next shipped stages should optimise for:

* correctness of the single-agent loop
* bounded and explicit context construction
* safe tool execution
* stronger terminal usability and visibility
* low operational friction for local-model users
* preserving architectural seams needed for later delegation

The easiest way to derail the product is to add too much orchestration before the console, context, and execution discipline are solid.
