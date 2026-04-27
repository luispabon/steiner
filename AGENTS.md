# steiner — agent manual

`steiner` is a minimal, local-first coding agent in Go. Work here is optimized for bounded context, explicit tool use, and safe local execution against real repositories.

## Repo map and boundaries

```text
cmd/steiner/            CLI entry and subcommands
cmd/steiner-core-tools/ Core tools binary
internal/agent/         Loop orchestration, state, limits
internal/config/        Loading, merging, validation, defaults
internal/provider/      Model transport and scheduler
internal/tool/          Registry, schema, policy, executor, output shaping
internal/prompt/        Context gathering, budgeting, assembly, compaction
internal/skill/         Skill discovery and loading
internal/tui/          Interactive TUI
internal/delegation/    Delegation contracts and scaffolding
internal/output/        Terminal and machine-readable event output
internal/history/       Conversation history persistence
testdata/stage3/         Test fixtures for integration tests
docs/                   Product/design docs and implementation notes
```

Keep package boundaries strict:

* `internal/agent` does not reach into provider transport details directly.
* `internal/prompt` stays separate from agent execution.
* `internal/config` owns config loading and merging.
* Core tools speak JSON input/output; do not mix in ad hoc contracts.

## File organisation

- Keep production `.go` files under 300 lines; split at 500 lines.
- Name files with snake_case (`foo_bar.go`), not camelCase.
- Place tests alongside source (`foo_test.go` next to `foo.go`).

## Build, test, verify

Use these exact commands when they apply:

```bash
go build ./...
go test ./...
go vet ./...
gofmt -w <files>
make build-binaries
go test ./path/to/pkg -run TestName
```

Go version is `1.24`.

## Working loop

1. Inspect nearby code first, then the smallest relevant test files.
2. Keep package boundaries intact while you change code.
3. Prefer targeted tests for changed packages, then broaden to `go test ./...` when the risk justifies it.
4. Run `gofmt -w <files>` before finishing any Go edit.
5. Prefer the `edit` tool over `write` for in-place mutations in steiner.

### Change workflow

Each change must leave `go build ./...` and `go test ./...` passing.
Run `gofmt -w` before finishing any edit.
Prefer `edit` for in-place mutations.

#### Anti-patterns
Do not create `util`, `helper`, or `common` packages. Place shared helpers in the package that owns the domain (e.g. JSON cloning lives in `internal/tool`).

## Coding and testing conventions

* Favor table-driven tests.
* Thread `context.Context` through provider and tool calls.
* Return errors instead of panicking, except at process boundaries for unrecoverable failures.
* Keep tool output bounded and summarized; do not let context grow linearly with tool chatter.
* Use `testdata/` for fixtures and keep large test data out of inline literals.
* **Error handling**: Wrap every error with `fmt.Errorf("<action>: %w", err)` using a lowercase verb phrase. Never swallow errors with `_ = someFunc()` in production paths. Use sentinel errors sparingly; prefer wrapped errors.
* **Imports**: Group imports: stdlib, blank-line, third-party, blank-line, internal (`github.com/luispabon/steiner/...`).
* **Interfaces**: Interfaces are defined by the consumer, not the implementor. Keep interfaces small (1–3 methods). Avoid header interfaces.
* Every package under `internal/` must have tests. Currently `history`, `tui/theme`, and `tui/prefs` are gaps to close.
* Favour table-driven tests. Thread `context.Context` through testable helpers.
* **File modes**: Use `0o` prefix for octal literals (`0o755`, not `0755`).
* **Builtins**: Do not shadow builtin identifiers like `close`, `max`, `min`.
* **Unexported-by-default**: If a symbol has no cross-package consumers, unexport it. Move tests to the internal package if needed.
* **Godoc**: Every exported symbol must have a comment starting with its name.

## Security and operational hazards

* Never hardcode or commit provider credentials.
* Treat `--log-file` and `STEINER_LOG_FILE` as sensitive; they can capture prompts and tool activity.
* The default local provider URL is `http://localhost:11434/v1`.
* Mutation tools are approval-gated by default.

## Architecture invariants

* Context source precedence is fixed: system preamble, global `~/.config/steiner/AGENTS.md`, project `./AGENTS.md`, auto-discovered project context, user-invoked skills, active conversation history, tool results, delegated sub-agent payloads.
* Provider access must go through the central scheduler; do not bypass `scheduler.parallelism`.
* Tools use structured JSON input and output.
* Skills are auxiliary context, not system authority.
* Sub-agents are isolated, receive only explicitly passed context, and cannot nest.

## Docs guidance

* Do not broadly load `docs/` or `.project_planning/`.
* Read only the specific file or section you need for the task.
* Prefer README plus nearby code first when you are orienting yourself.
