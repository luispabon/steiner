# Active Context Management Research
Date: 2026-05-01

---

## Question
What observation masking strategies were tested in the JetBrains/TU Delft NeurIPS 2025 DL4Code study, which performed best, what metadata was preserved, what were the costs, and are there threshold recommendations?

## Findings

**Paper:** "The Complexity Trap: Simple Observation Masking Is as Efficient as LLM Summarization for Agent Context Management"
Authors: Tobias Lindenbauer, Igor Slinko, Ludwig Felder, Egor Bogomolov, Yaroslav Zharov (JetBrains Research + TU Munich)
arXiv:2508.21433 (v3: NeurIPS 2025 DL4C camera-ready)

**Strategies tested:**
1. **Raw Agent** (baseline) — full trajectory, no management; context grows unbounded per turn
2. **Observation Masking** — rolling window of size M: observations older than M turns are replaced with a single placeholder string (e.g. "Previous 8 lines omitted for brevity."). Agent reasoning and action text is always preserved in full. No partial mask or metadata-only variant was tested; it is full replacement with placeholder text.
3. **LLM-Summary** — prompt an LLM to condense older turns into a running summary (as used by OpenHands/Cursor). Triggered on a fixed turn schedule (N=21 in experiments).
4. **Hybrid** — Observation Masking (window W=10) applied first; LLM-Summary triggered at turn N=43 when masked context matches the token volume of raw context at N=21 (~30K tokens).

**Best configuration:**
- Observation Masking with M=10 (rolling window of 10 most-recent turns) performed best on the cost/effectiveness frontier across all 5 model configurations (Gemini 2.5 Flash, Qwen3-32B, Qwen3-Coder 480B, and others) on SWE-bench Verified (n=500).
- Both Observation Masking and LLM-Summary halve cost vs Raw Agent.
- LLM-Summary could not consistently or significantly outperform Observation Masking.
- Hybrid (Observation Masking + delayed LLM-Summary) reduces cost further: 7% less than pure Observation Masking, 11% less than pure LLM-Summary, while improving solve rate by 2.6pp.
- Exception: Qwen3-32B showed LLM-Summary slightly better; the paper attributes this to model-specific reasoning patterns.

**Metadata preserved when masking:**
None beyond the placeholder text. The placeholder replaces the entire observation content. Agent reasoning (`<thought>`) and action (`<action>`) text for every turn is preserved in full — only the environment observation response is masked. No structured metadata (file name, tool name, return code) is retained in the masked slot.

**Cost breakdown:**
- Raw Agent: baseline (e.g. ~$X per instance)
- Observation Masking: ~50% of Raw Agent cost
- LLM-Summary: slightly higher than Observation Masking due to trajectory elongation side-effect (LLM-Summary trajectories run 4–15% more turns than masked trajectories because summaries can introduce ambiguity that causes the agent to re-explore)
- Hybrid: ~43% of Raw Agent cost (best)

**Threshold / window size recommendations:**
- Optimal M=10 for SWE-agent. This is agent-specific and must be tuned per scaffold — OpenHands required M=58 due to retaining retry turns that SWE-agent elides.
- No time-based or token-based threshold tested; threshold is turn-count only.
- The paper notes the fixed window is agnostic to staleness semantics (e.g. a file read observation is masked even if the file was subsequently modified — the agent has no way to know).
- Hybrid trigger: LLM-Summary fires when masked context reaches the same token volume as a Raw Agent would accumulate at the standard LLM-Summary trigger point (~30K tokens → turn 43 with masking vs turn 21 without).

## Implications for steiner

- Implement observation masking (rolling window, replace with placeholder) as the primary context reduction mechanism — it is empirically as good as LLM summarization and far simpler.
- Default window of the last 10 tool result observations is a reasonable starting point; expose as config and instrument for tuning.
- Preserve full reasoning and action text always; only mask tool observation responses.
- The Hybrid (delayed LLM compaction after masking) is worth exploring if further context reduction is needed, but requires steiner to already have compaction working.
- Do not invest in LLM-Summary as the primary strategy — the trajectory elongation side-effect makes it less cost-efficient for most models.

