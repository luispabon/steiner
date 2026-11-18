# Review

Branch: oneshot/ensure-when-you-start-with-the-issues-to-add-the-f5295e25cb6a79a5, 54 commits over origin/main (c0c60265), head e6291444, 47 files, +5748/-286, tree clean.

Issues #1054, #1051, #1023 and #1046 were verified OPEN with the in-progress label at review start. Labels left in place; PR creation stays with the engine.

## Status

pass_with_notes. No blocking findings remain. Non-blocking notes and residual risks are listed below; every check required by overview.md passes on the final head.

## Scope and inputs reviewed

- Inputs: overview.md, plan.yaml, execution.md, research.md, and the committed repository state.
- Areas: internal/diagnostics (per-stream locking, restartable rotation, append-time tail repair, subprocess tests), internal/oneshot (finalization markers, classification, manual resolution, CLI wiring), internal/filelock (Windows LockFileEx on top of the untouched Unix path), internal/tui oneshot launch history, internal/quota window forecast, and every changed doc page.
- Four parallel area reviews at the implementation head cde5e3b9, a re-review of each fix round, and a final re-review of the last round with source re-checks of the later comment, test and doc deltas.
- Coverage gaps: no macOS or Windows runtime validation was possible on this host. The Windows lock path and Windows cross-process rotation are compile-verified only. There is no process-kill subprocess test (crash states are fabricated in-process) and no retention-versus-append cross-process test.

## Blocking findings and resolution

- R1, finalizeRun wrote report state and closeout intent in two writes, so a crash between them left a versioned run that closeoutOutcomeUnknown treated as settled, permanently losing a possible PR/MR. Fixed: report state, version and pending intent are one write. A write-injection test fails on the pre-fix code.
- R2, diagnostics tail repair ran only at stream open, so a fragment left by a killed peer merged with the next record and became unrepairable. Fixed: repairIncompleteTail runs under the stream lock before every append; tests fail on the pre-fix code.
- R3, the plan-mandated test for provider success plus a failed result write, followed by repeated resumes with zero extra closeout calls, was missing. Test added; it pins the lost-result window. R1 itself is pinned by the R5 test.
- R4, doc claims contradicted the code across both oneshot pages, the diagnostics pages and the usage pages. Fixed by documentation passes and re-verified against code.
- R5, the ambiguous (manualRequired) branch still used two writes, stamping the version and written report before persisting closeout_state unknown, so a failed or interrupted second write left a settled-looking run. Fixed: one write records report state plus the unknown marker and preserves a pending intent. Regression test fails on the pre-fix code.
- R6, a retry_authorized run rejected --url, so an operator who authorised a retry and then found the request had no supported way to record it. Fixed: --url supersedes an authorised retry under the run lock; --retry still requires an unknown outcome. Tests cover acceptance, rejection and invalid URLs.
- R7, prepareStream created the active stream file with O_CREATE at startup when the lock was unavailable or recovery failed, so a cooperating peer rotating in that window could leave a second generation 0 and wedge the stream. Fixed: the stream is registered without a descriptor and the open happens under the lock on the first append. Tests fail on the pre-fix code.

All seven were fixed through delegated code agents (docs through documentor), merged one at a time, and re-reviewed. No review pass edited implementation files directly.

## Non-blocking notes

1. Windows runtime coverage is absent. LockFileEx exclusion, ERROR_LOCK_VIOLATION contention, kill-release and the subprocess harness are compile-verified only. Windows also differs from Unix by checking an already-expired context before the first attempt and by mapping only ERROR_NOT_SUPPORTED to unsupported.
2. Functional limitation, now documented for users and internals. A peer that keeps the active diagnostics file open blocks a rename on Windows, so a stream with long-lived Windows writers can fail to rotate and drop records once the rotation stall bound is reached. The approved plan requires runtime subprocess coverage only where runners are available, so this does not block acceptance for this scope, but multi-writer Windows rotation is not demonstrated.
3. Diagnostics has no process-kill subprocess test, no retention-versus-append test and no cross-process retention assertion. Retention is skipped, not deferred, when startup locking fails, and the internals page states that.
4. finalizeRun still records a closeout intent when oneshot.auto_pr is on but the host maps to no provider, so a crash in that window demands manual resolution of a run that could not have opened a PR/MR. Closeout returns unsupported and the result write clears the intent on the normal path. Recorded deviation, accepted.
5. Legacy ambiguity fires even when the run config proves no external call was possible (incomplete-phase legacy runs and auto_pr-off runs), requiring resolve plus a resume. Conservative by design per overview.md.
6. TUI history is recorded before Run can reject on worktree provisioning or run-lock failure, so such a launch still records the oneshot command. Preflight and store rejections record nothing. Accepted as better UX; the boundary is untested.
7. Diagnostics pays one extra open, stat and one-byte read per record on the hot path. Intended by the design.
8. The closeout write-injection seam is a mutable package global, matching existing precedent (timeNow, acquireStreamFileLock). Safe with the current sequential tests; a future parallel test that writes a manifest could race with it under -race.

Informational, no action: finalizeRun has no settled-state guard of its own (duplicate protection lives in the resume gate); the quota provider-type literal is pinned by a test; the ambiguous-branch failed-write and pending-intent preservation cases are pinned by tests.

## Verification

All on final head e6291444:

- make check exit 0 with a fresh GOLANGCI_LINT_CACHE: tidy-check, fmt-check, imports-check, build-binaries, test-race for all packages, vet, lint with 0 issues, govulncheck with no vulnerabilities.
- GOOS=darwin GOARCH=amd64 go build ./... exit 0; GOOS=windows GOARCH=amd64 go build ./... exit 0. Compilation only.
- go test -race on internal/oneshot, internal/diagnostics, internal/quota and internal/filelock: exit 0.
- go.mod and go.sum unchanged; git status --porcelain empty before and after.
- Linux runtime evidence: two-process diagnostics append and rotation subprocess tests, Unix filelock tests, and the oneshot recovery suite, including under -race.

No check failed. Only platform runtime testing was unavailable, which this host cannot provide.

## Residual risks

- Windows lock and rotation behaviour rests on code reading plus cross-compilation; a Windows runner could still reveal a sharing-mode or error-code mismatch.
- Rotation recovery is restartable and tested with fabricated crash states, but nothing kills a process mid-rename.
- The no-provider intent window and the legacy-ambiguity breadth can cost an operator an unnecessary manual resolution, never a duplicate external request.

## Advisor sanity-check note

Two advisory passes. The first found the remaining two-write window on the ambiguous branch, the retry_authorized/--url gap and the unprotected startup open; these became R5, R6 and R7. The second accepted pass_with_notes on condition that the Windows peer-handle rotation limitation is recorded explicitly (note 2), that exit statuses and attribution are taken on the final head (done), and that the remaining stays-settled wording is corrected (done). The advisor agreed that the untested Windows path, the no-provider intent conservatism and the mutable test seam are non-blocking, and that PR creation stays with the engine.
