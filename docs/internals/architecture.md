# Architecture

How Steiner's packages fit together and how one turn moves through them. Topic details live in the linked pages. Repo map and contributor rules are in [AGENTS.md](../../AGENTS.md).

## Entry points and composition root

`cmd/steiner` wires dependencies and flags. It holds no business logic.

| Mode | Entry | Path to `agent.Runner` |
|---|---|---|
| exec | `runExecMode` (`exec.go`) | `cliRunner.RunWithHooks` → `cliRunner.run` → `Runner.Run` |
| interactive | `runInteractiveMode` (`interactive.go`) | `interactive.Session.driverRun` (`driver_run.go`) → `sessionRunner.Run` (`interactive_session.go`) → `cliRunner.RunWithHooks` → `cliRunner.run` → `Runner.Run` |
| oneshot | `runOneshotTask`, `runOneshotResume` (`cmd_oneshot.go`) | `oneshot` orchestrator → `phaseRunner.RunPhase` → `agent.NewConversationDriver` (`phase_driver.go`) → `cliRunner.driverRun` (`runner_driver.go`) |

`defaultBuildRuntime` (`runtime.go`) calls `buildRuntimeWithRoots` (`runtime_build.go`). It builds the config, sandbox (`buildRuntimeSandbox`), diagnostics, event sink, delegation supervisor, MCP and LSP managers (`connectRuntimeMCP`, `connectRuntimeLSP`), provider factory (`buildRuntimeProviderFactory`), tool registry (`buildRuntimeRegistry`), and history and session stores. The result is the `cliRuntime` struct.

## Package layers

| Layer | Packages | Internal imports (verified with `go list`) |
|---|---|---|
| Composition | `cmd/steiner` | all of the below |
| Orchestration | `interactive`, `oneshot`, `delegation` | interactive: `agent`, `delegation`, `session`, `prompt`, `tool`. oneshot: `agent`, `session`, `fsatomic`, `tool`. delegation: `agent`, `advisor`, `prompt`, `provider`, `tool` |
| Turn loop | `agent` | `config`, `diagnostics`, `output`, `prompt`, `provider`, `tool`, `tool/builtin`, `usagestats` |
| Context | `prompt` | `config`, `provider`, `skill` |
| Transport | `provider` | `config`, `diagnostics`, `metadata`, `oauth`, `quota`, `usagestats` |
| Tools | `tool` (with `tool/builtin`), `mcp`, `lsp`, `sandbox` | `tool`: `config`, `diagnostics`, `provider`; `sandbox`: `config`; `mcp`, `lsp`: `config`, `tool` |
| Persistence | `session` | `agent`, `fsatomic` |
| Presentation | `output`, `tui` | `output`: `prompt`, `provider`, `tool`, `usagestats`; `tui`: `agent`, `interactive`, `output`, and others |

`tool` and `provider` never import `agent`. `prompt` never imports `delegation`.

## Turn lifecycle

1. `Runner.run` (`internal/agent/runner.go`) validates the request, builds `RunState`, and creates a `turnProgressor` (`newTurnProgressor`). The loop ends on a stop reason. `stopRunBeforeTurn` enforces `Limits.MaxTurns`, and `Limits.TurnTimeout` bounds each turn.
2. `turnProgressor.advance` prepares the turn (`prepareTurn`), compacts when the budget requires it (`handleCompaction`), and calls the model (`executeModelCall`) under `Limits.ModelCallTimeout`.
3. `executeChatRequest` (`model_call.go`) calls `ChatCompletion` first unless `RunRequest.StreamingPreferred` is set, then falls back to `StreamChatCompletion`. Streamed chunks become output events.
4. `executeToolCalls` (`turn_progression_tools.go`) walks the tool calls in order. Consecutive calls with the same `ParallelClassOf` class form a batch if that class limit is above 1 (`parallelRunLength`). `invokeParallel` runs the batch behind a `semaphore.Weighted` sized by `MaxParallelTools` or `MaxParallelDelegations`. Other calls run one at a time (`executeSingleToolCall`).
5. Each call runs through `tool.Executor.Execute`. `appendToolOutcome` records the result as a tool message in the conversation.
6. `RunRequest.DrainInbox` supplies queued input for the run (nil for sub-agents). A run ends with `emitStop` and a `StopReason`: `complete`, `max_turns`, `max_tokens`, `cancelled`, `error`, `usage_limit`, or `workflow_handoff`.
7. A transient provider error that survives provider-level retries gets a bounded per-turn retry (`handleTransientProviderRetry`).

## Provider

- `provider.Provider` (`interface.go`) has `ChatCompletion`, `StreamChatCompletion`, and `SupportsUsageStats`. `StatefulTranscript` marks backends that own the transcript, so callers must not rewrite it.
- `provider.Client` (`client.go`) implements `Provider` for HTTP backends. It handles pacing (`pace`), retries, and diagnostics. It delegates format work to a `Wire` (`wire.go`: `Payload`, `HTTPRequest`, `DecodeResponse`, `DecodeStream`). Wires are `openaiWire`, `responsesWire`, and `anthropicWire`.
- Retry: `Client.withRetry` (`retry.go`) loops over attempts, waiting for `retryBackoffDelay` or the server `Retry-After`. `classifyRetryError` calls `classifyProviderError` (`client.go`), which uses `isRetryableHTTPStatus` and `isRetryableTransportError` (`http_error.go`). The wire may refine the decision through `RefineRetry`. Usage-limit and decode errors are never retried.
- Provider types are `config.ProviderType` values. `claude_subscription` runs Claude CLI processes (`claudesub_*.go`) instead of `Client`. See [claude-subscription.md](claude-subscription.md).

