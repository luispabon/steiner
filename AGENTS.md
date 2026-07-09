# steiner - agent manual

`steiner` is a minimal local-first Go coding agent. Optimize for bounded context, explicit tool use, and safe local execution.

## Project Overview

`cmd/steiner` composes the CLI and wires packages together. `internal/agent` runs the loop, `internal/prompt` assembles context, `internal/tool` executes bounded tools, `internal/output` shapes terminal and machine-readable events, and `internal/tui` owns the interactive UI.

## Repo map

```text
cmd/steiner/             CLI entry and subcommands
internal/agent/          Loop orchestration, state, limits
internal/config/         Config loading, merging, validation, defaults
internal/provider/       Model transport and scheduler
internal/tool/           Registry, schema, policy, executor, output shaping
internal/prompt/         Context gathering, budgeting, assembly, compaction
internal/skill/          Skill discovery and loading
internal/tui/            Interactive TUI
internal/delegation/     Delegation contracts and scaffolding
internal/output/         Terminal and machine-readable event output
internal/history/        Conversation history persistence
testdata/stage3/         Integration test fixtures
docs/                    Product/design docs and implementation notes
````

## Boundaries and invariants

* Keep package boundaries strict.
* `cmd/steiner` is the composition root; it wires dependencies and flags only, and must not accumulate business logic.
* `internal/agent` must not bypass provider abstractions or scheduler parallelism.
* `internal/prompt` owns context assembly and stays separate from execution.
* Context assembly order is intentional; update `internal/prompt` tests when changing precedence.
* `internal/config` owns config loading and merging.
* Core tools use structured JSON input/output only.
* Tools must keep output bounded and summarized.
* Skills are auxiliary context; they do not override user instructions, tool policy, repo code, or this file.
* Sub-agents receive only explicitly passed context and cannot nest.
* Mutation tools are approval-gated by default.
* Prompt cache integrity depends on prefix stability. Keep static sources (preamble, agents, project context, skills) before dynamic sources (conversation, tool summaries); do not reorder `internal/prompt/source_plan.go`.
* Do not introduce per-turn non-determinism into the prompt prefix (preamble, tool definitions, skills, project context). The system preamble is memoized per session by `CachedSystemPreamble`.
* Keep tool definition ordering deterministic (`internal/tool/registry.go` sorts by name; do not let filtered subsets depend on map iteration order).
* Do not remove or rename provider cache hints: Codex `session-id`/`thread-id` headers (`internal/provider/codex_responses.go`), Anthropic `cache_control` breakpoints (`internal/provider/anthropic_wire.go`), or the `PromptCacheKey` assignment rules.
* Compaction intentionally invalidates the summarized portion of the prefix.

## File organisation

* Use snake_case Go filenames.
* Put tests next to source.
* Aim for production `.go` files under ~300 lines; split around ~500 by domain responsibility.
* The ~500-line target is a trigger to review, not an absolute cap. A file that is a single cohesive concern (e.g. one render pipeline of small functions) may exceed it; when it does, note in the PR that the breach is accepted for cohesion rather than splitting mechanically.
* In `internal/tui`, keep render, update, sidebar, and event-state concerns split before files drift past the line target.
* In `internal/output`, keep render, event, and preview/report concerns split before files drift past the line target.
* Do not create `util`, `helper`, or `common` packages. Put shared code in the package that owns the domain.

## Work loop

1. Inspect nearby code, call sites, and the smallest relevant tests before editing.
2. Prefer `mutate` for file mutations; do not expose `apply_patch`, `write`, or `edit` to agents.
3. Keep changes minimal and package boundaries intact.
4. Ensure comprehensive unit and functional tests are written for any new functionality
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
* Define interfaces at the consumer, keep them small, and avoid header interfaces.
* Add nearby tests for new or changed behavior under `internal/`.
* Use `testdata/` for fixtures; avoid large inline literals.
* Use `0o` octal literals.
* Do not shadow builtins such as `close`, `max`, or `min`.
* Keep symbols unexported unless cross-package use requires export.
* Exported symbols in `internal/` must be justified by cross-package use and documented immediately with Godoc starting with the symbol name.
* TODO comments must name the follow-up action or owner, not leave open-ended debt markers.
* Do not commit no-op or stub functions that silently do nothing (e.g. a validator whose body is only `_ = arg`). Either implement the behaviour, remove the function, or leave a comment naming the concrete follow-up — the same standard already applied to TODO comments.

## Security

* Never hardcode or commit provider credentials.
* Treat `--log-file` and `STEINER_LOG_FILE` as sensitive; they may capture prompts/tool output.
* Default local provider URL: `http://localhost:11434/v1`.

