## Execution plan for steiner

This turns the staged roadmap into concrete engineering work. It assumes the reworked PRD structure and current package layout direction.

---

## Working assumptions

* Go 1.24+
* single binary plus `steiner-core-tools`
* OpenAI-compatible chat completions first
* no persistence early
* no sub-agents until after context compaction is real
* `provider.parallelism` enforced centrally, not inside agent code

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

# Stage 4 - Delegation scaffolding

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

## Concrete work items

1. Define delegation request struct.
2. Define delegation result struct.
3. Define what context can be passed to child.
4. Build child state lifecycle behind interface.
5. Ensure scheduler gates all model calls across parent/child.
6. Add delegation events.

## Tests

### Unit

* delegation contract serialization
* child state is isolated from parent state
* allowed-tools filtering
* limit inheritance/override rules

### Integration

* instantiate child run behind internal interface without surfacing to model yet
* scheduler still enforces `parallelism` across multiple agent instances

## Exit criteria

* sub-agent execution can be added without refactoring the loop architecture

---

# Stage 5 - Sub-agent execution v1

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

### `internal/output/delegation.go`

Implement:

* visible delegation notices in terminal

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

## Exit criteria

* delegation actually reduces context pressure

---

# Stage 6 - Hardening and ergonomics

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

## Concrete work items

1. Add transient provider retry rules.
2. Improve config errors.
3. Add `--dry-run` if useful.
4. Add JSONL run event log.
5. Add better failure taxonomy.
6. Add optional git-aware summaries.
7. Tighten provider capability flags if needed.

## Tests

### Unit

* retry backoff and stop behaviour
* config conflict detection
* JSONL event emission
* git diff helper parsing

### Integration

* simulated transient provider failures recover
* debug artifacts written correctly
* dry-run does not mutate files

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

1. contract structs
2. child state model
3. scheduler integration checks
4. tests

## Stage 5

1. synchronous child execution
2. parent/child handoff
3. model-facing `spawn_agent`
4. visibility/events
5. tests

## Stage 6

1. retries
2. diagnostics/logging
3. config hardening
4. optional git helpers
5. tests

---

# Package ownership / responsibility map

## `internal/config`

Only config loading, merge, validation, defaults.

## `internal/provider`

Only model transport, normalization, scheduling.

## `internal/agent`

Loop orchestration, state, limits, no transport details.

## `internal/tool`

Registry, schema, policy, execution, previews, output shaping.

## `internal/prompt`

Context gathering, budgeting, assembly, compaction.

## `internal/skill`

Skill discovery/loading only.

## `internal/repl`

Interactive UX only.

## `internal/delegation`

Delegation contracts and execution scaffolding.

## `internal/output`

Terminal and machine-readable event output.

That separation matters. If you blur these early, Stage 3 and Stage 5 will be a pain.

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

## Integration tests with fake provider

Use for:

* agent loop
* tool calls
* approvals
* prompt assembly
* delegation flow

## Integration tests with temp repos

Use for:

* file reads/writes/edits
* glob/search
* bash execution
* path confinement
* repo-like workflows

## Golden tests

Use sparingly for:

* prompt assembly
* tool schema
* approval previews
* compacted context blocks

## End-to-end smoke tests

Use for:

* `--exec` against a small fixture repo
* scripted REPL flow if not too brittle

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
