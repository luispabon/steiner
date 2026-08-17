# Execution — 2026-08-17 parallel delegation

## Active branch
`cl/2026-08-17_parallel_delegation` — clean at start, checked out in main worktree.

## Verification strategy (loaded from overview.md)
- Targeted first: `gofmt`, `go build ./...`, `go vet ./...`, `go test ./internal/<pkg>`.
- Load-bearing: `go test -race ./internal/agent/... ./internal/delegation/... ./internal/interactive/...` on every concurrency-touching step.
- Per-step commands from plan.yaml `verification` fields.
- Final before closeout: `make check`. `make test-perf` for steps touching tui/agent.

## Steps
| id | title | state |
|----|-------|-------|
| step-1 | Remove inert provider.Scheduler machinery | complete |
| step-2 | Replace scheduler config with sub_agents.max_parallel | complete |
| step-3 | Concurrent tool-call execution in the agent turn loop | complete |
| step-4 | Wire delegation-tool eligibility at the composition root | complete |
| step-5 | Queue concurrent approval requests in ApprovalCoordinator | complete |
| step-6 | Preamble worktree guidance and documentation | complete |
| step-7 | End-to-end concurrent delegation tests | complete |

## Delegated agents
- child-1: step-1 initial attempt (failed: line-shift mutation churn; worktree restored clean, no commit)
- child-2: step-1 implementation (commit b0669dce on tmp branch; merged as fast-forward)
- child-3: step-1 review (found 3 lost non-scheduler tests; re-review passed after fix)
- child-4: step-1 fix (restore tests in openai_compat_response_test.go, commit 0d707815)
- child-5: step-2 initial attempt (aborted: mutate resolved relative paths against main checkout; agent restored both trees, no commit)
- child-6: step-2 implementation (commit 7adc9a56; amended to f4475d18 after executor reverted out-of-scope gap-report doc edit)
- child-7: step-2 review (2 findings: strict-decode assertion too weak; stale case name; re-review passed)
- child-8: step-2 fixes (771dd86d: stronger unknown-field assertion + renamed validation case)
- child-9: step-3 implementation (3f6baf06 on tmp branch)
- child-10: step-3 review x3 (round 1: 4 defects — width-1 parallel path, unbounded spawn, event-sink race, test gaps; round 2: started-event-for-unstarted-calls + 2 non-discriminating tests; round 3: pass)
- child-11: step-3 fixes round 1 (276e4cb7: serial width-1, acquire-before-spawn, main-goroutine started events, 10 discriminating tests)
- child-12: step-3 fixes round 2 (70407cab: started events only for executed calls; rendezvous-based handoff/failure tests)
- child-13: step-4 implementation (68db26a7: IsDelegationTool predicate + parent wiring + child pin; merged fast-forward)
- child-14: step-4 review (pass, no findings)
- child-15: step-5 implementation (eda4048f: FIFO approval queue + agent tagging + TUI suffixes)
- child-16: step-5 review x4 (round 1: 5 findings — TUI queued-request discard, reversed depth, Submit/Finish race, 2 test gaps; round 2: pill promotion unreachable, depth still wrong, stale depths, TUI tests missing, flaky concurrent test; round 3: nested pill branch unreachable; round 4: pass)
- child-17: step-5 fixes round 1 (69e21047: queued promotion, mutex-held submit, concurrent Begin + race tests)
- child-18: step-5 fixes round 2 (76ba8860: head-relative depth + recompute, sibling pill promotion, TUI tests, de-flaked concurrent test)
- child-19: step-5 fixes round 3 (cf9178a6: pill promotion branch sibling fix + regression test)
- child-20: step-6 explore (canon pinning, scheduler refs, doc structure; delegation section in system.go confirmed free text, not pinned)
- child-21: step-6 implementation (a48d6ef0: preamble worktree guidance, delegation docs, README, AGENTS/CLAUDE invariant, runner_test window widening; amended 4x incl. review fixes)
- child-22: step-6 review (2 major findings: docs used nonexistent plural config path sub_agents.max_parallel; fixed by child-21), executor caught 2 extra defects after review (race-test regression; broken luisl/steiner issue URL)
- child-23: step-7 implementation (6d843358) + hardening (beb79205) + reverse-release fix (9cff2841); stalled on edit drift in round-3 follow-up (restored, no commit)
- child-24: step-7 review round 1 (7 findings)
- child-25: step-7 review round 2 (gap A ordering release sequencing, gap B failure-run deadline)
- child-26: step-7 review round 3 (child-side ack not happens-after)
- child-27: step-7 event gating (fe38183b), summary-served gating (59bec8e1), honest comment (70fcd268)
- child-28: step-7 review round 4 (DelegationComplete precedes summary)
- child-29: step-7 review round 5 (summary-served precedes provider return; residual irreducible; comment fix demanded)
- child-30: lint cleanup 9ab13dc3 (gocyclo refactor appendToolGroupApprovalQueueDepths, dropped always-nil error return, De Morgan, struct removals, S1000)
- child-31: lint-cleanup review (1 false-positive finding — verified false via git show; merged)
- child-32: final make check gate (all green)

