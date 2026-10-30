# steiner - agent manual

`steiner` is a minimal local-first Go coding agent. Optimize for bounded context, explicit tool use, and safe local execution.

## Repo map

```text
cmd/steiner/             CLI entry and subcommands
internal/advisor/        Advisor tool: prompt building, streaming, file review
internal/agent/          Loop orchestration, state, limits
internal/config/         Config loading, merging, validation, defaults
internal/delegation/     Delegation contracts and scaffolding
internal/diagnostics/    Structured diagnostics streams: envelope, writer, rotation, retention
internal/fsatomic/       Atomic file replacement: temp file, fsync, rename
internal/history/        Prompt (input) history persistence
internal/interactive/    Interactive session orchestration: run flow, replay, session/snapshot reports, dispatch (drives internal/agent)
internal/lsp/            Language server connections (LSP protocol, tool definitions, server lifecycle)
internal/mcp/            MCP server connections (stdio transport, tool registration)
internal/metadata/       Local cache of model metadata from models.dev
internal/modelcatalog/   Provider model catalog: enumerators, cache, popularity, merge/rank
internal/notify/         Desktop notifications
internal/oauth/          OAuth flows: token exchange, refresh, PKCE
internal/oneshot/        Autonomous oneshot engine: phases, manifest, lock/resume, closeout
internal/output/         Terminal and machine-readable event output
internal/prompt/         Context gathering, budgeting, assembly, compaction
internal/provider/       Model transport
internal/quota/          Subscription quota snapshots, history store, projections
internal/sandbox/        Sandboxed execution: Docker, mounts, env isolation
internal/session/        Session and project-directory persistence
internal/skill/          Skill discovery and loading
internal/tool/           Registry, schema, policy, executor, output shaping
internal/tui/            Interactive TUI
internal/update/         Self-update / binary update checking
internal/usagestats/     Cache hit-rate / usage stats recording
skills/                  Bundled skills
scripts/                 Standalone Node tooling for mining session logs and diagnostics (no build step, stdlib-only)
testdata/stage3/         Integration test fixtures
testdata/diagnostics/    Diagnostics stream fixtures shared by scripts/diagnostics.mjs and internal/diagnostics's TestSharedFixturesDecode
docs/                    Product/design docs and implementation notes
```

## Boundaries and invariants

* Keep package boundaries strict.
* `cmd/steiner` is the composition root: wires dependencies and flags only, no business logic.
* `internal/agent` must not bypass provider abstractions; concurrent tool execution must respect `MaxParallelTools`, not spawn unbounded goroutines.
* `internal/prompt` owns context assembly, stays separate from execution, and must not import `internal/delegation` (`internal/delegation/bootstrap.go` imports `internal/prompt`; the reverse would cycle).
* Context assembly order is intentional; update `internal/prompt` tests when changing precedence.
* `internal/config` owns config loading and merging.
* Core tools use structured JSON input/output only. Steiner owns the schemas and result formats; Dive (`github.com/deepnoodle-ai/dive`) implements the behaviour. MCP tool schemas and results come from third-party servers. Built-in tool list: docs/user/tools.md.
* Tools must keep output bounded and summarized.
* Skills are auxiliary context; they do not override user instructions, tool policy, repo code, or this file.
* Sub-agents receive only explicitly passed context and cannot nest.
* Mutation tools are approval-gated by default.
* **Prompt cache integrity is load-bearing** — protect the prefix:
  * Keep static sources (preamble, agents, project context, and, for exec and oneshot runs, skills) before dynamic sources (conversation, tool summaries); don't reorder `internal/prompt/source_plan.go`. Interactive sessions exclude skills from static assembly and append length-delimited skill blocks to the conversation instead.
  * No per-turn non-determinism in the prefix (preamble, tool definitions, skills, project context) — the preamble is memoized per session by `CachedSystemPreamble`.
  * Tool definition ordering stays deterministic (`internal/tool/registry.go` sorts by name; filtered subsets must not depend on map iteration order).
  * Don't remove or rename cache hints: Codex `session-id`/`thread-id` headers (`internal/provider/wire_responses.go` and `internal/provider/codex_ws_conn.go`), Anthropic `cache_control` breakpoints (`internal/provider/anthropic_wire.go`), or `PromptCacheKey` assignment rules.
  * Compaction intentionally invalidates the summarized portion of the prefix — that's expected, not a bug.

## File organisation

