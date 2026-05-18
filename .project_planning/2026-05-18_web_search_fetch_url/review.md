# Review — web_search / fetch_url

## Scope Reviewed

- Branch: `cl/2026-05-18_web_search_fetch_url`
- Planning folder: `.project_planning/2026-05-18_web_search_fetch_url`
- Files reviewed: `internal/tool/builtin/brave_search.go`, `fetch_url.go`, `web_search.go`, `search_backend.go`, `input.go`, `schema.go`, `builtins.go`, `internal/config/validate_search.go`, `internal/config/config.go`, `internal/tool/types.go`, `internal/tool/approval.go`, `cmd/steiner/runner.go`, plus all test files for the above.

## Inputs Reviewed

- `overview.md` — original intent, acceptance, verification strategy, decision log
- `plan.yaml` — approved implementation contract (5 stages, stage-1-step-1 through stage-3-step-1)
- `execution.md` — all steps complete, vp001 passed (0 lint, all tests pass)
- `research.md` — background assumptions
- `fix_plan_verification_pass_001.md` — executor lint fix record

## Findings

### F-001 — fetch_url missing `Approval: ApprovalModeAuto` [blocking]

**Evidence:**

`internal/tool/builtin/fetch_url.go` — `NewFetchURLTool` returns a `tool.ToolDef` with no `Approval` field set (zero value `""`).

`internal/tool/approval.go:51`:
```go
func IsApprovalPrompt(mode config.ApprovalMode) bool {
    return mode == config.ApprovalModePrompt || mode == ""
}
```

`internal/tool/approval.go:18-28` — `ResolveApprovalMode` fallback chain: `def.Approval != ""` → tool override → `cfg.Approval.Default` → `ApprovalModeAuto`. If a user sets `approval.default = "prompt"`, `ResolveApprovalMode` returns `"prompt"` for fetch_url (since `def.Approval == ""`), meaning every fetch_url call requires manual approval.

**Plan constraint** (`plan.yaml` stage-2-step-2): "Approval mode auto".

**Comparator**: `web_search.go` correctly sets `Approval: config.ApprovalModeAuto`, which causes `ResolveApprovalMode` to return `"auto"` unconditionally.

**Impact**: fetch_url behaves correctly only when user has default or auto approval config. Deviates from plan for users with `approval.default = "prompt"` or `"deny"`.

### F-002 — SSRF acceptance criterion not tested [non_blocking]

**Evidence**: plan.yaml stage-2-step-2 acceptance: "SSRF protection blocks private IPs". `fetch_url_test.go:97-105` attempts a request to `http://localhost:65534/nonexistent` but doesn't assert that `SafeHTTPClient` specifically blocked it (test logs non-200 as acceptable, doesn't assert error type). The protection is in the implementation (`toolkit.SafeHTTPClient` is wired correctly), but no test asserts the blocking behaviour.

**Impact**: test coverage gap only; implementation is correct.

### F-003 — SearxngSearcher exported, braveSearcher unexported — style inconsistency [informational]

**Evidence**: `braveSearcher` was unexported as a deviation during vp001 to satisfy revive. `SearxngSearcher` and `NewSearxngSearcher` remain exported with doc comments (revive passes). Both are only used within the `builtin` package. Inconsistent style but lint is clean and no correctness impact.

## Fix Plan

### Consolidated reviewer fix pass (F-001, F-002, F-003)

#### F-001 — fetch_url.go

`internal/tool/builtin/fetch_url.go`: add `Approval: config.ApprovalModeAuto` to the `tool.ToolDef` literal in `NewFetchURLTool`. Add `"github.com/luispabon/steiner/internal/config"` import. One field, one import. No other changes.

#### F-002 — fetch_url_test.go

`internal/tool/builtin/fetch_url_test.go`: add test case within `TestFetchURLTool` asserting that a private IP URL (e.g. `http://192.168.1.1/`) returns a `*FetchURLError` result (not a hard error), with a non-empty `Error` field. Confirms `toolkit.SafeHTTPClient` SSRF blocking surfaces as structured error, matching plan acceptance: "SSRF protection blocks private IPs" + "Network errors return structured error result".

#### F-003 — search_backend.go

`internal/tool/builtin/search_backend.go`:
- Rename `SearxngSearcher` struct → `searxngSearcher` (unexported, matching `braveSearcher`)
- Change `NewSearxngSearcher` return type from `(*SearxngSearcher, error)` to `(web.Searcher, error)` (matching `NewBraveSearcher`)
- Update constructor body to return the interface
- Update `Search` receiver from `*SearxngSearcher` → `*searxngSearcher`
- Update doc comments

`search_backend_test.go`: no changes needed — `wantType` strings (`"*SearxngSearcher"`, `"*BraveSearcher"`) are defined in the test struct but only compared against `"<nil>"` in the assertion loop (lines 88-96). Type names are never compared via `%T`.

**Verification after fix pass**:
- `go build ./...`
- `go test ./internal/tool/builtin/...`
- `golangci-lint run ./internal/tool/builtin/...`

**Fixes**: F-001, F-002, F-003

**Approved**: yes

---

## Fixes Applied

| Fix | Files | Notes |
|-----|-------|-------|
| F-001 | `fetch_url.go`, `builtins_test.go` | Added `Approval: config.ApprovalModeAuto` + `config` import. Companion: updated `alwaysAuto` map in builtins_test to include `fetch_url`. |
| F-002 | `fetch_url_test.go` | Added `t.Run("SSRF blocked private IP returns structured error result")` using `http://192.168.1.1/`, asserts `*FetchURLError` with non-empty Error field. |
| F-003 | `search_backend.go` | Renamed `SearxngSearcher` → `searxngSearcher`, `NewSearxngSearcher` return type → `(web.Searcher, error)`. No test changes needed. |

Commit on temp branch: `0c90b89`
Merged into feature branch via: `review: merge fix-pass-001 (fetch_url approval, SSRF test, SearxngSearcher unexport)`
Worktree and temp branch deleted.

## Fixes Applied

_pending_

## Verification

| Run | Scope | Result |
|-----|-------|--------|
| post-fix-pass-001 | `go build ./...` | PASS |
| post-fix-pass-001 | `go test ./internal/tool/builtin/...` | PASS |
| post-fix-pass-001 | `golangci-lint run ./internal/tool/builtin/...` | PASS (0 issues) |

## Final Status

`pass_with_notes` — all blocking findings resolved. Non-blocking F-002 also resolved with dedicated SSRF test. F-003 (informational, style) resolved. No remaining findings.

Finaliser handoff ready.