## Risks and Uncertainties

- The paper only tested SWE-bench (software engineering). The authors note that SE tasks have unusually long, verbose tool outputs — masking may be less effective for shorter-output domains.
- All tested agents used a fixed turn-based window with no semantic awareness. The paper calls this a limitation: a stale file read after a write is masked identically to a genuinely redundant one.
- M=10 is not universal; wrong M degrades performance "drastically" (OpenHands example).
- Only tested on frontier models (Gemini 2.5 Flash, Qwen3 family). No results for 7B–32B local models.

## Sources

- arXiv:2508.21433 — "The Complexity Trap: Simple Observation Masking Is as Efficient as LLM Summarization for Agent Context Management", Lindenbauer et al., NeurIPS 2025 DL4C workshop.
- Data: https://huggingface.co/datasets/JetBrains-Research/the-complexity-trap
- Code: https://github.com/JetBrains-Research/the-complexity-trap

## Open Questions

- What is the right M for steiner given its typical trajectory length and tool output sizes?
- Should the placeholder text include any tool-type metadata (e.g. "[read result masked]" vs "[bash result masked]") to aid agent orientation?
- Does the trajectory elongation effect appear with smaller local models as well?

---

## Question
SWE-Pruner analysis found file read operations account for 67–76% of total token consumption. What mitigation strategies were proposed, what was the impact on task success, and which reads are necessary vs redundant?

## Findings

**Paper:** "SWE-Pruner: Self-Adaptive Context Pruning for Coding Agents"
Authors: Yuhang Wang, Yuling Shi, Mo Yang, et al. (Shanghai Jiao Tong University / Microsoft)
arXiv:2601.16746 (v3; accepted ACL 2026)

**Token dominance confirmed:**
- Claude Sonnet 4.5 on SWE-bench Verified: read operations (cat, grep, head, directory listing) = **76.1%** of all tokens.
- GLM-4.6 on same benchmark: read operations = **67.5%** of all tokens.
- Consistent across model architectures — this is an agent behavior pattern, not model-specific.
- Remaining tokens split between execute (~15%) and edit (~9%) operations.

**Mitigation strategy: SWE-Pruner**
A middleware layer ("neural skimmer") inserted between the coding agent and file system. Architecture:
1. Agent emits a **goal hint** with each file read (e.g. "focus on error handling in the authentication flow").
2. A lightweight **0.6B binary classifier** (the "neural skimmer") scores each line of the file read output for relevance to the goal hint.
3. An **adaptive thresholding** mechanism selects lines above a dynamic relevance score — not a fixed top-K.
4. The pruned file content (selected lines only) is returned to the agent instead of the full file.
The classifier was trained on synthetically diversified code-question pairs.

**Impact on task success:**
- SWE-bench Verified: **23–38% token reduction** while **improving success rates by 1.2–1.4 percentage points**. Interaction rounds decreased 18–26%.
- SWE-QA: **29–54% token reduction** with similar success improvement.
- Single-turn LongCodeQA: up to **14.84x compression** with minimal performance impact.
- Mechanism: pruning forces agents to locate issues more precisely and make more decisive decisions, reducing repeated exploratory file reads and completing tasks earlier.

**Necessary vs redundant reads:**
The paper does not provide a categorical breakdown, but the trajectory analysis shows:
- Agents repeatedly re-read the same files during exploration before finding the bug location.
- Once a fix is decided, previously-read context provides minimal marginal value.
- "Redundant information" = content unrelated to the current goal hint; this is what the skimmer drops.
- With pruning, Claude Sonnet 4.5 maintains similar round counts (within 3% variation), while GLM-4.6 reads more files (29–41% more rounds) but at much lower tokens per read.
- Case study: one failure-to-success scenario showed 83.3% token reduction; successful trajectories showed 30.2% reduction in peak prompt length.

