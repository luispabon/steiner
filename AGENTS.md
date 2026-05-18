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

## File organisation

* Use snake_case Go filenames.
* Put tests next to source.
* Aim for production `.go` files under ~300 lines; split around ~500 by domain responsibility.
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
	make quick-check
	make check
	make ci-check
	make build-binaries
	```
Before finalizing Go changes, run `make quick-check` at minimum. For larger changes, run `make check`. Before merging, run `make ci-check` where practical. If a check cannot run, report the exact command and failure.

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

## Security

* Never hardcode or commit provider credentials.
* Treat `--log-file` and `STEINER_LOG_FILE` as sensitive; they may capture prompts/tool output.
* Default local provider URL: `http://localhost:11434/v1`.

## Built-in tools

Steiner exposes these model-facing built-in tools:

- `read` — read files with offset/limit pagination
- `mutate` — apply one or more structured file mutations atomically (create, write, replace, line_replace, delete, move)
- `glob` — find files by pattern
- `grep` — search file contents with context
- `ls` — list directories
- `bash` — run shell commands
- `scratchpad` — record working state (intent, decisions, next action); persists across compaction
- `display_file` — show a file in the TUI overlay without adding contents to conversation

Steiner owns the schemas and result formats. Dive implements the behavior.

## Docs

Do not broadly load `docs/` or `.project_planning/`; read only the specific file or section needed. Prefer README plus nearby code for orientation.
