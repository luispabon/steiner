# steiner - agent manual

`steiner` is a minimal local-first Go coding agent. Optimize for bounded context, explicit tool use, and safe local execution.

## Project Overview

`cmd/steiner` composes the CLI and wires packages together. `internal/agent` runs the loop, `internal/prompt` assembles context, `internal/tool` executes bounded tools, `internal/output` shapes terminal and machine-readable events, and `internal/tui` owns the interactive UI.

## Repo map

```text
cmd/steiner/             CLI entry and subcommands
internal/advisor/        Advisor tool: prompt building, streaming, file review
internal/agent/          Loop orchestration, state, limits
internal/config/         Config loading, merging, validation, defaults
internal/delegation/     Delegation contracts and scaffolding
internal/history/        Conversation history persistence
internal/interactive/    Interactive session orchestration: run flow, replay, session/snapshot reports, dispatch (drives internal/agent)
internal/mcp/            MCP server connections (stdio transport, tool registration)
internal/metadata/       Local cache of model metadata from models.dev
internal/modelcatalog/   Provider model catalog: enumerators, cache, popularity, merge/rank
internal/notify/         Desktop notifications
internal/oauth/          OAuth flows: token exchange, refresh, PKCE
internal/oneshot/        Autonomous oneshot engine: phases, manifest, lock/resume, closeout
internal/output/         Terminal and machine-readable event output
internal/prompt/         Context gathering, budgeting, assembly, compaction
internal/provider/       Model transport
internal/sandbox/        Sandboxed execution: Docker, mounts, env isolation
internal/session/        Session and project-directory persistence
internal/skill/          Skill discovery and loading
internal/tool/           Registry, schema, policy, executor, output shaping
internal/tui/            Interactive TUI
internal/update/         Self-update / binary update checking
internal/usagestats/     Cache hit-rate / usage stats recording
skills/                  Bundled skills
testdata/stage3/         Integration test fixtures
docs/                    Product/design docs and implementation notes
```

## Boundaries and invariants

* Keep package boundaries strict.
* `cmd/steiner` is the composition root: wires dependencies and flags only, no business logic.
* `internal/agent` must not bypass provider abstractions; concurrent tool execution must respect `MaxParallelTools`, not spawn unbounded goroutines.
* `internal/prompt` owns context assembly, stays separate from execution, and must not import `internal/delegation` (`internal/delegation/bootstrap.go` imports `internal/prompt`; the reverse would cycle).
* Context assembly order is intentional; update `internal/prompt` tests when changing precedence.
* `internal/config` owns config loading and merging.
* Core tools use structured JSON input/output only.
* Tools must keep output bounded and summarized.
* Skills are auxiliary context; they do not override user instructions, tool policy, repo code, or this file.
* Sub-agents receive only explicitly passed context and cannot nest.
* Mutation tools are approval-gated by default.
* **Prompt cache integrity is load-bearing** — protect the prefix:
  * Keep static sources (preamble, agents, project context, skills) before dynamic sources (conversation, tool summaries); don't reorder `internal/prompt/source_plan.go`.
  * No per-turn non-determinism in the prefix (preamble, tool definitions, skills, project context) — the preamble is memoized per session by `CachedSystemPreamble`.
  * Tool definition ordering stays deterministic (`internal/tool/registry.go` sorts by name; filtered subsets must not depend on map iteration order).
  * Don't remove or rename cache hints: Codex `session-id`/`thread-id` headers (`internal/provider/codex_responses.go`), Anthropic `cache_control` breakpoints (`internal/provider/anthropic_wire.go`), or `PromptCacheKey` assignment rules.
  * Compaction intentionally invalidates the summarized portion of the prefix — that's expected, not a bug.

## File organisation

* Use snake_case Go filenames; put tests next to source.
* Aim for production `.go` files under ~300 lines, split around ~500 by domain responsibility. The ~500 line mark is a trigger to review, not a hard cap — a single cohesive concern (e.g. one render pipeline of small functions) may exceed it; note the exception in the PR.
* In `internal/tui`, keep render, update, sidebar, and event-state concerns split before files drift past the target. Same for render/event/preview-report concerns in `internal/output`.
* Do not create `util`, `helper`, or `common` packages; shared code lives in the package that owns the domain.
* Branch from `origin/main` unless told otherwise.

