# Research 002 — Advisor context strategy (issue #192)

Focused follow-up: what context to feed the advisor each call. Strategies:
(A) full transcript + cap, (B) curated/stage-aware subset (pi-advisor), (C) dedicated
model-led summarisation pass per consult.

## Question

1. Context fidelity vs cost — does full transcript beat curated/summarised for an on-demand advisor?
2. Summarisation-before-critic risk — does feeding a summary launder away the signal a critic needs?
3. Who should summarise, if summarising at all?
4. Anthropic's full suggested coding system prompt for the advisor tool.
5. Other production frameworks with on-demand advisor/critic + their context choice.

## Findings

- **Anthropic advisor tool: full transcript, always, server-enforced.** Executor input is
  empty; server forwards system prompt + all tool defs + all prior turns + all tool results.
  No public rationale for full-vs-summary. Two indirect signals: a **low-context** advisor
  call (before the executor has gathered context) *hurt* performance by 3–4pp — strong
  evidence richer context → better advice. Recommended timing = moments of max context
  (after exploratory reads; after writes+test output).
- **Context rot** (Anthropic context-engineering blog): recall degrades as tokens grow (n²
  attention). Argues against blindly sending an unbounded raw transcript — favours
  curation/compaction. (That blog is about the *executor's* context, not the advisor.)
- **pi-advisor uses strategy B**: bounded system prompt + active-tools summary + recent tool
  activity + first+last N messages; stage (initial/recovery/final-check) shapes prompt
  framing, not the content. No published eval that B matches full-transcript quality.
- **Summarise-before-critic is risky (Q2).** Self-correction survey (Kamoi et al. 2024,
  arxiv 2406.01297): LLMs refine well *given reliable feedback*, but generating reliable
  feedback needs **additional information**, not less. CriticGPT (OpenAI, arxiv 2407.00215)
  critiques **full** answers/conversations — no evidence it works on summaries. Summarisation
  literature (arxiv 2512.05334): summaries drop info needed downstream "unless the
  summariser is aware of the downstream task." Mechanistically, summarising agent transcripts
  compresses exactly the artifacts an advisor needs (raw errors, file contents, the
  executor's specific misreading) → laundering risk.
- **Who summarises (Q3):** weak-model summaries are less faithful; a weak driver summarising
  its own work compounds the Q2 risk. If summarising is unavoidable, the **strong advisor
  model** should do it, with explicit instructions to preserve errors/filenames/assumptions/
  test output. But strategy B avoids the failure mode entirely.
- **Anthropic coding system prompt (Q4), captured:** timing guidance — call advisor BEFORE
  substantive work (writing/editing/declaring an answer); orientation (finding files,
  fetching) is not substantive. Also call: when task believed complete (make deliverable
  durable FIRST — write/commit before the call); when stuck (recurring errors, not
  converging); when considering an approach change. On multi-step tasks call ≥once before
  committing to an approach and once before declaring done; on short reactive tasks the
  advisor adds most value on the first call. Give advice serious weight, but adapt if a step
  fails empirically or primary-source evidence contradicts it. Optional per-call user line to
  bound output: "(Advisor: keep guidance under 80 words…)" — raises call frequency, lowers
  per-call cost, net cheaper. An optional "Hard rule" block exists for under-calling
  executors (+7–10pp pass but over-calls on easy tasks; ~flat on mixed) — exact text not
  captured.
- **Other prior art (Q5):** CriticGPT (trained critic on full responses, not an on-demand
  loop tool); LLM-as-judge / MT-Bench (full responses to judges, eval not loop). No other
  documented production on-demand advisor with a described context-selection strategy beyond
  Anthropic + pi-advisor.

## Implications

### Steiner-specific (the decisive factor): delegation already makes the transcript lossy

steiner is steered to **delegate aggressively**, and per `docs/SUBAGENT_DELEGATION.md` a
delegate's internal tool calls and transcript are **NOT** copied to the parent — only the
task given + a bounded result summary persist. So steiner's "full transcript" is, by
construction, *already curated*: original task + parent's own tool calls + delegation
task/result summaries. Compaction summarises old parent turns on top of that.

Consequences:
- **Strategy A and B nearly converge in steiner.** Passing the live parent conversation
  already yields a *bounded, summarised-at-the-boundaries* context — we get strategy B's
  benefit (bounded, no context-rot blow-up) for free, without building a curated selector.
- **Strategy C (extra summarisation pass) is the wrong move** here: the transcript is already
  summarised at delegation + compaction boundaries; a further summarisation compounds the
  fidelity loss the critic literature warns about (Q2), adds a model call + latency.
- **Advisor's natural role is high-level steering/planning**, not line-level bug-catching
  inside delegated work — it literally cannot see inside delegations. This *matches* the
  issue ("planning and steering"). Low-level review stays the job of `verify`/`code`
  sub-agents.
- **Cost is partly self-mitigating:** delegation collapses big work into summaries, keeping
  the parent transcript (and thus advisor cost) bounded.

### General
- Reuse Anthropic's coding system prompt (timing guidance) for steiner's advisor steering
  preamble — behavioural, model-agnostic.
- Keep a head/tail token cap as a hard safety bound (preserve original task + recent turns).
- Defer pi-advisor stage classification; revisit if advice quality needs per-stage framing.

## Risks and Uncertainties

- No published ablation comparing full vs curated vs summarised for an on-demand coding
  advisor — all conclusions are inferences from adjacent evidence.
- `claude.com/blog/the-advisor-strategy` was inaccessible this round; possible extra rationale missed.
- "Hard rule" block exact text not captured.
- pi-advisor's first+last N values unknown / possibly configurable.
- Self-correction survey predates latest models; intrinsic self-correction may have improved.

## Sources

- https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/advisor-tool
- https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
- https://pi.dev/packages/pi-advisor
- https://arxiv.org/html/2406.01297v3 (self-correction survey)
- https://arxiv.org/html/2407.00215v1 (CriticGPT)
- https://arxiv.org/html/2512.05334v1 (summarisation downstream effects)
- docs/SUBAGENT_DELEGATION.md (steiner — delegate transcript not copied to parent)

## Open Questions

1. Exact "Hard rule" block text (read docs page directly during implementation).
2. Whether to optionally fold full recent delegation *reports* into advisor context (they
   already persist in the transcript — likely no special handling needed).
3. Cost threshold at which explicit curation/stage detection becomes worthwhile.

## Decision

**Strategy A, realised via steiner's existing mechanisms:** pass the live (compaction-
budgeted) parent conversation as advisor context, bounded by a head/tail token cap. Because
delegation + compaction already summarise, this is effectively a bounded curated context
with no new subsystem. Reject the dedicated summarisation pass (C). Defer explicit
pi-advisor-style curation/stage detection (B-as-new-subsystem) until cost demands it.