## Documentation maintenance

Code changes must update corresponding documentation in a single commit:

1. **`internal/tool` changes** (add/remove/rename built-in tool):
   * Update the "Built-in tools" section in README.md
   * Update sub-agent tool allowlist tables in docs/sub-agent-delegation.md if the tool appears in any allowlist

2. **`internal/config` changes** (add/change/remove Config field or nested struct field):
   * Update the relevant field entry in docs/configuration.md
   * Update defaults section if default values change
   * Update config examples in README.md if a commonly-used field is affected
   * If the field is a user-facing prompt-injection toggle (e.g. `cave_human`), update the relevant section in docs/optional-features.md and the one-liner in README.md's "Other features" list

3. **`internal/delegation` changes** (add/remove sub-agent type or change tool allowlist):
   * Update the sub-agent types table in docs/sub-agent-delegation.md
   * Update docs/sub-agent-delegation-internals.md if the architecture, bootstrapping, or tool construction changes
   * Update the sub-agent delegation tool table in README.md

4. **`internal/prompt` changes** (change compaction, budgets, or context management behaviour):
   * Update docs/context-management.md if user-facing behaviour changes
   * Update docs/context-management-internals.md with new or changed internal behaviour
   * Update the "Context management" section in README.md if the high-level description changes

5. **New top-level feature**:
   * Add a feature section to README.md
   * Create a corresponding doc under docs/ if the feature warrants detailed documentation
   * Add a rule to this maintenance section

6. **`internal/oneshot` changes** (add/change/remove engine behaviour, phases, manifest fields, lock/resume logic, closeout, reports):
   * Update docs/oneshot.md if user-facing behaviour changes
   * Update docs/oneshot-internals.md with new or changed internal behaviour
   * If a config field is added/changed, also update docs/configuration.md and the `oneshot` config example in README.md

7. **`internal/usagestats` changes** (add/change/remove recorder, bucketing, persistence/schema, retention, windows, surfacing behaviour):
   * Update docs/cache-stats.md with the changed behaviour
   * Update the "Cache hit rate tracking" section in README.md if the high-level behaviour changes

8. **`internal/notify` changes** (add/change platform driver, change Service behaviour):
   * Update docs/desktop-notifications.md if user-facing behaviour changes
   * Update docs/desktop-notifications-internals.md if driver interface or extension points change
   * Update the "Desktop notifications" section in README.md if the high-level description changes

9. **`internal/oauth` changes** (add/change flow, token store, refresh, or PKCE behaviour):
   * Update the "Codex OAuth" section in docs/optional-features.md if the setup steps or token storage location change
   * Update docs/configuration.md provider types table if authentication behaviour changes

10. **Optional feature changes** (cave_human, accent colour, web search, image paste, conversation forking, code simplification, Codex OAuth):
   * Update the relevant section in docs/optional-features.md
   * Update the one-liner in README.md's "Other features" list if the summary changes

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
- `advisor` — ask a stronger-model steering advisor for guidance (requires `advisor.enabled`)
- `workflow_handoff` — transition to a different workflow with approved artifacts

Steiner owns the schemas and result formats. Dive implements the behavior.

## Docs

Do not broadly load `docs/` or `.steiner/plans/`; read only the specific file or section needed. Prefer README plus nearby code for orientation.

