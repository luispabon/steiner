## Request
Replace Steiner's turn-count-based compaction with token-budget-based compaction driven by the selected model's configured context window.

The requested configuration direction is now:
- no backward compatibility with the old singular `provider` block
- a top-level `scheduler` section for transport concurrency
- a `models:` map keyed by model alias
- a top-level `model` selector that references the alias in `models`
- per-model `context_size`, `max_completion_tokens`, and `compaction` settings

The feature needs to:
- estimate prompt/request token usage before each model call
- use backend-reported usage when available, then fall back to Steiner's internal tokenizer-based estimator when it is not
- reserve explicit completion headroom and configurable safety margin
- trigger model-based compaction when estimated request size approaches the model's context window
- remove the current recent-turn retention logic
- keep the beginning-of-conversation context in a compacted synthetic summary that the model sees on later requests
- make the compaction summary strong enough for small local models by preserving:
  - the original user request
  - the current solution design
  - recent actions already taken
  - future work already identified but not yet carried out

## Overview
This is a breaking architectural migration across config loading, runtime model selection, prompt budgeting, and loop compaction.

The current design is built around `RecentTurns` and post-turn dropping of older messages. That abstraction is wrong for the real failure mode. A short conversation can still overflow the window if it contains large tool output, bulky tool schemas, or accumulated context blocks. The replacement should treat compaction as request-budget management instead of conversation-length management.

The implementation should likely break into these pieces:
- config migration from `provider` to:
  - `scheduler.parallelism`
  - `models.<alias>`
  - `model`
- runtime model resolution so the CLI and TUI select a model alias, then resolve the model entry for:
  - transport config
  - `context_size`
  - `max_completion_tokens`
  - `compaction.safety_margin_tokens`
  - `compaction.summary_max_tokens`
- a built-in tokenizer-based estimator using one internal implementation choice, not a user-configurable tokenizer setting
- request budgeting that:
  - prefers backend usage/count information when the backend provides it
  - falls back to the internal estimator otherwise
  - compares prompt estimate plus reserved completion headroom plus safety margin against `context_size`
- a compaction trigger in the runner before the next model call, not only after a turn completes
- a model-based summarization path that replaces the dropped-history summary logic with a stronger structured summary optimized for weaker local models
- compaction messaging and session-health signaling that informs the user on every compaction event, warns on the second compaction, and strongly warns on the third and later compactions
- diagnostics and tests that report token-budget pressure and compaction decisions instead of turn-retention counts
- README and config sample updates that document only the new configuration shape

The request estimator must operate at the semantic chat/request level rather than by tokenizing the compact JSON wire payload. Counting transport wrapper syntax such as braces, field names, and other OpenAI-compatible marshaling details would make budget behavior depend on HTTP serialization instead of the prompt/tool content actually sent to the model. The definitive fallback estimator should therefore count:
- message contents and role framing
- tool schemas
- tool-call arguments
- tool results
- explicit overhead constants for chat-format bookkeeping where needed

It should not directly tokenize the marshaled wire JSON body.

The built-in estimator should be treated as a guardrail, not an oracle. Exact token parity across OpenAI-compatible backends is not realistic. The sane design is:
- explicit `context_size` in config
- explicit model-level safety margin
- backend usage when available
- internal tokenizer estimate as fallback
- conservative thresholds

That fallback estimate still needs to be stable at the semantic chat layer. It should be conservative because of explicit overhead constants and safety margin, not because transport JSON punctuation is being counted accidentally.

The compaction prompt needs special care. For small local models, generic summarization is not good enough. The summary must reconstruct working state, not just compress history. It should explicitly capture:
- original request and success criteria
- chosen or emerging solution design
- recent concrete actions
- unresolved constraints and decisions
- future work already planned
- anything that must survive to keep the next turn on track

The summary format should be fixed-heading structured prose with terse bullet points. That is a better fit for weaker local models than freeform prose, and less brittle than strict JSON.

Likely code areas:
- `internal/config/`
- `cmd/steiner/main.go`
- `internal/agent/`
- `internal/prompt/`
- `internal/provider/`
- `internal/output/`
- `README.md`
- `cmd/steiner/*_test.go`, `internal/config/*_test.go`, `internal/prompt/*_test.go`, `internal/agent/*_test.go`

## Verification Strategy

