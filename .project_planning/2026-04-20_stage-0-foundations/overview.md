# Overview: Stage 0 — Foundations and Architecture Skeleton

## Request

Implement Stage 0 of the steiner roadmap: create the Go project skeleton and harden core interfaces before any agent behaviour exists. No agent loop, no tool execution, no provider calls — types, contracts, config loading, scheduler, and CLI stubs only.

Source documents: `docs/ROADMAP.md` §Stage 0, `docs/INITIAL_IMPLEMENTATION_PLAN.md` §Stage 0.

---

## Overview

### Module

`github.com/luispabon/steiner`, Go 1.24+.

### What gets built

**`go.mod` / `go.sum`**
- Module declaration, Go version, dependencies: `cobra`, `yaml.v3`, `golang.org/x/sync`

**`cmd/steiner/main.go`**
- Cobra root command
- `version` subcommand (prints version string)
- `config` subcommand (loads + prints resolved config as YAML)
- `--exec` flag stub (wired but noop at this stage)
- `--config`, `--model`, `--verbose` flag stubs

**`internal/config/`** — `config.go`, `defaults.go`, `env.go`, `validate.go`
- Full config struct matching the PRD schema (provider, limits, approval, sub_agent, tools, project_context, paths, logging)
- Compiled defaults
- 5-level merge: defaults → global (`~/.config/steiner/config.yaml`) → project (`.steiner/config.yaml`) → env vars (`STEINER_` prefix, `os.ExpandEnv`-style) → CLI flags
- Validation (required fields, range checks, unknown provider type)

**`internal/provider/`** — `interface.go`, `types.go`, `scheduler.go`
- `Provider` interface (`ChatCompletion`, `StreamChatCompletion`, `SupportsUsageStats`)
- Canonical `ChatRequest`, `ChatResponse`, `ChatChunk`, `Message`, `ToolCall`, `UsageStats` types
- `Scheduler` struct: semaphore over `golang.org/x/sync/semaphore`, enforces `provider.parallelism`, exposes `Acquire(ctx)`/`Release()`

**`internal/agent/`** — `types.go`, `state.go`, `limits.go`
- Canonical internal message type (user / assistant / tool / summary / context-block roles)
- `RunState` struct (turn counter, token counter, stop reason, conversation history)
- `Limits` struct (max turns, max tokens, tool timeout)
- `StopReason` enum (max_turns, max_tokens, cancelled, complete, error)

**`internal/tool/`** — `types.go`, `registry.go`, `schema.go`
- `ToolDef` struct (name, exec path, subcommand, description, param schema, timeout, approval mode)
- `Registry`: loads tool definitions from config
- `ToOpenAISchema(ToolDef) map[string]any`: generates OpenAI-compatible function schema JSON

**`internal/prompt/`** — `types.go`
- `ContextSource` enum (preamble, global_agents_md, project_agents_md, project_context, skill, conversation, tool_result, delegation_result)
- `ContextBlock` struct (source, content, byte size)
- No assembly logic

**`internal/output/`** — `log.go`
- `SetupLogger(level string) *slog.Logger`
- `EventSink` interface (`Emit(Event)`)
- `Event` struct (type, timestamp, payload)
- `NoopSink` implementation

### What does NOT get built in Stage 0
- No agent loop
- No provider implementation (only interface)
- No tool execution
- No prompt assembly
- No REPL
- No sub-agents

### Exit criteria
- `go build ./...` passes
- `steiner version` prints version
- `steiner config` loads, merges, and prints resolved config
- Scheduler semaphore blocks beyond configured parallelism (proven by unit test)
- Config precedence proven by unit tests
- All internal packages compile cleanly with no import cycles

---

## Verification Strategy

### Sources
- Fresh repo, no Makefile or CI yet — standard Go toolchain assumed
- `go.mod` will declare Go 1.24

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### format
- preferred_mode: fix
- fix:
  - `gofmt -w ./...`
- check:
  - `gofmt -l ./...`
- use_check_only_when:
  - CI lint pass (not applicable yet)

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always (no fix mode exists)

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
- use_check_only_when:
  - always

#### test
- preferred_mode: check
- fix:
  - none
- check:
  - `go test -race ./...`
- use_check_only_when:
  - always

### Tiers
- cheap:
  - format
  - vet
  - build
- medium:
  - test
- expensive: []

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - format
  - vet
  - build
  - test
- reviewer_after_fix:
  - Re-run `go build ./...` and `go test -race ./...` after any fix

### Assumptions
- Go 1.24 toolchain available in PATH
- No Makefile or CI config exists yet; verification is pure `go` toolchain
- `gofmt` (not `goimports`) is sufficient for Stage 0 — no import organisation tooling required yet

### Uncertainties
- Whether a `Makefile` should be scaffolded as part of Stage 0 or deferred — defaulting to defer

---

## Decision Log

| # | Decision | Rationale |
|---|----------|-----------|
| 1 | `cobra` for CLI | De facto Go standard for subcommand CLIs; best supported |
| 2 | `gopkg.in/yaml.v3` for config | Standard YAML library; manual merge gives full 5-level precedence control |
| 3 | `log/slog` for logging | stdlib since Go 1.21; structured, zero extra deps |
| 4 | `golang.org/x/sync/semaphore` for scheduler | Idiomatic, well-maintained, correct cancellation via context |
| 5 | `os.ExpandEnv`-style env interpolation | Simple, sufficient, user-confirmed |
| 6 | No `Makefile` in Stage 0 | Out of scope; pure `go` toolchain verification is enough |
| 7 | Prompt assembly types only in `internal/prompt` | Stage 0 locks the seam; assembly belongs in Stage 1 |
| 8 | `steiner-core-tools` binary deferred to Stage 1 | No tool execution in Stage 0; the binary has no purpose yet |
