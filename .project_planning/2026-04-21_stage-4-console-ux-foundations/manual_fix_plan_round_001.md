# Manual Fix Plan Round 001

## Reported Issues

1. In interactive mode there is no visible blinking cursor after the first round-trip.
2. After the first successful prompt, the second submitted message appears to do nothing.

## Scope

- Keep fixes within Stage 4 console UX wiring.
- Focus on the interactive prompt/output integration boundary across:
  - `internal/repl`
  - `cmd/steiner/main.go`
  - tests covering interactive console behavior

## Diagnosis

- The Stage 2 readline integration only made assistant replies prompt-aware through the REPL `Prompter`.
- Status and event output in interactive mode still flow through the existing stderr/status streams and provider logging sink.
- That bypasses readline's own prompt-refresh path, so terminal output can corrupt the active prompt state after the first turn.

## Fix Plan

1. Add a prompt-backed output stream adapter in `internal/repl` so non-assistant interactive output can be rendered through the same prompt-aware surface.
2. Update interactive CLI wiring in `cmd/steiner/main.go` to:
   - create one shared interactive prompter
   - route session output, event sink output, and approval/status rendering through prompt-aware streams during interactive mode
   - preserve existing non-interactive and file-log behavior
3. Add or update tests for the interactive stream selection and prompt-backed writer behavior.
4. Re-run the relevant verification first:
   - `go test ./cmd/steiner ./internal/repl ./internal/output`
5. If that passes, re-run end-of-implementation checks affected by the manual-fix scope:
   - `go vet ./...`
   - `go test ./...`
   - `make build-binaries`
   - `go build ./cmd/steiner ./cmd/steiner-core-tools`
