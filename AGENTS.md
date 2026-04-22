# steiner — agent conventions

steiner is a minimal, local-first coding agent in Go. Single binary. OpenAI-compatible providers. Context hygiene is the primary engineering concern.

---

## Project layout

```
cmd/steiner/            CLI entry, subcommands
cmd/steiner-core-tools/ Core tools binary (read, glob, search, write, bash, edit)
internal/agent/         Loop orchestration, state, limits — no transport details
internal/config/        Loading, merging, validation, defaults only
internal/provider/      Model transport, response normalisation, scheduler
internal/tool/          Registry, schema, policy, executor, output shaping
internal/prompt/        Context gathering, budgeting, assembly, compaction
internal/skill/         Skill discovery and loading only
internal/repl/          Interactive UX only
internal/delegation/    Delegation contracts and execution scaffolding (Stage 4+)
internal/output/        Terminal and machine-readable event output
testdata/repos/         Fixture repos for integration/e2e tests
docs/                   Product design docs
.project_planning/      Implementation logs
```

Package boundaries are hard. Do not blur them. If `internal/agent` imports `internal/provider` directly (not through the interface), that is a violation. If `internal/prompt` imports `internal/agent`, that is a violation.

---

## Product design docs

The product design docs at `docs/` as well as the contents of `.project_planning/` are very large. Do NOT load them into your context unless specifically instructed to do so.

## Architecture invariants

### Context sources — fixed precedence order

1. Fixed system preamble
2. Global `~/.config/steiner/AGENTS.md`
3. Project `./AGENTS.md`
4. Auto-discovered project context (bounded by token budget)
5. User-invoked skills
6. Active conversation history
7. Tool results
8. Delegated sub-agent return payloads (Stage 5+)

Never reorder these. Never silently inject a source that is not in this list.

### Provider scheduler

`provider.parallelism` is enforced centrally by the scheduler. No agent code may call a provider directly without going through the scheduler semaphore. This applies to both parent and child agents once delegation exists.

### Tool contract

All tools speak JSON: structured JSON input, structured JSON output, non-zero exit = error. No exceptions for core tools. Script-style tools require an explicit adapter mode — they must not silently contradict the JSON contract.

### Skills are not system authority

Skills are injected as auxiliary context below the fixed system contract and convention hierarchy. They must never be encoded as peer system instructions or given AGENTS.md-level authority.

### Sub-agents are isolated

A sub-agent gets its own empty conversation history, its own limits, and only explicitly passed context. Parent does not inherit child tool chatter. Child cannot spawn a child. The whole point is context isolation — this is not negotiable.

---

## Config

Config resolves in this order (later overrides earlier):

1. Compiled defaults
2. `~/.config/steiner/config.yaml`
3. `.steiner/config.yaml`
4. Environment variables (`STEINER_` prefix)
5. CLI flags

Key env vars: `STEINER_API_KEY`, `STEINER_BASE_URL`, `STEINER_MODEL`, `STEINER_PROVIDER_PARALLELISM`, `STEINER_MAX_TURNS`, `STEINER_LOG_LEVEL`.

All config loading lives in `internal/config`. Nothing else does config merging or env interpolation.

---

## Roadmap stage reference

| Stage | Goal | Key deliverable |
|-------|------|-----------------|
| 0 | Foundations skeleton | Config, provider interface, scheduler, state types, CLI stubs |
| 1 | Core single-agent loop | Working ReAct loop, core tools, REPL, --exec, approvals |
| 2 | Execution safety | Path confinement, output caps, `edit` tool, approval previews |
| 3 | Context compaction | Rolling compaction, source budgets, summary blocks |
| 4 | Delegation scaffolding | Contract types, child state, scheduler integration |
| 5 | Sub-agent execution v1 | `spawn_agent`, isolated child runs, result-only return |
| 6 | Hardening / ergonomics | Retries, diagnostics, JSONL log, config hardening |
| 7 | Advanced | Parallel agents, MCP, persistence, sandboxing |

Do not start Stage 5 until Stages 2 and 3 are solid. Context discipline must precede delegation.

---

## Core tools

Implemented in `cmd/steiner-core-tools/`:

- `read` — read file contents
- `glob` — find files by pattern
- `search` — search file contents
- `write` — overwrite a file (early primitive)
- `bash` — run shell command under project root
- `edit` — exact old/new replacement (Stage 2+, preferred over write for mutations)

`edit` is preferred for file mutation. `write` is kept but must not be the only mutation path.

---

## Testing strategy

| Layer | Use for |
|-------|---------|
| Unit | Config, schema generation, policy checks, compaction logic, delegation contracts, scheduler semantics |
| Integration (fake provider) | Agent loop, tool calls, approvals, prompt assembly, delegation flow |
| Integration (temp repos) | File reads/writes/edits, glob/search, bash, path confinement |
| Golden tests | Prompt assembly snapshots, tool schema snapshots, compacted context snapshots |
| E2E smoke | `--exec` against fixture repos |

Fixture repos live in `testdata/repos/`: `go_tiny_bug/`, `multi_file_search/`, `large_output/`, `delegation_fixture/`.

---

## What not to do

- Do not scatter config logic across packages — all of it lives in `internal/config`.
- Do not mix prompt assembly with agent execution — `internal/prompt` and `internal/agent` are separate.
- Do not leave concurrency control as a future concern — the scheduler belongs in Stage 0.
- Do not inject skills as system authority — they are auxiliary context only.
- Do not accumulate tool output naively in context — output must be bounded and summarised.
- Do not allow context to grow linearly with tool calls — compaction is a Stage 3 hard requirement.
- Do not implement delegation before compaction and output truncation exist.
- Do not allow child agents to nest — `allow_nesting: false` enforced structurally.
- Do not return full child transcripts to parent — result envelope only.
- Do not start Stage 7 until bounded context, safe execution, deterministic scheduling, and comprehensible failure modes all work.

---

## Go conventions

- Go 1.24+
- `gofmt` and `go vet` required before commit
- Errors returned, not panicked, except at process boundary for truly unrecoverable state
- `context.Context` threaded through all provider and tool calls for cancellation
- Internal message types are canonical — do not duplicate or alias provider-specific types into agent logic
- Table-driven tests preferred
- `testdata/` for fixture files, never embed large fixtures inline

---

## Approval modes

Per-tool config under `approval.overrides`:

- `auto` — executes immediately, no prompt
- `prompt` — asks user before executing
- `deny` — always rejected

Defaults: `read`, `glob`, `search` → `auto`; `write`, `bash`, `edit` → `prompt`.

---

## Context failure modes to guard against

- Prompt bloat from large tool output
- Stale README dominating context
- Conflicting instructions across AGENTS.md and skills
- Conversation drift during long sessions
- Parent context pollution from delegated work
- Repeated reinjection of low-value context
- Degraded responsiveness from oversized prompts on local models