## Implications for steiner

- File reads are the primary optimization target, not bash outputs or edit confirmations.
- SWE-Pruner's 0.6B neural skimmer is not deployable in steiner's local-first/Ollama context (requires separate model inference).
- However, the goal-hint pattern is transferable: when the agent calls `read`, steiner could include context about what it is looking for (which it already does implicitly via `grep` patterns). This argues for preferring bounded tools (`grep`, `glob`) over open-ended `read` for exploration.
- The 67–76% figure validates that observation masking (Q1) applied specifically to read results would capture the majority of savings without a neural component.
- The interaction round reduction (18–26%) with pruning suggests that forcing the model to get less data per read may actually improve decision quality — this supports truncating read outputs by default (steiner's existing `read` tool has offset/limit pagination).

## Risks and Uncertainties

- SWE-Pruner requires a task-aware goal hint from the agent at read time. If the agent does not produce a coherent goal, the skimmer cannot filter meaningfully.
- The 0.6B skimmer has its own inference cost; for small local models, this may be comparable to the main model cost.
- GLM-4.6's increased exploration rounds after pruning (29–41% more) could be a steiner failure mode if the local model is similarly conservative: the model reads more files but each read is smaller, so total tokens may not drop as much as expected.
- No data for models below 7B or for 7B–14B range that steiner commonly targets.

## Sources

- arXiv:2601.16746 — "SWE-Pruner: Self-Adaptive Context Pruning for Coding Agents", Wang et al., ACL 2026.
- Code: https://github.com/Ayanami1314/swe-pruner

## Open Questions

- At what file size does a `read` result become "redundant"? Is there a line-count threshold below which full output is always worth keeping?
- Would capping `read` output at N lines (hard truncation) capture most of SWE-Pruner's gains without the skimmer?
- Does steiner's existing `read` tool pagination get used effectively by models, or do models always request unlimited reads?

---

## Question
For offline token estimation in Go (Ollama/local model setups): is byte_count/4 reasonable, are there lightweight Go tokenizer libraries, what do other Go LLM tools use, and what method gives the best accuracy/simplicity tradeoff for threshold decisions?

## Findings

**steiner's existing approach (already implemented):**
steiner uses `github.com/tiktoken-go/tokenizer` v0.7.0 — a pure-Go implementation of OpenAI's tiktoken BPE tokenizer (430 stars). The `encodingNameForModel` function maps known model name prefixes to specific encodings (O200kBase for gpt-4o/o-series, Cl100kBase for gpt-4/gpt-3.5, P50kBase for code-davinci, R50kBase for legacy). For any unknown model name (including all Ollama model names like `llama3.2`, `qwen2.5-coder`, `deepseek-r1`) the function falls back to **Cl100kBase** (cl100k, the GPT-4 tokenizer). This is confirmed by the test: `"unknown-model"` and `""` both map to `Cl100kBase`.

**Accuracy of cl100k for Ollama models:**
- cl100k_base is a BPE tokenizer trained on text similar to GPT-4's pretraining data.
- Most open-source models (Llama, Qwen, DeepSeek, Mistral, Gemma) use different tokenizers (SentencePiece or custom BPE). Token counts will differ.
- Empirically: cl100k tends to produce counts within 10–20% of actual for English prose and typical code. For models with larger vocabularies (e.g., Qwen's 150k-token vocabulary), cl100k may overcount by 15–30% since Qwen tokens are denser.
- For threshold decisions (not billing): overcounting is conservative and safe — steiner will trigger compaction earlier than necessary, which is acceptable.

**Is byte_count/4 reasonable?**
- OpenAI's own rule of thumb is "~4 characters per token for English text" (equivalent to ~4 bytes for ASCII/Latin-1 content).
- For code: identifiers, punctuation, and whitespace tokenize more granularly. Empirical ratio is closer to 3–3.5 chars/token for typical Go source.
- Error margin for byte_count/4: roughly ±20–35% depending on content type. Higher error than BPE estimation but acceptable for threshold decisions with a generous safety margin.
- byte_count/4 systematically undercounts code (may underestimate by 15–25%), making it less conservative than cl100k — a concern for a "will this fit?" decision.

**Available Go tokenizer libraries:**
1. `github.com/tiktoken-go/tokenizer` (430 stars) — pure Go, no CGo, BPE. Already used by steiner. Supports O200kBase, Cl100kBase, P50kBase, R50kBase. Requires BPE dictionary files (downloaded on first use, cacheable via TIKTOKEN_CACHE_DIR env var; an offline loader variant exists via `tiktoken-go-loader`).
2. `github.com/pkoukk/tiktoken-go` (912 stars) — Go port of OpenAI tiktoken; similar capabilities, more stars, also requires BPE files.
3. `github.com/lancekrogers/tcount/tokenizer` — smaller/less known.

**What other Go LLM tools use:**
- langchaingo (`github.com/tmc/langchaingo`) does not implement local token counting; it relies on `MaxTokens` as a parameter, not a pre-flight estimator.
- Ollama's own API returns `prompt_eval_count` and `eval_count` in responses — exact token counts from the model's actual tokenizer, but only available after inference, not before.
- Ollama's OpenAI-compatible API returns `usage.prompt_tokens` and `usage.completion_tokens` post-hoc.

**Best accuracy/simplicity tradeoff:**
For threshold decisions (not billing), ranked:
1. **tiktoken-go/tokenizer with cl100k fallback** (current steiner approach) — best accuracy for OpenAI models, reasonable approximation for Ollama models, no network calls if BPE files are cached. Already in use; no change needed.
2. **byte_count/4 with a conservative multiplier** (e.g., `len(bytes)/3` for code) — zero dependencies, zero latency, acceptable for coarse budget decisions if safety margin is large (≥20%).
3. **Ollama post-hoc feedback loop** — use `prompt_eval_count` from Ollama responses to calibrate or validate estimates over time, but cannot be used for pre-flight decisions.

## Implications for steiner

- No change required to the token estimation approach — steiner's cl100k fallback for unknown models is already the best practical option.
- The cl100k fallback will overestimate for high-vocabulary models (Qwen family) and may modestly underestimate for models with small vocabularies, but both are within acceptable bounds for threshold decisions.
- To improve Ollama accuracy: consider reading `prompt_eval_count` from Ollama responses and using it to emit a calibration log or drift metric. Do not change pre-flight estimation based on this — it is post-hoc.
- If offline/air-gapped operation is required, use `tiktoken-go-loader` to embed the BPE dictionary instead of downloading it at runtime.
- Do not replace BPE estimation with byte_count/4 — it systematically undercounts code content and would make the "fits in context?" decision less conservative.

## Risks and Uncertainties

- tiktoken-go/tokenizer v0.7.0 does not know about model-specific tokenizers for Llama, Mistral, Qwen, etc. This is a known gap; no Go library currently supports all open-source model tokenizers natively.
- BPE dictionary download on first use could fail in air-gapped environments unless the offline loader is configured.
- Qwen 2.5+ models with 150k vocabulary will produce systematically fewer tokens than cl100k estimates (cl100k overcounts by ~15–30%). This is conservative for safety but could cause premature compaction.

## Sources

- `github.com/tiktoken-go/tokenizer` — https://github.com/tiktoken-go/tokenizer (430 stars, pure Go)
- `github.com/pkoukk/tiktoken-go` — https://github.com/pkoukk/tiktoken-go (912 stars)
- OpenAI token counting cookbook: https://github.com/openai/openai-cookbook/blob/main/examples/How_to_count_tokens_with_tiktoken.ipynb
- Ollama API types (PromptEvalCount): https://github.com/ollama/ollama/blob/main/api/types.go
- steiner implementation: `/home/luis/Projects/AI/steiner/internal/provider/token_estimator.go`

## Open Questions

- At what Qwen/Llama token vocabulary size does cl100k error exceed 30%? Would a simple model-family prefix check (e.g., "qwen" → divide cl100k estimate by 1.2) improve accuracy enough to matter?
- Should steiner track actual prompt_eval_count from Ollama responses and compare against pre-flight estimates to surface systematic miscalibration?

---

## Question
Do coding agents or LLM frameworks use a structured scratchpad/state block the model updates each turn? What is the prior art, how do small models handle structured formats, what failure modes exist, and what format simplicity recommendations exist?

## Findings

**Prior art on structured scratchpad/state blocks:**

1. **ReAct (Yao et al., 2022, arXiv:2210.03629):** The foundational pattern for interleaving reasoning and acting. Each turn produces `Thought: ... Action: ... Observation: ...` — a structured but free-text scratchpad. The model writes its own reasoning trace as part of each turn. This is the closest widely-adopted prior art: a per-turn structured output that the model both reads and writes.

2. **SWE-agent "state" injection:** SWE-agent injects a state block at each turn containing `current open file`, `current line`, and `window` (visible lines). This is a minimal structured block injected by the scaffold, not written by the model. It gives the model orientation without asking it to maintain state itself.

3. **Springdrift "sensorium" (Brady, 2025, arXiv:2604.04660):** A persistent runtime for LLM agents that injects a "structured self-state representation (the sensorium)" into each cycle without tool calls. The sensorium is injected by the system, not produced by the model. The paper reports a 23-day deployment where the agent used this to maintain cross-session context. The sensorium is described as "ambient self-perception" — a passive read for the model, not a write target.

4. **DeepCode "stateful code memory" (arXiv:2512.07921):** Uses "structured indexing using stateful code memory" as one of four information operations. The code memory is a scaffold-maintained data structure, not a model-written scratchpad.

5. **OpenHands / Cursor:** Use LLM-Summary to maintain a running summary (as a scaffold-maintained state block replacing old history). The model writes the summary as output to a specific summarization call, not as part of its normal reasoning turn.

**How small models (7B–35B) handle structured output formats:**

From arXiv:2408.11061 (StructuredRAG, Shorten et al., 2024) — benchmark of 6 JSON formatting tasks across Gemini 1.5 Pro and Llama 3 8B-instruct:
- **Average success rate: 82.55%** across 24 experiments.
- High variance: success rates ranged from 0 to 100% depending on task, model, and prompting strategy.
- Llama 3 8B often performed competitively with Gemini 1.5 Pro.
- Task complexity is the primary determinant: tasks involving **lists or composite object outputs** had the lowest success rates.
- Simple flat key-value outputs were reliable even for smaller models.

From arXiv:2510.03847 (SLMs for Agentic Systems survey, Sharma & Mehta, 2025):
- **Guided decoding** (XGrammar, Outlines) + strict JSON Schema effectively closes most of the gap between SLMs and LLMs for schema-constrained tasks.
- Schema-first prompting + type-safe outputs are the recommended pattern for production SLM deployments.
- Without guided decoding, 7B–12B models show unreliable schema adherence on complex nested structures.
- Simple flat schemas (3–5 fields, all strings/enums) are reliable even without guided decoding.

**Failure modes for model-written structured state blocks:**
1. **Format drift:** Over long trajectories, the model gradually diverges from the required format — omitting delimiters, changing field names, adding free-text commentary around the block.
2. **Field omission:** Under token pressure or when the model decides a field is "not applicable," it silently omits required fields. Composite/nested fields are most prone to omission.
3. **Verbosity creep:** Models, especially instruction-tuned ones, pad fields with explanatory prose. A field intended to hold a short string like `"write auth module"` becomes a paragraph.
4. **Copy-forward errors:** The model copies the previous turn's state block without updating it, producing a stale state that never changes.
5. **Hallucinated fields:** The model invents field names not in the schema.
6. **Interleaving confusion:** When the state block appears in the middle of a long context, smaller models sometimes treat it as part of the conversation content rather than a special structured section.

**Format simplicity recommendations:**

From the literature and empirical results:
- **Prefer tagged-text over JSON for model-written state** (e.g. `<goal>fix auth bug</goal>` vs `{"goal": "fix auth bug"}`). Tagged text is more tolerant of partial omission — the parser can extract what's present.
- **Minimal field count:** 3–5 fields maximum for reliable small-model compliance. Each additional field increases omission probability.
- **Flat structure:** No nesting. A single-level key-value block is far more reliable than nested objects or arrays.
- **Short field values:** Use short, imperative strings. Avoid fields that invite multi-sentence responses.
- **Anchor in system prompt with example:** Providing a concrete example of the filled-out block in the system prompt significantly improves adherence.
- **Scaffold-written vs model-written:** The most reliable pattern is for the scaffold to maintain state and inject it read-only into each turn (SWE-agent, Springdrift approach), rather than asking the model to update a state block as part of its response. If model-writes are required, use guided decoding (Ollama supports `format: json` for constrained generation).

## Implications for steiner

- A model-written structured scratchpad is risky without guided decoding, especially for local 7B–32B models. Failure modes (format drift, field omission, verbosity creep) compound over long sessions.
- The scaffold-written injection pattern (SWE-agent style) is more reliable: steiner maintains the state block; the model reads it but does not write it. This is lower risk and still provides orientation benefit.
- If a model-written state block is desired (e.g. for active goal tracking), Ollama's `format: json` with a strict JSON Schema is the safest approach. This requires sending the schema to Ollama and only works for Ollama-served models.
- For a simple per-turn "what is the current goal?" field: tagged text with 1–2 fields, anchor example in system prompt, parsed leniently (extract what's present). Do not depend on all fields being present every turn.
- Keep any state block under 5 fields, all flat strings. Avoid arrays or nested objects.
- The copy-forward failure mode (model copies previous state unchanged) means state blocks are not reliable as a "what changed this turn" signal — use them only for slow-changing state (current task goal, current file focus).

## Risks and Uncertainties

- StructuredRAG (82.55% average) was measured on Llama 3 8B and Gemini 1.5 Pro, not on coding-specific fine-tunes (Qwen-coder, DeepSeek-coder). Code-focused fine-tunes may have better or worse JSON adherence.
- No direct study of format drift over long (50+ turn) trajectories was found. Most evaluations are single-turn or short session.
- Ollama's `format: json` constrains to valid JSON but does not enforce schema field presence — it prevents syntax errors but not semantic omission.
- XGrammar/Outlines guided decoding for schema compliance is not currently available in steiner's provider layer.

## Sources

- arXiv:2210.03629 — ReAct: Synergizing Reasoning and Acting in Language Models (Yao et al., 2022)
- arXiv:2604.04660 — Springdrift: An Auditable Persistent Runtime for LLM Agents (Brady, 2025)
- arXiv:2512.07921 — DeepCode: Open Agentic Coding (Li et al., 2024)
- arXiv:2408.11061 — StructuredRAG: JSON Response Formatting with Large Language Models (Shorten et al., 2024)
- arXiv:2510.03847 — Small Language Models for Agentic Systems: A Survey (Sharma & Mehta, 2025)
- arXiv:2411.15100 — XGrammar: Flexible and Efficient Structured Generation Engine for LLMs
- SWE-agent state injection: https://github.com/SWE-agent/SWE-agent

## Open Questions

- Would a scaffold-injected state block (goal + current focus file, updated by steiner logic) provide enough orientation benefit to justify the complexity?
- What is the actual format adherence rate for Qwen2.5-coder-7B and DeepSeek-coder-7B on a simple 3-field tagged-text block over 30+ turns?
- Is there measurable agent performance benefit (task completion, fewer re-reads) from injecting even a minimal state block, separate from context management?
