# Research — Advisor pattern (issue #192)

## Question

1. Premise check — does Anthropic's "advisor tool" implement "smarter model advises a weaker/cheaper driving agent"? What is it exactly?
2. Premise check — does pi-advisor implement the same pattern? What does it do?
3. Mechanism (each): invocation (tool vs hook), request/response shape, lifecycle, model split.
4. Interface/API surface (each): params, config knobs, schema, how guidance is injected back.
5. Other prior art for "stronger model steers weaker agent".

## Findings

### Anthropic advisor tool (server-side)

- **Premise: CONFIRMED.** Cheaper executor (Sonnet/Haiku) drives the loop; stronger model (Opus) advises on escalation.
- **What it is:** a server-side built-in tool (beta, `type: "advisor_20260301"`) added to the request `tools` array; requires `betas: ["advisor-tool-2026-03-01"]`. The whole advisor round-trip happens inside a single `/v1/messages` request — no extra client calls, no client context management.
- **Mechanism:**
  1. Declare `{"type":"advisor_20260301","name":"advisor","model":"claude-opus-4-8"}`.
  2. Executor decides when to call; emits a `server_tool_use` block with **always-empty `input: {}`** — signals timing only, passes no question.
  3. Server runs a separate inference on the advisor model, which automatically receives the **full transcript** (system prompt + all tool defs + all prior turns + all tool results).
  4. Advisor reply returns as `advisor_tool_result` → `advisor_result.text` (free-form natural language) + `stop_reason`.
  5. Executor resumes, informed by the advice. Advisor never calls tools, never produces user-facing output.
- **Lifecycle:** one-shot per call; no persistent advisor session — "memory" = shared transcript.
- **Model split:** explicit/configurable. Advisor model set in tool def; executor model = top-level `model`.
- **Trigger:** executor decides autonomously. Anthropic data: executors *under-call* on coding tasks without prompt steering, *over-call* if nudged too early. Docs ship a verbatim suggested coding system prompt ("call advisor BEFORE substantive work", and after file writes / test output).
- **Schema:** `{type, name, model, max_tokens?}`. Tool-level `max_tokens` caps advisor output; top-level `max_tokens` caps executor only (advisor uncapped otherwise). `caching` flag for 3+ calls. Advisor output does not stream.
- **Availability:** beta; Claude API + Claude Platform on AWS. NOT on Bedrock/Vertex/Foundry.
- **Evaluated gains:** Sonnet 4.6 + Opus advisor: +2.7pp SWE-bench Multilingual, −11.9% cost/task. Haiku + Opus advisor on BrowseComp: 41.2% vs 19.7% solo, 85% cheaper than Sonnet solo.

### pi-advisor (client-side, third-party)

- **Premise: CONFIRMED.** README: "modeled after Anthropic's advisor tool pattern."
- **What it is:** extension for the "Pi" coding agent (github.com/RimuruW/pi-advisor, npm v0.3.0 ~June 2026). Registers an `advisor` tool in Pi's registry. **Client-side** implementation of the same pattern.
- **Mechanism:** executor calls `advisor(params)` → extension detects stage (initial/recovery/final-check), builds **curated/bounded** context (bounded system prompt + active-tools summary + recent tool activity + first + last N messages), calls advisor model via Pi's provider interface, returns **structured** verdict ("On track" / "Course-correct" / "Not done yet") + ≤5 numbered action items + optional file/command refs.
- **Lifecycle:** one-shot; config persisted to `~/.pi/agent/advisor.json`.
- **Model split:** explicit; default advisor model `claude-fable-5` (note: this IS a real model — Fable 5 — research agent's cutoff lacked it). Configurable via `/advisor config`.
- **Trigger:** automatic (tool call) AND manual (`/advisor ask` slash command).
- **Config knobs:** `provider=anthropic`, `model=claude-fable-5`, `maxUsesPerRun=3`, `maxTokens=16384`, `reasoning=high`, `maxContextMessages=18`.
- **Injection:** tool result block, rendered inline in TUI with token usage, stage label, expand affordance.

### Key differences (Anthropic vs pi-advisor)

| Aspect | Anthropic (server) | pi-advisor (client) |
|---|---|---|
| Where it runs | Inside one API request, server-side | Client orchestrates a second model call |
| Context to advisor | Full transcript | Curated/bounded (stage-aware) |
| Output shape | Free-form text | Structured verdict + action items |
| Per-run cap | Client's responsibility | Built-in `maxUsesPerRun=3` |
| Manual trigger | No | Yes (`/advisor ask`) |
| Provider support | Anthropic only | Any provider |

## Implications

- Pattern is validated by published benchmarks — not speculative.
- **Implement as a model-invoked tool**, not a loop hook. Executor decides timing via system-prompt steering. Simplest fit for steiner's explicit-tool-use philosophy and existing `internal/tool` registry.
- **Driving model passes ~nothing in tool input.** Steiner's advisor tool assembles the advisor's context internally (from the live conversation), not from tool args.
- **Full-transcript (Anthropic) vs curated (pi-advisor) is the central design decision.** Full = simpler, higher quality; curated = cheaper, needs stage logic.
- Advisor is a **pure reasoning pass**: no tool access, no file writes, no user-facing output. In steiner terms it's a provider call, not a new agent with tools. This distinguishes it sharply from the existing `delegate`/`plan` sub-agents (which DO have tools and run a child loop).
- **Per-run cap matters for cost.** Add a config field from the start.
- Steiner is provider-agnostic (Ollama default) → must implement **client-side** (Anthropic's server tool can't be relied upon).
- Config needs an explicit `advisor_model` distinct from executor model.
- Advisor call blocks the turn → needs a visible TUI status indicator.
- Anthropic's suggested coding system prompt should be fetched in full before writing steiner's steering prompt.

## Risks and Uncertainties

- Anthropic's server-side tool is beta + Anthropic-only → not usable as steiner's backend; must build client-side.
- Full-transcript context can get expensive for long sessions; curated context loses information. Tradeoff to decide.
- Trigger timing is fragile — needs prompt tuning; under-/over-calling both observed.
- pi-advisor is brand-new (0.3.0, 0 dependents); robustness unverified — use as design reference only.
- Full text of Anthropic's suggested coding system prompt not captured — fetch before prompt design.
- Must clearly differentiate advisor from existing `delegate`/`plan` tools so the executor doesn't conflate them.

## Sources

- https://claude.com/blog/the-advisor-strategy
- https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/advisor-tool
- https://platform.claude.com/docs/en/agents-and-tools/tool-use/advisor-tool
- https://www.npmjs.com/package/pi-advisor
- https://github.com/RimuruW/pi-advisor

## Open Questions

1. Full text of Anthropic's suggested coding system prompt (fetch before prompt design).
2. Full transcript vs curated context for steiner's first version.
3. Whether stage detection (initial/recovery/final-check) earns its complexity in v1.
4. Exact differentiation in docs/tool descriptions between advisor and `delegate`/`plan`.
