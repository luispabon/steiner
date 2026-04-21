## Request

Plan the work for `# Stage 4 - Console UX foundations` using only the Stage 4 sections from `docs/ROADMAP.md` and `docs/INITIAL_IMPLEMENTATION_PLAN.md` as planning scope, plus a targeted research pass on third-party Go libraries relevant to terminal input and output.

The intended outcome is a Stage 4 plan that improves the interactive terminal experience without changing the single-agent architecture, and that is willing to entertain a modest spec adjustment if the best library-backed approach justifies it.

## Overview

Stage 4 should be planned as a focused console-surface upgrade centered on `internal/repl`, `internal/output`, and `cmd/steiner/main.go`, with no changes to prompt assembly, agent state semantics, or provider responsibilities.

The strongest implementation direction is:

- use a dedicated line-editing library in `internal/repl` instead of extending the current `bufio.Reader` loop
- keep `internal/output` as the rendering boundary for terminal-facing events and assistant text
- preserve the existing event model as the canonical runtime path, but make the terminal renderer streaming-aware and channel-aware
- avoid full TUI frameworks that would introduce a second application state/render loop

Research indicates the best candidate stack is:

- `github.com/reeflective/readline` for prompt editing, cursor motion, and history navigation
- `github.com/charmbracelet/lipgloss` plus `github.com/muesli/termenv` for styled, terminal-aware output
- optionally `github.com/charmbracelet/log` for status-like non-assistant channels if it simplifies structured rendering without taking over transcript rendering

The current Stage 4 docs should be softened in one narrow place: instead of treating the current event/output model as fixed in shape, Stage 4 should allow an explicit terminal output contract that classifies rendered traffic into channels such as `assistant`, `status`, `tool`, `approval`, and `error`. That is still consistent with the roadmap constraint that richer rendering must not fork the runtime path, because the event model remains canonical and provider code remains terminal-agnostic.

Planned implementation shape:

- `internal/repl`
  - replace the line-buffered `ReadString('\\n')` loop with a real prompt/input abstraction
  - preserve existing command parsing, skill toggling, and completion behavior
  - ensure prompt refresh works correctly when streamed output or status lines appear while the user is in interactive mode
- `internal/output`
  - evolve the current `Stream` into a renderer that distinguishes assistant replies from status/event output
  - add streaming-capable assistant rendering that can incrementally append content without corrupting prompt state
  - improve approval and tool event formatting for scanability while keeping truncation and important arguments visible
  - define default dark theme tokens/styles here rather than scattering ANSI decisions through the REPL or provider layers
- `cmd/steiner/main.go`
  - wire interactive mode so the richer prompt/input layer and richer output renderer share the same terminal session cleanly
  - keep `--exec` consistent in semantics, but permit a simpler non-interactive rendering path when prompt-management behavior is unnecessary

Key planning risks:

- accidentally introducing a full-screen TUI architecture rather than a prompt-plus-renderer architecture
- allowing prompt refresh concerns to leak into provider code or agent logic
- hiding approvals or truncation signals behind overly decorative rendering
- making interactive behavior depend on terminal assumptions that degrade badly on constrained local setups

Open decisions to carry into detailed planning:

- whether Stage 4 should include history persistence to disk or keep history in-memory only
- whether `charmbracelet/log` should be adopted, or whether custom rendering on top of `lipgloss` is cleaner
- whether dark styling should be fixed by project convention only, or adapt when `termenv` can safely detect terminal capabilities/background

## Verification Strategy

### Sources
- `AGENTS.md`
- `Makefile`
- current package test layout under `cmd/` and `internal/`
- local validation run of `go test ./...`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: true

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed-go-files>`
- check:
  - `gofmt -d <changed-go-files>`
- use_check_only_when:
  - the user explicitly wants diff-only verification before applying formatter changes
  - a step is gathering signal only and should avoid unrelated formatting churn before implementation stabilises

#### vet
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always, because `go vet` is diagnostic-only

#### unit-and-integration-tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always, because `go test` is diagnostic-only

#### build
- preferred_mode: check
- fix:
  - none
- check:
  - `make build-binaries`
  - `go build ./cmd/steiner ./cmd/steiner-core-tools`
- use_check_only_when:
  - always, because build validation is diagnostic-only

### Tiers
- cheap:
  - formatting
- medium:
  - vet
  - unit-and-integration-tests
  - build
- expensive:
  - none

### Required Boundaries
- step_level_exceptions:
  - rerun targeted package tests when changing command parsing, completion, or stream formatting behavior in isolation
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - vet
  - unit-and-integration-tests
  - build
- reviewer_after_fix:
  - rerun the minimal relevant package tests first
  - rerun broader end-of-implementation checks if reviewer fixes touch shared console/output wiring

### Assumptions
- `gofmt` applies to the changed Go files only; repo policy does not require whole-repo formatting on every change
- `go test ./...` remains a practical default validation command for this repository
- no separate CI-only lint or E2E command is currently required by repo policy

### Uncertainties
- there is no discovered CI configuration in the repository root to confirm whether future workflow checks will add stricter requirements
- final dependency additions for Stage 4 may justify narrower package-level test commands during execution before broad validation at the end

## Decision Log

- Research was approved before overview creation because terminal library choice could materially change the Stage 4 implementation shape.
- Research outcome: prefer a lightweight prompt library plus lightweight styling/rendering libraries, not a full TUI framework.
- Leading library recommendation:
  - input: `reeflective/readline`
  - rendering: `lipgloss` + `termenv`
  - optional structured status output: `charmbracelet/log`
- Proposed spec adjustment: permit Stage 4 to define explicit terminal output channels and make prompt-refresh ownership part of the renderer/input contract, while keeping the event model canonical and provider code unaware of terminal mechanics.
- No broader scope change is proposed: Stage 4 remains console UX work only, with no delegation, GUI, or runtime-path fork.
