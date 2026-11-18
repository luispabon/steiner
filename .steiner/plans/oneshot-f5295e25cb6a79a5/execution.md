# Execution

Branch: `oneshot/ensure-when-you-start-with-the-issues-to-add-the-f5295e25cb6a79a5`, 28 commits over `origin/main` (`c0c60265`), tree clean at completion.

Issues #1054, #1051, #1023 and #1046 all verified to carry the `in-progress` label (`gh issue view` at start and at resume).

## Verification strategy

As recorded in `overview.md`: scoped `gofmt`/`goimports` fix modes, focused then package tests, `go vet` and per-package `golangci-lint` per task; integrated `make check` plus Darwin and Windows cross-builds at the end; subprocess runtime tests only on available platforms. No overrides were needed.

## Resume context

Resumed after a lost run. Steps 1 and 2 were already committed and green; step 3 was restarted from scratch because its child worktree had been pruned with nothing merged. Steps 4 to 6 were executed in this run. An advisor consult was attempted at resume and was unavailable (`provider usage limit reached (codex, plan: plus)`); decomposition followed `plan.yaml` and the plan-phase advisor record.

Rebased onto freshly fetched `origin/main` at the user's request during step 3 (`329402bc..c0c60265`, 13 commits, clean). `origin/main` was re-fetched at the start of the review and was unchanged, so no second rebase was needed.

## Steps

- step-1: complete (prior run). Task 1.1 single task (implementation and tests share one narrow TUI behaviour). Corrections 3; rejected results 3; failed checks 2. Struggle: preflight dispatch ordering, synchronous event emission and lint; re-briefed with failure evidence.
- step-2: complete (prior run). Task 2.1 single task (implementation, build tags, contract docs and platform tests are inseparable). Corrections 7; rejected results 7; failed checks 0. Struggle: Windows subprocess synchronization and cleanup; native Windows unavailable, so cross-compilation only.
- step-3: complete, 7 commits. Tasks: 3.1 per-stream lock plumbing (`locks.go`, `writer.go`, `writer_test.go`), corrections 0; 3.2 restartable rotation (`rotation.go`, `writer.go`, `recovery_test.go`, `envelope.go` doc line), corrections 1 (my brief made `prepareStream` open the active file behind a pending marker, which wedged recovery), failed checks 1 (`gocyclo` on `installRotation`); 3.3 real two-process subprocess tests (`subprocess_test.go`), corrections 0; 3.4 docs (`docs/internals/diagnostics.md`, `docs/user/configuration.md`), corrections 0. Deviations: rotation lives in `rotation.go` rather than growing `writer.go`; tail repair errors fail writer construction like prune errors.
- step-4: complete, 9 commits. Tasks: 4.1 versioned finalization fields, ordered error-returning `finalizeRun`, `unknown` state and failure-report guard, corrections 0, deviation that `resume.go` call sites had to propagate errors; 4.2 classification, synthetic listing phases and `ResolveCloseout`, corrections 0, failed checks 1 (`gocyclo` on `ResolveCloseout`); 4.3 CLI `--resolve`/`--url`/`--retry`, corrections 0, failed checks 1 (own fixture bug); 4.4 both oneshot docs, corrections 0. CLI deviation recorded below.
- step-5: complete, 2 commits. Tasks: 5.1 gated window-average projection plus 13-case table test and a service regression, corrections 0; 5.2 usage docs, corrections 0. Deviation: the test uses a reset exactly 4h20m ahead so elapsed is exactly 40m (4h21m remaining implies 39m elapsed); the service regression keeps 4h21m and asserts only `OK`/`BeforeReset`.
- step-6: complete, 6 commits. Task 6.1 independent review over `origin/main..HEAD` (10 findings, F1 to F10); 6.2 code fixes for F1, F4, F8 and the test gaps plus a lint correction, 1 correction (nested-worktree lint cache was resolved by a fresh `GOLANGCI_LINT_CACHE`, then `make check` passed); 6.3 final verification.

## Sub-agents used

