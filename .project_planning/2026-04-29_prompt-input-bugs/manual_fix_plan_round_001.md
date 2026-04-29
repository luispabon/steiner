# Manual Fix Plan Round 001

## Reported Issue
- Manual verification found that `Shift+Enter` still does not insert a newline in interactive mode.
- Confirmed terminals: Terminator (VTE), WezTerm, Kitty.
- `Alt+Enter` works in those terminals.

## Scope
- Keep the fix scoped to interactive keyboard input handling for multiline prompt entry.
- Preserve plain `Enter` submit behavior.
- Preserve approval-mode `Enter` behavior.
- Do not widen into unrelated TUI layout or session behavior.

## Findings So Far
- Repo-local routing was fixed so modified `Enter` can reach the textarea.
- The current Bubble Tea dependency version in `go.mod` is `v1.3.10`.
- Local source inspection shows `bubbletea` v1.3.10 recognizes `enter` and `alt+enter`, but does not expose a `shift+enter` key path.
- That means the remaining issue is likely below the app layer: either missing terminal sequence decoding in the dependency, or missing richer keyboard protocol support.

## Planned Fix Pass
- Investigate the smallest safe way to make `Shift+Enter` observable by the app.
- Prefer a minimal dependency-local patch over a broad dependency upgrade if the patch is small and testable.
- If a patch is feasible:
  - isolate it in the temp branch/worktree
  - add regression coverage for the decoded `Shift+Enter` path
  - rerun the narrow TUI verification first
- If no small safe patch is feasible:
  - stop and record the blocker instead of guessing

## Candidate Implementation Directions
- Add a local module replacement for `github.com/charmbracelet/bubbletea` and patch key decoding for known `Shift+Enter` escape sequences.
- Only consider upgrading to a newer Bubble Tea line if the local patch path is clearly infeasible.

## Verification
- `gofmt -w` on touched Go files
- `go test ./internal/tui -run 'Test.*(Shift|Enter|Input|Approval)'`
- `go test ./...` if the fix changes dependency wiring or key parsing broadly

## Acceptance
- `Shift+Enter` inserts a newline in the prompt textarea on supported terminals instead of submitting.
- Plain `Enter` still submits normal prompts.
- Approval-mode `Enter` still works as before.
