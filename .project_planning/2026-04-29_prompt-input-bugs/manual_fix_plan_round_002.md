# Manual Fix Plan Round 002

## Reported Issue
- After manual fix round 001, normal typing no longer works in Kitty, Ghostty, and WezTerm.
- Terminator still accepts typing, but `Shift+Enter` still does not insert a newline there.

## Scope
- Fix the typing regression introduced by manual fix round 001.
- Preserve plain prompt entry, plain `Enter` submit, approval-mode `Enter`, and `Alt+Enter` newline insertion.
- Do not broaden into unrelated TUI behavior.

## Findings So Far
- Manual fix round 001 added:
  - a local Bubble Tea replacement with `Shift+Enter` decode coverage
  - terminal keyboard-protocol enable/disable escape writes from the TUI lifecycle
- The typing regression strongly suggests the protocol enable request changes ordinary key reporting in terminals that support it, while the local parser still lacks broader decoding for that mode.
- The safest likely short-term fix is to remove the protocol enable/disable behavior while preserving only safe local parsing and tests.

## Planned Fix Pass
- Remove the runtime keyboard-protocol enable/disable request from the TUI lifecycle.
- Keep only behavior that does not regress normal typing.
- Re-run narrow TUI checks first, then broader repo checks if needed.
- If `Shift+Enter` remains unsupported after removing the protocol request, record that limitation explicitly instead of guessing.

## Verification
- `gofmt -w` on touched Go files
- `go test ./internal/tui -run 'Test.*(Shift|Enter|Input|Approval)'`
- `go test ./...`
- `go build ./...`

## Acceptance
- Normal typing works again in Kitty, Ghostty, WezTerm, and Terminator.
- Plain `Enter` submit works.
- Approval-mode `Enter` works.
- `Alt+Enter` newline insertion still works.
- No new claim is made that `Shift+Enter` works unless manual verification confirms it.