| Agent | Type | Step/Task | State | Notes |
|---|---|---|---|---|
| child-1 | explore | pre-3 survey | complete | diagnostics/lock surface map |
| child-2 | code | 3.1 | merged `c57bed48` | locks, startup retention, tail repair |
| child-4 | code | 3.2 | merged `21938e76`, `50d9f7c1` | rotation transaction, 1 correction |
| child-5 | code | 3.3 | merged `af5ee938` | two-process tests |
| child-6 | documentor | 3.4 | merged `71785740` | diagnostics docs |
| child-7 | code | 4.1 | merged `8fc9a583` | finalization persistence |
| child-8 | code | 4.2 | merged `84e7dd14` | classification and resolve |
| child-9 | code | 4.3 | merged `31100518` | CLI flags |
| child-11 | documentor | 4.4 | merged `4afb6b66` | oneshot docs |
| child-12 | code | 5.1 | merged `57b09929` | quota projection |
| child-13 | documentor | 5.2 | merged `1b01822f` | usage docs |
| child-14 | review | 6.1 | complete | 10 findings, verdicts per area |
| child-15 | code | 6.2 | merged `05ac7eba`, `2407fa35` | review fixes, then lint correction |
| child-16 | documentor | 6.2 docs | merged `a1ba16db` | stale docs claims |
| child-17 | sanity_check | 6.3 | complete | `make check` failed only on a stale lint cache |
| child-3, child-10 | explore | 4, 5 surveys | complete | oneshot and quota maps |

## Verification

- `make check` on the final tree: pass. tidy-check, fmt-check, imports-check, build-binaries, test-race, vet, lint (0 issues) and govulncheck (no vulnerabilities) all pass. The first run failed lint only because the installed cache referenced a deleted sibling worktree; a fresh `GOLANGCI_LINT_CACHE` gave 0 issues, and no tracked file differs between the two runs.
- `GOOS=darwin GOARCH=amd64 go build ./...`: pass. `GOOS=windows GOARCH=amd64 go build ./...`: pass. These prove compilation only; no macOS or Windows runtime validation was possible here.
- Linux runtime evidence: `internal/diagnostics` two-process append and rotation subprocess tests pass, including under `-race`; `internal/filelock` Unix lock tests pass. The Windows `LockFileEx` path is covered by compiled tests that cannot run without a Windows runner.
- Per-task checks: `go test` for `internal/diagnostics`, `internal/oneshot`, `internal/quota`, `internal/tui`, `cmd/steiner`, `internal/filelock`, each with `-race` where concurrency is involved, plus `go vet` and per-package `golangci-lint`.

## Deviations and assumptions

- Manual closeout resolution is exposed as flags on the existing single `oneshot` command: `steiner oneshot --resolve <run-id> --url <url>` and `--resolve <run-id> --retry`, not a `resolve` subcommand. Evidence: `cmd/steiner/commands.go` registers one command with a positional task, so a `resolve` positional would collide with a task named `resolve`.
- Review finding F3 (closeout intent written even when no external call is possible) is fixed only for the `auto_pr` and review-outcome gates. The provider-detection gate needs the `ctx`-bound `git remote` resolution, so it was left out rather than duplicated; a run whose host maps to no provider can still record an intent before returning `unsupported`.
- Review finding F9 (no Windows CI runner for the `LockFileEx` subprocess tests) is out of plan scope and is reported as a platform coverage limitation, not fixed.
- Review finding F10's cosmetic note about `TestRejectedOneshotLaunchDoesNotRecordHistory` was left as is: the positive launch test asserts exactly one history action, which covers the behaviour.
- Removing the redundant retry-authorized term in `finalizeRun` changes one narrow case for the better: a crash inside an authorised retry leaves the intent pending, and the next finalization now requires resolution again instead of calling closeout twice.
- Step 3 tail repair runs at stream open only; a fragment written by another process after this writer opened is repaired by the next writer that opens the stream. Stated in both diagnostics docs.
- Worktree branch base moved once at the user's request; no uncommitted work was carried across it.

## Blockers

None. All four issues are implemented with docs; the Windows runtime tests remain a platform coverage limit, not a blocker.
