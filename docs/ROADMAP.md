Below is a staged implementation roadmap derived from the reworked PRD and your stated priority: context hygiene first, delegation later, no architectural dead ends. Based on the current PRD draft.

# steiner implementation roadmap

## Guiding rule

Every stage must improve one or more of these without materially harming the others:

* context cleanliness
* execution safety
* debuggability
* local-model operability
* future delegation readiness

Sub-agents are not an early deliverable. Their seams are.

---

## Stage 0 - Foundations and architecture skeleton

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

### Key design decisions to lock now

* exact config schema
* provider request lifecycle abstraction
* scheduler location and API
* tool contract shape
* event/log format
* path resolution model relative to project root

### Important implementation notes

* implement provider scheduler now, even if stage 1 only ever uses one in-flight request
* make `provider.parallelism` enforced centrally, not in ad hoc agent code
* define one canonical internal message format for user / assistant / tool / summary / context block

### Exit criteria

* resolved config can be loaded, merged, and printed
* provider config accepts `parallelism`
* CLI boots and validates config without running an agent
* internal packages compile cleanly

### Risks to avoid

* scattering config logic across packages
* mixing prompt assembly with agent execution too early
* leaving concurrency control as a future afterthought

---

## Stage 1 - Core single-agent loop

### Goal

Ship the thinnest useful agent that can do real work end-to-end.

### Deliverables

* OpenAI-compatible provider implementation
* single-agent ReAct loop
* REPL mode
* `--exec` mode
* streaming output where supported
* core termination controls:

  * max turns
  * model call cancellation
  * tool timeout handling
* minimal system preamble
* AGENTS.md loading
* bounded project context loading
* skills discovery and explicit invocation
* core tools:

  * read
  * glob
  * search
  * write
  * bash
* approval system
* plain logging of model calls, tool calls, and stop reasons

### Scope constraints

* no sub-agents
* no compaction yet
* no fancy edit primitive yet
* no persistence
* no concurrency beyond scheduler enforcement

### Implementation notes

* keep tool execution sequential for now
* even if the provider supports streaming and tool calling differently, normalize both through the same internal response model
* skills should not be injected as peer system authority
* project context should be bounded hard by byte/token budget, not best effort

### Exit criteria

* can fix a small bug in a toy repo
* can read files, edit one file, run a targeted test, and explain result
* tool approvals behave correctly
* path handling is project-root-relative and deterministic
* local setup with `parallelism: 1` behaves predictably on constrained hardware

### Risks to avoid

* overbuilding terminal UX
* letting README/project context dominate prompt size
* making write the only future-compatible edit path

---

## Stage 2 - Execution safety and safer mutation

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
* first safer mutation primitive:

  * either `edit` as exact old/new replacement
  * or `patch` as unified diff apply

### Recommendation

Implement `edit` first, not `patch`.

Reason:

* easier to validate
* easier to preview in approvals
* easier to recover from model mistakes
* enough for early coding tasks

### Approval preview improvements

* show path
* show diff or replacement excerpt
* show cwd
* show timeout
* show truncation warning if applicable

### Exit criteria

* agent can modify files without relying solely on blind overwrite
* large command output no longer pollutes conversation naively
* dangerous paths are rejected by policy, not only by user judgment
* shell tool behaviour is inspectable and constrained

### Risks to avoid

* implementing patch application without strong validation
* dumping truncated tool output into context anyway
* allowing shell execution outside project root by accident

---

## Stage 3 - Context discipline and compaction

### Goal

Make long sessions viable.

### Deliverables

* context assembler with explicit source ordering
* rolling retention policy implementation
* tool-output summary envelopes
* conversation compaction mechanism
* preservation of active constraints across compaction
* optional prompt inspection/debug command
* explicit context diagnostics in logs:

  * source budgets
  * retained turns
  * compacted segments
  * dropped/truncated material

### Core behaviour to implement

* recent turns retained verbatim
* oversized tool outputs reduced to structured summaries
* older conversational history compacted into rolling summaries
* active conventions and unresolved tasks preserved
* project context rebounded every turn from policy, not accumulated naively

### Suggested internal artifacts

* `ConversationState`
* `ContextAssembler`
* `CompactionPolicy`
* `SummaryBlock`
* `ActiveConstraints`

### Exit criteria

* long exploratory sessions remain coherent
* prompt size does not grow linearly with every tool call
* user can inspect when compaction happened
* critical decisions survive compaction

### Risks to avoid

* compaction that destroys actionable state
* summarising away user constraints
* treating tool output summaries as authoritative instructions

---

## Stage 4 - Delegation scaffolding

### Goal

Build the seams for sub-agents without actually shipping full delegated execution yet.

### Deliverables

* delegation package
* explicit parent/subtask contract
* scoped context handoff builder
* structured sub-agent result envelope
* limit inheritance/override rules
* scheduler integration so all future agent activity respects provider parallelism
* event model for delegated activity

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

### Risks to avoid

* leaking parent transcript wholesale
* reusing shared mutable state between parent and child
* encoding delegation as a hacky tool shortcut without a real contract

---

## Stage 5 - Sub-agent execution v1

### Goal

Ship the first real delegated execution path.

### Deliverables

* `spawn_agent` exposed internally and then to the model
* synchronous sub-agent execution only
* isolated sub-agent conversation history
* parent passes bounded context only
* result-only integration back to parent
* separate limits for sub-agent turns/tokens/runtime
* clear terminal visibility when delegation occurs

### Behavioural rules

* child cannot spawn another child
* child only sees explicitly passed context
* parent only receives compact child result payload
* child tool chatter never enters parent history verbatim

### Recommendation

Do not expose delegation to the model until:

* compaction works
* truncation works
* scheduler works
* safer edits work

Otherwise you will create a more expensive mess faster.

### Exit criteria

* delegated search/exploration tasks reduce parent prompt growth
* parent can continue productively after delegated result returns
* local-model users with `parallelism: 1` still get deterministic behaviour
* no transcript leakage from child to parent unless explicitly enabled for debug

### Risks to avoid

* returning too little from child and forcing repeated delegation
* returning too much and defeating isolation
* concurrency creeping in early

---

## Stage 6 - Hardening and ergonomics

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
* better output formatting

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

## Stage 7 - Advanced capabilities

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

## Must happen before Stage 5

* delegation contract
* isolated child state
* provider parallelism enforcement
* output truncation and compaction

---

# Suggested milestone breakdown

## Milestone A

Stages 0-1
Target outcome: usable minimal agent

## Milestone B

Stage 2
Target outcome: safer editing and safer execution

## Milestone C

Stage 3
Target outcome: long-session viability

## Milestone D

Stages 4-5
Target outcome: real delegated execution without context pollution

## Milestone E

Stages 6-7
Target outcome: robustness and advanced features

---