## Work loop

1. Inspect nearby code, call sites, and the smallest relevant tests before editing.
2. Prefer `mutate` for file mutations; do not expose `apply_patch`, `write`, or `edit` to agents.
3. Keep changes minimal and package boundaries intact.
4. Write comprehensive unit and functional tests for new functionality.
5. Run `gofmt -w <files>` after Go edits.
6. Run targeted tests first; broaden checks when practical or risk warrants it.
7. If checks fail or cannot run, report the exact command and reason.

Commands:

```bash
gofmt -w <files>
goimports -w <files>
go test ./path/to/pkg -run TestName
go test ./...
go test -race ./...
go build ./...
go vet ./...
golangci-lint run ./...
govulncheck ./...
make check
make build-binaries
```

Before finalizing Go changes, run `make check`. If a check cannot run, report the exact command and failure.

Go version: `1.25`.

## Go conventions

* Favor table-driven tests.
* Thread `context.Context` through provider, tool, and testable helper calls.
* Return errors instead of panicking except at process boundaries.
* Wrap errors as `fmt.Errorf("<lowercase action>: %w", err)`.
* Do not silently discard production errors; comment intentional ignores.
* Define interfaces at the consumer, keep them small, avoid header interfaces.
* Add nearby tests for new or changed behavior under `internal/`, using `testdata/` for fixtures over large inline literals.
* Use `0o` octal literals; don't shadow builtins like `close`, `max`, `min`.
* Keep symbols unexported unless cross-package use requires export; exported `internal/` symbols need Godoc starting with the symbol name.
* TODO comments must name the follow-up action or owner, not leave open-ended debt markers.
* Do not commit no-op/stub functions that silently do nothing (e.g. a validator whose body is only `_ = arg`) — implement it, remove it, or comment the concrete follow-up. Same standard applies to tests: every test needs an observable failing path (`t.Error`/`t.Fatal`); a `t.Logf` comparison asserts nothing.
* When asserting file/directory permissions in tests, compare exactly (`mode.Perm() != 0o600`) or assert absence of unwanted bits (`mode&0o077 != 0`). A subset mask like `mode&0o644 != 0o644` also passes for `0o666`/`0o777` — don't use it.

## Security

* Never hardcode or commit provider credentials.
* Treat `--log-file` and `STEINER_LOG_FILE` as sensitive; they may capture prompts/tool output.
* Default local provider URL: `http://localhost:11434/v1`.

## Documentation maintenance

A code change must update its matching docs in the same commit:

