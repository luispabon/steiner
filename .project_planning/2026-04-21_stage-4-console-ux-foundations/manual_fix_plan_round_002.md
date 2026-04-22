# Manual Fix Plan Round 002

## Reported Issues

1. Pressing Enter can emit raw terminal control response text such as `^[[5;3R`.
2. A submitted prompt may not be sent to the model on the first Enter.
3. Additional Enters can gradually flush output, indicating prompt/render state desynchronization.

## Scope

- Keep fixes inside Stage 4 console UX wiring only.
- Focus on the interaction between:
  - `internal/repl/prompt.go`
  - `internal/repl/repl.go`
  - `cmd/steiner/main.go`
  - focused tests for prompt-aware interactive output

## Diagnosis Direction

- The current fix routes generic stream writes through `readline.Shell.Printf`.
- The emitted `^[[row;colR` text strongly suggests terminal cursor-position-report traffic is being surfaced as plain input/output instead of being consumed by the prompt library at the right time.
- The likely issue is that arbitrary event/status output is being injected through the readline shell while it is not in the correct internal read/display state, or via a writer path that is too generic for raw terminal control interactions.

## Fix Plan

1. Rework the prompt-aware interactive output path so it uses a dedicated prompt printer abstraction rather than a generic `io.Writer` bridge.
2. Ensure interactive assistant/status/event/approval rendering uses operations that are safe relative to the readline lifecycle.
3. Preserve headless and exec behavior unchanged.
4. Add tests that cover prompt printer routing without depending on raw terminal side effects.
5. Verify in order:
   - `go test ./cmd/steiner ./internal/repl ./internal/output`
   - `go vet ./...`
   - `go test ./...`
   - `make build-binaries`
   - `go build ./cmd/steiner ./cmd/steiner-core-tools`