## Verification results
- step-1: go build ./... OK; go test ./internal/provider/... ./cmd/steiner/... OK; go vet ./... OK. Merged fast-forward 0d707815. Worktree + temp branch cleaned.
- step-2: go build ./... OK; go test ./internal/config/... ./cmd/steiner/... OK; go vet OK. Merged fast-forward 771dd86d. Worktree + temp branch cleaned.
- step-3: go build ./... OK; go test -race ./internal/agent/... OK; vet OK; make test-perf OK (agent perf ceilings hold). Merged fast-forward 70407cab. Worktree + temp branch cleaned.
- step-3 deviations (review-enforced, interface unaffected): ToolCallStarted emission moved from invokeTool to the batch spawn loop / serial caller (plan sketch had invokeTool emit; concurrent emission races non-thread-safe event sinks); semaphore acquired before goroutine spawn (bounded live goroutines); width 1 takes literal serial path.
- step-4: go build ./... OK; go test -race ./internal/delegation/... ./cmd/steiner/... OK; go vet OK. Merged fast-forward 68db26a7. Worktree + temp branch cleaned.
- step-5: go build ./... OK; go test -race ./internal/interactive/... ./internal/tui/... ./internal/agent/... OK; vet OK; make test-perf OK. Merged fast-forward cf9178a6. Worktree + temp branch cleaned.
- SESSION STOP (user request): implementation halted after step-5. steps 6-7 pending. Feature branch `cl/2026-08-17_parallel_delegation` clean at cf9178a6; no worktrees/branches left behind. Resume = rerun implement workflow on this folder.
- Naming deviation: plan's `sub_agents.max_parallel` shorthand implemented as `sub_agent.max_parallel` (existing singular config block); env var kept as STEINER_SUB_AGENTS_MAX_PARALLEL per D5.
- Known deferred (RESOLVED by step-3, per resume note): `make check` tidy-check golang.org/x/sync discrepancy resolved; semaphore import lives in internal/agent.
- step-6: go test -race ./cmd/steiner/... OK (after widening test model ContextWindow to 32768 in TestRunnerDelegateDepsCarryRuntimeSandboxState — the new preamble text tipped the 4096-token test default over its 70% compaction threshold; executor verified base passes / worktree fails 3-3, then child-21 fixed it mirroring sibling test pattern); go test ./internal/prompt/... ./skills/... ./internal/oneshot/... OK; go build OK; grep scheduler in docs/ README.md CLAUDE.md → only historical docs/gap-report-research-milestones.md:531 (exempt). Merged fast-forward a48d6ef0. Worktree + temp branch cleaned.
- step-6 naming deviation applied: docs use `sub_agent.max_parallel` (singular, actual config key) not the plan's plural shorthand — review + executor caught the plural path in README and delegation docs; fixed.
- make check lint gate: 7 findings cleaned in commit 9ab13dc3 (5 pre-existing from steps 1/3/5 + S1000 one-case select in integration_parallel_test.go + unparam always-nil error return on buildRuntimeProviderFactory surfaced after cache-clean rerun). golangci-lint run ./... now 0 issues.
- step-7: go test -race ./internal/delegation/... ./internal/agent/... OK; new file internal/delegation/integration_parallel_test.go with 8 e2e tests (overlap via barrier, bounding, unbounded, ordering, mixed batch, failure isolation, no nesting, cancellation). 4 review rounds on the ordering test: final design gates releases on DelegationComplete count + per-task summary-served signal, forcing result-value finalization in reverse call order; documented residual limit (provider-level observation cannot see the handler's last steps; deterministic reversed completion covered by unit test TestExecuteToolCalls_ParallelReversedCompletionAppliesInOrder). Merged fast-forward 70fcd268. Worktree + temp branch cleaned.
- step-7 review rounds: child-24 (round 1: 7 findings — unbounded waits, weak ordering, weak mixed batch, flaky failure, weak cancellation, bounded count); child-25 (round 2: order release sequencing + failure-run deadline); child-26 (round 3: child-side ack not happens-after); child-28 (round 4: DelegationComplete precedes summary); child-29 (round 5: summary-served signal precedes provider return; residual irreducible — comment-only fix demanded); child-27 fixed rounds 3-5 (event gating fe38183b, summary-served 59bec8e1, comment 70fcd268).
- child-30: lint cleanup (9ab13dc3, 0 lint issues); child-31 review found 1 false-positive (S1000 body misread; executor verified via git show that base already contained the same mutations — merge proceeded).
- child-32: final gate — golangci-lint cache clean, make check ALL GREEN (tidy, fmt, imports, build-binaries, test-race 28 pkgs, vet, lint 0 issues, vuln no vulns), make test-perf 8/8, flake `go test -race -count=10 ./internal/delegation/` + `-count=5 ./internal/agent/ -run TestExecuteToolCalls` clean. Tree clean at 9ab13dc3.

## Deviations / blockers

## Review remediation
- Review fixes were applied in isolated worktree `review-fix/2026-08-17_parallel-delegation-v2`.
- PD-01: submissions require a non-empty exact identity match before claiming a pending approval. Duplicate, stale, and empty identities leave FIFO state unchanged; delivery remains outside the coordinator mutex.
- PD-02: CallID is retained by ordinary tool-call segments and fallback approval pills. Initial prompts and identity-based promotion use the coordinator FIFO head, including same-tool requests.
- Regression coverage verifies duplicate same-tool submissions do not advance the tail and CallID retention in both content paths.
- Verification: `go test -race ./internal/interactive ./internal/tui`, `golangci-lint cache clean`, `make check`, and `git diff --check` passed.