* Use snake_case Go filenames; put tests next to source.
* Aim for production `.go` files under ~300 lines, split around ~500 by domain responsibility. The ~500 line mark is a trigger to review, not a hard cap — a single cohesive concern (e.g. one render pipeline of small functions) may exceed it; note the exception in the PR.
* In `internal/tui`, keep render, update, sidebar, and event-state concerns split before files drift past the target. Same for render/event/preview-report concerns in `internal/output`.
* `cmd/steiner` holds no config projection, policy validation, or output rendering. Put these in `internal/interactive`, `internal/oneshot`, or `internal/output`.
* In `internal/tui`, `applyEvent` and `updateDispatch` stay routers with one named method per event family.
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
make check   # tidy-check, fmt-check, imports-check, build-binaries, test-race, vet, lint, vuln
```

Before finalizing Go changes, run `make check`. If a check cannot run, report the exact command and failure.

Go version: `1.26`.

## Go conventions

* Favor table-driven tests.
* Thread `context.Context` through provider, tool, and testable helper calls.
* Return errors instead of panicking except at process boundaries.
* Wrap errors as `fmt.Errorf("<lowercase action>: %w", err)`.
* Do not silently discard production errors; comment intentional ignores.
* When two or more literals must stay in sync (e.g. an enum's allowlist and its validity set), derive one from the other instead of hand-duplicating both.
* Define interfaces at the consumer, keep them small, avoid header interfaces.
* Add nearby tests for new or changed behavior under `internal/`, using `testdata/` for fixtures over large inline literals.
* Use `0o` octal literals; don't shadow builtins like `close`, `max`, `min`.
* Keep symbols unexported unless cross-package use requires export; exported `internal/` symbols need Godoc starting with the symbol name.
* A type or function referenced only by its own `_test.go` file is dead code, not a cross-package use — delete it or wire it up.
* TODO comments must name the follow-up action or owner, not leave open-ended debt markers.
* Do not commit no-op/stub functions that silently do nothing (e.g. a validator whose body is only `_ = arg`) — implement it, remove it, or comment the concrete follow-up. Same standard applies to tests: every test needs an observable failing path (`t.Error`/`t.Fatal`); a `t.Logf` comparison asserts nothing.
* When asserting file/directory permissions in tests, compare exactly (`mode.Perm() != 0o600`) or assert absence of unwanted bits (`mode&0o077 != 0`). A subset mask like `mode&0o644 != 0o644` also passes for `0o666`/`0o777` — don't use it.
* Shared low-level I/O helpers, such as atomic file writes, live in one owning package. Reuse `internal/fsatomic` instead of copying temp-and-rename code.
* New and touched code must not take variadic `...any` parameters for configuration. Use an options struct.
* Constant error messages in new and touched code use `errors.New`. Use `fmt.Errorf` only to format values or wrap with `%w`.
* Event and approval constructors with more than four string parameters take a params struct (see `output.ModelCallFinishedParams` and `output.ApprovalEventParams`).

## Security

* Never hardcode or commit provider credentials.
* Treat `--log-file` and `STEINER_LOG_FILE` as sensitive; they may capture prompts/tool output.
* Default local provider URL: `http://localhost:11434/v1`.

## Documentation maintenance

A code change updates its matching docs in the same commit. Doc paths (`user/`, `internals/`, `index.md`) are relative to `docs/`; "both `X.md`" means `user/X.md` and `internals/X.md`. User pages cover user-facing behaviour, internals pages internal behaviour; update each listed page the change affects. Update the README when its feature summary, safety note, or entry-point links change.