## Tools

- `tool.ToolDef` (`types.go`) has `Name`, `ParameterSchema`, `Timeout`, `ParallelSafe`, and MCP provenance. It runs either an in-process `Handler` or a subprocess (`ExecPath`, `Subcommand`).
- `tool.Registry` (`registry.go`) holds definitions. `Names` and `Definitions` sort by name, so tool order is deterministic.
- Sources: built-ins (`tool/builtin`, for example `NewBashTool`), MCP tools ([mcp.md](mcp.md)), LSP tools ([lsp.md](lsp.md)), the advisor ([advisor.md](advisor.md)), and sub-agent tools from `delegation`.
- `Executor.Execute` → `runPipeline` (`execution_pipeline.go`):
  1. Reject unparseable input (`KindInvalidToolInput`) and resolve the definition.
  2. Apply the execution mode. Plan mode narrows writes to the plan directories (`RestrictWritesTo`).
  3. `normalizeExecutionInput` runs `PathPolicy.ValidateToolInput`. A promptable out-of-project path raises an `ApprovalRequest` (`ApprovalKindPath`) through `ApprovalResponder`.
  4. Store the sandbox decision in context (`SandboxWrapperKey`).
  5. `executeTool` runs the handler or subprocess under the tool timeout. Sandbox denials can re-enter approval (`execution_denial.go`).
- Output bounding: captures use `boundedCapture` (`output.go`). Bash keeps a head and tail (`captureUntilMarker`) and saves full output as an artifact (`bashArtifactStore`).
- Modes and sandboxing: [execution-modes.md](execution-modes.md), [sandboxing.md](sandboxing.md).

## Sessions and persistence

| Store | Location | Code | Holds |
|---|---|---|---|
| Sessions | `~/.config/steiner/sessions` | `session.Store` (`store.go`) | `session.Session`: model, mode, skills, `Lineage`, cache key, sub-agent ledger |
| Prompt history | `~/.config/steiner/history.log` | `history.Writer` | Input history |
| Project directory | `.steiner/` in the project | `session.EnsureSteinerProjectDir` | Project state, with a `.gitignore` |

- `Store` writes one file per session and an index, using atomic writes (`writeAtomic`). It evicts the oldest entries.
- Resume: `Session.LoadSessionByID` (`persistence.go`) → `loadSession`. `replayLoadedTranscript` (`interactive/replay.go`) re-emits events so the TUI shows the transcript. `session.Fork` copies a session.
- Compaction adds a generation with `ConversationLineage.WithNewGeneration` (`compaction_escalation.go`, `interactive/compaction.go`).
- Other stores: diagnostics ([diagnostics.md](diagnostics.md)), usage stats ([cache-stats.md](cache-stats.md)), quota snapshots ([usage-dashboard.md](usage-dashboard.md)), oneshot manifests ([oneshot.md](oneshot.md)).

## Events, approvals, and the TUI

- Components emit `output.Event` values (`event_types.go`: `Type`, `Timestamp`, `Payload`, `Scope`) through `output.EventSink.Emit`. Sub-agent events carry `Scope.AgentID`.
- Sinks: `output.NewStream` writes terminal output, `output.NewFileLogSink` writes the log file, and `output.NewMultiSink` fans out to both (`runtime_build_logging.go`).
- TUI: `tui.eventBridge` (`event_bridge.go`) buffers events and delivers them as `runtimeEventMsg`. `Model.updateDispatch` (`model_update.go`) routes each message to one handler. `handleRuntimeEventMsg` calls `applyEvent` (`model_events.go`), which routes sub-agent events to `applySubAgentEvent` and payload events to `applyPayloadEvent`.
- User input flows back as `interactive.Action` values (`actions.go`: `SubmitPrompt`, `SubmitApproval`, `InterruptActiveRun`, `CancelDelegate`), dispatched by `Session.Handle` (`session_dispatch.go`).
- Approval round trip: `agent.NewEventingApprover` (`approval.go`) emits an approval event. In interactive mode, `approvalResponder.RequestApproval` (`interactive/wiring.go`) registers the request with `ApprovalCoordinator` before the event is emitted. The TUI shows it and sends `SubmitApproval`, and `ApprovalCoordinator.Submit` releases the waiting request. In exec mode, `stdinApprovalResponder` (`cmd/steiner/approval.go`) answers instead.

## Invariants

- Static prompt sources come before dynamic ones, and tool definitions sort by name: [context-management.md](context-management.md), [prompt-caching.md](prompt-caching.md).
- Cache hints in the wires (`cache_control` breakpoints, Codex session headers, `PromptCacheKey`) must not be removed: [prompt-caching.md](prompt-caching.md).
- Tool output is bounded and summarized (`boundedCapture`).
- Out-of-project paths raise path approval requests (`ApprovalKindPath`). MCP calls use the MCP approval kind: [execution-modes.md](execution-modes.md), [mcp.md](mcp.md).
- Concurrency is bounded by `MaxParallelTools` and `MaxParallelDelegations`. Sub-agents cannot nest: [sub-agent-delegation.md](sub-agent-delegation.md).
- Retries live in `provider` (per request) and `agent` (per turn). Wires only refine retry classification through `RefineRetry`.

Other topic pages: [configuration.md](configuration.md), [desktop-notifications.md](desktop-notifications.md).
