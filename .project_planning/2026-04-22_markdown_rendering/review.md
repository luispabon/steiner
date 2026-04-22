# Review

## Scope Reviewed
- Planning folder: `.project_planning/2026-04-22_markdown_rendering`
- Feature branch: `cl/2026-04-22_markdown_rendering`
- Review scope: Stage 6 markdown rendering, sidebar wiring, layout changes, and recorded execution/verification state

## Inputs Reviewed
- `overview.md`
- `plan.yaml`
- `execution.md`
- Repository state on `cl/2026-04-22_markdown_rendering`
- Changed implementation in `cmd/steiner/main.go`, `internal/tui/app.go`, `internal/tui/content.go`, `internal/tui/git.go`, `internal/tui/keys.go`, `internal/tui/model.go`, `internal/tui/render.go`, `internal/tui/sidebar.go`, and `internal/tui/statusbar.go`

## Findings
- Review pass 1 status: `fail`
- `blocking` `R1`: Markdown rendering is hard-wired to 80 columns and cached as already-rendered strings, so the content pane does not reflow when the viewport width changes. Evidence: `internal/tui/content.go` constructs the Glamour renderer with `markdownRenderWidth = 80` and caches it on the buffer (`internal/tui/content.go:11-18`, `internal/tui/content.go:294-310`), while `internal/tui/model.go` only changes viewport width on resize and then reuses `m.content.String()` (`internal/tui/model.go:155-178`). This violates the stage acceptance that the three-region layout render correctly and reflow on terminal resize.
- `blocking` `R2`: The sidebar’s “active skills” metric is incorrect in interactive mode. The model repopulates `sidebar.activeSkills` from all discovered skill names on every sync (`internal/tui/model.go:248-258`), the interactive runtime wires `OnSkillToggle` to a no-op (`cmd/steiner/main.go:177-197`), and prompt assembly still always uses `rt.skillNames` instead of any enabled subset (`cmd/steiner/main.go:242-247`). The sidebar therefore reports configured skills, not active skills, and `/skill` toggles do not affect the actual run state.
- Review pass 2 status: `pass`
- `R1` resolved: the content buffer now stores semantic segments and re-renders markdown against the current viewport width instead of caching fixed-width rendered strings. `internal/tui/model.go` now rebuilds viewport content with the current width, and `internal/tui/model_test.go` covers width-sensitive markdown reflow.
- `R2` resolved: interactive skill state is now tracked separately, `/skill` toggles update the enabled subset, the sidebar shows only enabled skills, and `cliRunner.Run` receives the selected subset for prompt assembly. `cmd/steiner/main_test.go` and `internal/tui/model_test.go` cover the enabled-skill flow.

## Fix Plan
- Proposed review-fix pass:
- Address `R1` by making markdown rendering width-aware and re-renderable on layout changes. Keep raw assistant blocks so markdown can be re-rendered when the content pane width changes, and add focused tests covering resize/reflow behaviour.
- Address `R2` by plumbing active skill state through the interactive TUI/runtime boundary. The model should surface only enabled skills in the sidebar, and interactive runs should pass the enabled subset into prompt assembly. Add focused tests covering `/skill` toggles and the sidebar/runtime state they drive.
- Planned verification after fixes: `go test ./internal/tui ./cmd/steiner`
- Approved by user: yes
- Review-fix execution: isolated pass on temporary branch `reviewfix/2026-04-22_markdown_rendering-pass-1` in worktree `/tmp/steiner-reviewfix-pass-1`

## Fixes Applied
- Spawned review-fix sub-agent `019db605-2b30-7552-ac3c-80ad35552e5e` using `gpt-5.4-mini` (cheaper tier), committed fix pass `d7a18c5` on the temporary branch, reviewed the returned diff, then merged it back into `cl/2026-04-22_markdown_rendering`.
- Temporary review-fix branch and worktree were deleted after merge.
- Sub-agent closure status: closed after merge and cleanup.

## Verification
- Review pass 1 completed from code and diff inspection.
- Reviewer reruns before fixes: none.
- Post-fix verification reruns:
- `go test ./internal/tui` -> passed
- `go test ./cmd/steiner` -> passed

## Final Status
- Status: `pass`
- Blocking findings: none
- Non-blocking notes: none
- Finaliser handoff: ready after committing this `review.md` update