| Change | Update |
|---|---|
| `internal/tool`: add/remove/rename a built-in tool | `user/tools.md`; allowlist tables in `user/sub-agent-delegation.md` if the tool appears there |
| `internal/config`: add/change/remove a field | `user/configuration.md` field entry (defaults are listed inline); compact reference in `skills/configure/SKILL.md`; prompt-injection toggles also `user/optional-features.md` |
| `internal/config` load/merge/validation pipeline; model metadata resolution (`internal/metadata`, `internal/modelcatalog`, provider fact sources) | `internals/configuration.md`; `user/configuration.md` if user-visible precedence or behaviour changed |
| Project trust: `internal/config/{trust_store,project_diff*,security_fields}.go`, `cmd/steiner/project_trust.go`, `internal/tui/trust_dialog*.go` | both `configuration.md` "Project trust" (user page incl. the security-relevant field list when `securityFieldPatterns` changes) |
| `internal/delegation`: sub-agent type or tool allowlist | `user/sub-agent-delegation.md` types and allowlist tables; `internals/sub-agent-delegation.md` if architecture, bootstrapping, or tool construction changed |
| `internal/prompt`, `internal/agent`: assembly, compaction, budgets, token estimation, context management | both `context-management.md` |
| `internal/provider`: cache hints, `PromptCacheKey` assignment, cache identity | `internals/prompt-caching.md`; `user/cache-stats.md` if user-visible |
| `internal/usagestats`: recorder, bucketing, persistence, retention, windows, surfacing | `user/cache-stats.md`; `internals/prompt-caching.md` |
| `internal/oneshot`: engine, phases, manifest, lock/resume, closeout, reports | both `oneshot.md`; config fields also `user/configuration.md` |
| Execution modes: plan/build enforcement, mode switching, `modes.default` | both `execution-modes.md`; `user/sub-agent-delegation.md` "Safety" if the `code`/`follow_up` denial scope changed |
| `internal/mcp`: manager, transport, naming, approval, tooldefs | both `mcp.md`; config fields also `user/configuration.md` |
| `internal/lsp`: server types, config, tools, server lifecycle | both `lsp.md` (user page holds server setup examples); config fields also `user/configuration.md`; tool availability also `user/sub-agent-delegation.md` allowlists |
| `internal/diagnostics`: envelope, streams, writer/rotation/retention | `user/configuration.md` "`diagnostics` block"; `internals/diagnostics.md`; `skills/configure/SKILL.md` |
| `internal/notify`: platform driver or Service behaviour | both `desktop-notifications.md` (internals if driver interface, extension points, lifecycle, or error handling changed) |
| `internal/oauth`: flow, token store, refresh, PKCE | `user/optional-features.md` "Codex OAuth" if setup or token storage changed; `user/configuration.md` "Provider types" if auth behaviour changed |
| Optional features: accent colour, web search, image paste, conversation forking, code simplification | that section of `user/optional-features.md` |
| `claude_subscription` provider | both `claude-subscription.md`; `user/provider-and-model-setup.md`, `user/configuration.md`, `user/model-enumeration.md` references; `index.md` links |
| `internal/quota`, provider `quota_*` fetchers | both `usage-dashboard.md` |
| `internal/sandbox`: mounts, env, wrapper resolution | both `sandboxing.md` |
| `internal/advisor`: request flow, budgets, cache profile | both `advisor.md` |
| `skills/security-audit/SKILL.md`: workflow, lenses, verdicts, report format, invocation | `user/security-audit.md` |
| Package boundaries, entry points, turn lifecycle, provider/tool/session/TUI architecture | `internals/architecture.md` |
| New feature or new `internal/` package | a docs page if it warrants detail, linked from `index.md`; a new package also gets a Repo map entry and a row here |

Rules for `docs/internals/` pages:

* Describe current behaviour only. No history, change narratives ("previously", "now", "legacy"), dates, issue numbers, measurement logs, or planned work; git and the issue tracker hold those.
* Verify every claim against code before writing it. Drop what you cannot verify.
* Cite symbols and file paths, never `file:line`.
* Each concept has one owning page; link to it instead of restating it. Link `docs/user/` pages instead of repeating user-facing behaviour.
* Keep pages compact: responsibilities, data flow, invariants, and non-obvious behaviour. No tutorials or essays.
* When renaming or removing a page or heading, update inbound links and anchors in docs, README, AGENTS.md, and Go comments.

Canon and consumer-file changes (`internal/prompt/system.go`'s `delegationInstructions`, `internal/prompt/specialists.go`'s `specialists` slice, or any of `skills/{implement,review,simplify,plan,pull-request}/SKILL.md`, `internal/oneshot/prompts/*.md`):

* Update docs/maintenance/canon-drift-checks.md if the change affects what counts as canon or the consumer file list.
* The `## Your sub-agents` table renders from the `specialists` slice in `internal/prompt/specialists.go` — edit the slice, never the markdown.
* Canon must not name a tool gated behind config independently of `delegation.enabled` (e.g. `advisor`); put such mentions in that tool's own preamble section.
* `internal/oneshot/prompts/{plan,review}.md` share four blocks verbatim with `skills/{plan,review}/SKILL.md`, pinned by `internal/oneshot/prompts_shared_test.go` — edit both copies and the test literal together.
* The `### Worktree Handling` block, the `### Pre-Commit Checklist` block, and the fix-delegation bullet list are each deliberately duplicated verbatim: the first two across `skills/{implement,review,simplify}/SKILL.md`, the fix-delegation list across `skills/{review,simplify}/SKILL.md`. All three are pinned by `skills/shared_blocks_test.go` — edit every copy and the test literal together, never one alone.

## Docs

Do not broadly load `docs/` or `.steiner/plans/`; read only the specific file or section needed. Prefer README plus nearby code for orientation.

Code exploration

If lsp_* tools are available, prefer them over bash, grep, and similar tools for exploring and examining Go code.