| # | Change | Update |
|---|---|---|
| 1 | `internal/tool`: add/remove/rename a built-in tool | README "Built-in tools"; this file's "Built-in tools" list; sub-agent allowlist tables in docs/sub-agent-delegation.md if the tool appears there |
| 2 | `internal/config`: add/change/remove a field | docs/configuration.md field entry; defaults section if defaults changed; README config examples if a commonly-used field; if a prompt-injection toggle (e.g. `cave_human`) — also docs/optional-features.md and the README "Other features" one-liner |
| 3 | `internal/delegation`: add/remove a sub-agent type or change a tool allowlist | docs/sub-agent-delegation.md types table; docs/sub-agent-delegation-internals.md if architecture/bootstrapping/tool construction changed; README sub-agent delegation tool table |
| 4 | `internal/prompt`: change compaction, budgets, or context-management behaviour | docs/context-management.md if user-facing; docs/context-management-internals.md for internal behaviour; README "Context management" section if the high-level description changed |
| 5 | New top-level feature, or a new package under `internal/` | README feature section; a docs/ page if it warrants detail; a new package gets an entry in the Repo map above; a new row in this table |
| 6 | `internal/oneshot`: engine, phases, manifest fields, lock/resume, closeout, or reports | docs/oneshot.md if user-facing; docs/oneshot-internals.md for internal behaviour; if a config field changed — also docs/configuration.md and the `oneshot` example in README |
| 7 | `internal/usagestats`: recorder, bucketing, persistence, retention, windows, or surfacing | docs/cache-stats.md; README "Cache hit rate tracking" if the high-level behaviour changed |
| 8 | `internal/notify`: platform driver or Service behaviour | docs/desktop-notifications.md if user-facing; docs/desktop-notifications-internals.md if the driver interface/extension points changed; README "Desktop notifications" section if the high-level description changed |
| 9 | `internal/oauth`: flow, token store, refresh, or PKCE | docs/optional-features.md "Codex OAuth" section if setup steps/token storage changed; docs/configuration.md provider types table if auth behaviour changed |
| 10 | Optional feature change (`cave_human`, accent colour, web search, image paste, conversation forking, code simplification, Codex OAuth) | docs/optional-features.md's section; README "Other features" one-liner if the summary changed |
| 11 | Execution mode change (plan/build enforcement, mode-switching UX, `modes.default`) | docs/execution-modes.md; README "Execution modes" section if the high-level description changed; docs/sub-agent-delegation.md Safety section if the `code`/`follow_up` denial scope changed |
| 12 | `internal/mcp`: manager, transport, naming, approval, or tooldef behaviour | docs/mcp.md if user-facing; docs/configuration.md for config field changes; README MCP section if the high-level description changed |

**13.** `delegationInstructions`/consumer-file changes (`internal/prompt/system.go`'s `delegationInstructions`, `internal/prompt/specialists.go`'s `specialists` slice, or any of `skills/{implement,review,simplify,plan,pull-request}/SKILL.md`, `internal/oneshot/prompts/*.md`):

* Update docs/canon-drift-checks.md if the change affects what counts as canon or the consumer file list.
* The `## Your sub-agents` table renders from the `specialists` slice in `internal/prompt/specialists.go` — edit the slice, never the markdown.
* Canon must not name a tool gated behind config independently of `delegation.enabled` (e.g. `advisor`); put such mentions in that tool's own preamble section.
* `internal/oneshot/prompts/{plan,review}.md` share four blocks verbatim with `skills/{plan,review}/SKILL.md`, pinned by `internal/oneshot/prompts_shared_test.go` — edit both copies and the test literal together.
* The `### Worktree Handling` block, the `### Pre-Commit Checklist` block, and the fix-delegation bullet list are each deliberately duplicated verbatim: the first two across `skills/{implement,review,simplify}/SKILL.md`, the fix-delegation list across `skills/{review,simplify}/SKILL.md`. All three are pinned by `skills/shared_blocks_test.go` — edit every copy and the test literal together, never one alone.

## Built-in tools

Steiner exposes these model-facing built-in tools:

- `read` — read files with offset/limit pagination
- `mutate` — apply one or more structured file mutations atomically (create, write, replace, line_replace, delete, move)
- `glob` — find files by pattern
- `grep` — search file contents with context
- `ls` — list directories
- `bash` — run shell commands
- `scratchpad` — record working state (intent, decisions, next action); persists across compaction
- `fetch_url` — fetch a URL and return its content as markdown or image data
- `display_file` — show a file in the TUI overlay without adding contents to conversation
- `advisor` — ask a stronger-model steering advisor for guidance, optionally passing `question` and `files` for it to review (requires `advisor.enabled`)
- `workflow_handoff` — transition to a different workflow with approved artifacts
- `mcp__<server>__<tool>` — MCP tools registered from connected MCP servers appear alongside built-ins with the `mcp__` prefix (tool names may include an optional 8-hex SHA-256 hash suffix when sanitisation or truncation is required). Their schemas and results come from third-party servers, not steiner.

Steiner owns the schemas and result formats. Dive implements the behavior.

## Docs

Do not broadly load `docs/` or `.steiner/plans/`; read only the specific file or section needed. Prefer README plus nearby code for orientation.