### Sources
- `AGENTS.md`
- `README.md`
- `Makefile`
- `go.mod`
- `internal/config/config.go`
- `internal/config/defaults.go`
- `internal/config/env.go`
- `internal/config/validate.go`
- `cmd/steiner/main.go`
- `internal/prompt/assembler.go`
- `internal/prompt/budget.go`
- `internal/prompt/retention.go`
- `internal/prompt/compaction.go`
- `internal/agent/loop.go`
- `.project_planning/2026-04-23_token-budget-compaction/research.md`

### Defaults
- execution_verification_timing: deferred_until_end_of_implementation
- reviewer_verification_timing: rerun_minimal_relevant_checks_first
- broad_expensive_checks_default: late_only
- repo_wide_formatting_allowed: false

### Commands

#### formatting
- preferred_mode: fix
- fix:
  - `gofmt -w <changed files>`
- check:
  - `gofmt -d <changed files>`
- use_check_only_when:
  - formatting only, no code behavior change is being validated
  - a targeted file-format pass is needed before running tests

#### targeted-unit-tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./internal/config ./internal/prompt ./internal/agent ./internal/provider ./cmd/steiner`
- use_check_only_when:
  - always, because tests are validation not mutation
  - run the targeted package subset first while config and compaction behavior are still moving

#### full-unit-tests
- preferred_mode: check
- fix:
  - none
- check:
  - `go test ./...`
- use_check_only_when:
  - always, because tests are validation not mutation
  - after targeted packages pass or when broad regression coverage is needed

#### static-analysis
- preferred_mode: check
- fix:
  - none
- check:
  - `go vet ./...`
- use_check_only_when:
  - always, because vet is validation not mutation

#### build-validation
- preferred_mode: check
- fix:
  - none
- check:
  - `go build ./...`
  - `make build-binaries`
- use_check_only_when:
  - always, because build commands are validation not mutation

### Tiers
- cheap:
  - formatting
- medium:
  - targeted-unit-tests
  - static-analysis
  - build-validation
- expensive:
  - full-unit-tests

### Required Boundaries
- step_level_exceptions:
  - none
- stage_level_exceptions:
  - none
- end_of_implementation:
  - formatting
  - targeted-unit-tests
  - full-unit-tests
  - static-analysis
  - build-validation
- reviewer_after_fix:
  - rerun the smallest targeted tests for the files changed in the last step first
  - broaden to `go test ./...` and `go vet ./...` if the change touches shared config, prompt assembly, provider wiring, or runner control flow

### Assumptions
- `scheduler.parallelism` is the only scheduler field currently required; additional queueing or timeout controls are not part of this feature unless implementation reveals a concrete need.
- `STEINER_MODEL` should continue to select the active model alias represented by top-level `model`, not a raw backend-side model name.
- the built-in tokenizer implementation will be an internal detail and not configurable by users.
- model-level `compaction.safety_margin_tokens` is the right place for the main estimation buffer because it scales with the selected model's context window and backend behavior.
- compaction will be triggered from the runner before a request is sent, not only after a turn completes.
- compaction should use the currently active model, not a separate summarization model.
- compaction warning thresholds should be code-level constants and internal policy, not user-facing config.

### Uncertainties
- how much post-call calibration Steiner should retain from backend-reported usage beyond diagnostics and logging
- the smallest useful set of explicit per-message and per-tool overhead constants needed to keep the semantic estimator conservative without coupling it back to wire-format details

## Decision Log
- Turn-count-based retention is the wrong abstraction and will be removed, not tuned.
- This is a breaking config change. No backward compatibility with the old `provider` block is required.
- `context_size` is a required explicit provider setting; Steiner should not depend on portable auto-discovery from OpenAI-compatible APIs.
- Steiner should use backend-reported usage when available and fall back to one built-in tokenizer-based estimator when it is not.
- The estimator is an internal implementation detail, not a configurable user-facing subsystem.
- The definitive fallback estimator should model semantic chat payload content and explicit bookkeeping overhead, not the compact marshaled JSON wire body.
- The compaction threshold should be based on:
  - estimated prompt tokens
  - reserved completion headroom from `max_completion_tokens`
  - model-level `safety_margin_tokens`
- The compaction summary must optimize for small local models by preserving request intent, solution design, recent actions, and pending future work.
- The config shape should use `models` and top-level `model`, not `providers` and `default_model`.
- Compaction warnings should be user-visible on every compaction event, with severity increasing on the second and third-plus compactions.
- Very old lineage generations should be pruned once they are permanently unusable for recompaction and a newer generation has taken over as the active source for future compaction.
