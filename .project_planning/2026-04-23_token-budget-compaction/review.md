# Review Log

## Scope Reviewed
- `overview.md`, `plan.yaml`, `execution.md` artifacts reviewed
- Feature branch: `cl/2026-04-23_token-budget-compaction`
- Working tree: clean
- Files touched per diff: 23 files changed across config, agent, prompt, provider, output, and cmd/steiner

## Inputs Reviewed
- Planning folder: `.project_planning/2026-04-23_token-budget-compaction`
- Planning artifacts:
  - `overview.md` (217 lines)
  - `plan.yaml` (269 lines)
  - `execution.md` (301 lines)
- Feature branch: `cl/2026-04-23_token-budget-compaction`
- Verification runs performed: targeted-unit-tests, full-unit-tests passed

## Findings
- Review status: `pass`

### Blocking Findings
- None after fix applied.

### Non-Blocking Findings
- **NB-1**: `defaultRecentTurns = 4` in `internal/prompt/budget.go:15` is dead code but doesn't block handoff.

### Informational Findings
- **INF-1**: Pre-request token-budget checking IS implemented correctly at `loop.go:121`
- **INF-2**: Compaction uses the currently active model as required
- **INF-3**: Compaction IS triggered pre-request when `!fit.Fits`
- **INF-4**: Config now uses `scheduler.parallelism`, `model`, and `models.<alias>` per plan
- **INF-5**: Token-budget primitives implemented in `internal/prompt/token_budget.go`
- **INF-6**: Semantic estimator implemented in `internal/provider/token_estimator.go`

### Review Pass 2 Findings
- Review status: `pass`

#### Blocking Findings
- **BLK-2**: Fully merging preserved branch `tmp/stage-3-step-2-token-budget-compaction` regressed no-config startup because `defaultConfig()` now leaves `models["default"].model` empty while validation still requires every `models.<alias>.model`.
  - Evidence:
    - `internal/config/defaults.go:10-20` no longer sets a backend model for the compiled `default` alias.
    - `internal/config/validate.go:36-38` still rejects any model entry with an empty `model`.
    - `go test ./...` now fails in `cmd/steiner` and `internal/config` with `invalid config: models["default"].model is required`.
  - Impact:
    - `steiner config` and default runtime construction fail when no user config overrides the compiled default alias.
    - `config.Load()` fails even when callers select another alias, because validation walks every entry in `cfg.Models`.
  - Resolution:
    - Restored the compiled default backend model in `internal/config/defaults.go`.
    - Targeted rerun `go test ./internal/config ./cmd/steiner` passed.
    - Broad reruns `go test ./...`, `go vet ./...`, `go build ./...`, and `make build-binaries` passed.

#### Informational Findings
- **INF-7**: The preserved branch has now been merged back entirely via merge commit `f5a3ff8`.

## Fix Plan
(N/A - pass)

### Review Pass 2 Proposed Fix Plan
- Restore a valid compiled default backend model in `internal/config/defaults.go` so merged defaults remain compatible with existing validation and startup flows.
- Keep the already-merged README/config-format changes unless the code fix proves they are now materially misleading.
- Rerun the smallest relevant checks first: `go test ./internal/config ./cmd/steiner`.
- If that passes, rerun the broad verification set used for handoff: `go test ./...`, `go vet ./...`, `go build ./...`, and `make build-binaries`.

## Fixes Applied
- **BLK-1**: Removed turn-count retention from live execution path
  - File: `internal/agent/loop.go`
  - Change: Removed `base.Policy.Retention.RecentTurns = countTurns(conversation) + 1` from `assemblyOptions()`
  - Commit: `a831a7c`
  - Verification: `go test ./internal/agent ./internal/prompt` passed
- **BLK-2**: Restored a valid compiled default backend model after fully merging preserved branch `tmp/stage-3-step-2-token-budget-compaction`
  - File: `internal/config/defaults.go`
  - Change: Restored `Model: "qwen3-35b-a3b"` for the compiled `default` alias so startup defaults remain valid under current config validation.
  - Commit: pending
  - Verification: `go test ./internal/config ./cmd/steiner` passed, followed by `go test ./...`, `go vet ./...`, `go build ./...`, and `make build-binaries` passing

## Verification
- Post-fix targeted tests: `go test ./internal/agent ./internal/prompt ./internal/config ./cmd/steiner` passed (65 tests)
- Full unit tests: `go test ./...` passed (203 tests)
- Static analysis: `go vet ./...` passed
- Build validation: `go build ./...` passed
- Review pass 2 targeted tests: `go test ./internal/config ./cmd/steiner` passed
- Review pass 2 full unit tests: `go test ./...` passed
- Review pass 2 static analysis: `go vet ./...` passed
- Review pass 2 build validation: `go build ./...` passed
- Review pass 2 binary build: `make build-binaries` passed

## Final Status
- Review status: `pass`
- Branch: `cl/2026-04-23_token-budget-compaction`
- Working tree: reviewer-owned updates pending commit
- Review-pass commits: `a831a7c` (review-fix 1), `f5a3ff8` (full merge of preserved branch), reviewer-owned follow-up commit pending for `BLK-2` fix and `review.md`
- Sub-agents closed: none spawned by reviewer

### Review Pass 2 Status
- Review status: `pass`
- Branch: `cl/2026-04-23_token-budget-compaction`
- Merge under review: `f5a3ff8`
- Working tree state at finding time: clean before `review.md` update
- User approved the reviewer fix plan for `BLK-2`
- Finaliser handoff readiness: pending reviewer-owned commit and final clean-tree check
