# Research: System Prompt Structure for LLM Instruction Adherence

## Question

How should a coding agent's system prompt be structured to maximize instruction adherence across both cloud models (Claude, GPT-4) and small local models (7B-70B), while being token-economic and KV-cache-friendly?

## Findings

### 1. Positional Attention Effects

The "Lost in the Middle" phenomenon (Stanford, Liu et al. 2023) shows a U-shaped attention curve: LLMs attend most to the beginning and end of context, with a valley in the middle. For system prompts:

- **Opening lines receive highest attention weight.** Identity and critical constraints belong here.
- **Mid-prompt constraints are most at risk** of being ignored, especially as conversation grows.
- **Recency bias** means the last system prompt lines before the first user turn get boosted attention.
- **Mitigation**: Place highest-priority constraints at both the beginning (primacy) and restate briefly at the end (recency).

### 2. Structural Formatting

- **Markdown `##` headers** are the best cross-model section delimiter (widely understood by all model families).
- **XML tags** (`<rules>`, `<tools>`) are additionally effective for Claude specifically.
- **Numbered lists** imply ordered sequential steps; **bullet lists** imply parallel constraints of equal weight.
- **Bold emphasis** effective when used sparingly (2-3 per section max); overuse nullifies it.
- **Prose walls** read as one undifferentiated thing — sectioned prompts with clear labels dramatically improve parsing.
- **Small models** are more sensitive to formatting ambiguity — explicit, unambiguous section labels are critical.

### 3. Token Economy

- **Irrelevant content actively degrades quality**, not just efficiency. Semantically adjacent but slightly different content is more damaging than unrelated noise.
- **Moderate compression (up to ~4x) is safe** with minimal quality loss.
- **Removing verbose filler** (pleasantries, hedging, redundant restatements) typically cuts 10-20% with zero meaning loss.
- **Do not strip edge-case constraints.** Brevity that removes infrequently-triggered rules costs correctness precisely when those rules matter.
- **Contradictory statements** at any position cause confused/inconsistent outputs — this is the highest-risk form of prompt bloat.

### 4. Identity and Role Framing

- **Single strong identity statement at the top** is optimal. Repeated identity references provide no additive benefit and waste tokens.
- **Positive framing outperforms negative.** "Do X" is better followed than "Don't do Y."
- **Role framing primarily shapes tone and domain vocabulary**, not factual accuracy. For a coding agent, it usefully shifts toward code-centric responses.
- **For small models**: consider a one-line identity restatement as the very last system prompt line to exploit recency bias.

### 5. Composability

- **Lifecycle ordering**: identity/role > non-negotiable constraints > tool policy > task conventions > output format. Higher = more critical = more attention.
- **Avoid semantic overlap between sections.** Overlapping content with slight contradictions is the most damaging defect.
- **Explicit priority declarations** work when sections might conflict: "When X conflicts with Y, X takes precedence."
- **Stable prefix first, variable content last.** Dynamic/conditional sections at the bottom preserve KV cache for the stable head.

### 6. Small Model Considerations (7B-32B)

- **Keyword anchoring**: Small models pattern-match on familiar preamble patterns from training data rather than parsing full prompts. Qwen-7B overfit to "You are a helpful assistant" keywords.
- **Higher instruction decay rate**: Multi-instruction adherence drops exponentially steeper at smaller scales.
- **Section ignoring**: Mistral-7B showed 100% fault rates on structured instruction-following tasks.
- **Mitigations**: Shorter/more focused prompts, simpler language, flat numbered lists over nested conditionals, critical constraint restatement at prompt end.

### 7. KV Cache Implications

- **Stable prefix = free computation.** Byte-identical prefix tokens across requests are computed once and reused (vLLM, llama.cpp).
- **Any token change in the prefix invalidates all subsequent cached blocks.**
- **Practical**: For a 2000-token prompt that is 80% stable, each request saves prefill cost of ~1600 tokens. At high request volume, this is substantial for local inference where prefill is the bottleneck.
- **Design rule**: Stable base sections at top, dynamic/per-request content at bottom.

## Implications

For steiner's system prompt restructure:

1. **Single identity sentence, line 1.** No repetition elsewhere.
2. **Core behavioral rules immediately after identity** — these are the highest-priority constraints and get primacy attention.
3. **Tool policy and delegation** in the middle — important but less critical than core rules.
4. **Style modifiers (caveman, humanizer) at the end** — naturally the lowest-priority additions, and being at the tail exploits recency bias while keeping the stable prefix intact for KV cache.
5. **System suffix absolutely last** — dynamic content must not invalidate the stable prefix.
6. **DRY between main and sub-agent prompts** — shared base eliminates semantic overlap/contradiction risk.
7. **Numbered lists for constraints, `##` headers for sections** — best cross-model compatibility.
8. **Terse, positive framing** throughout — "do X" not "don't do Y"; strip filler.

## Risks and Uncertainties

- Small model behavior varies significantly across base families and quantization levels — testing with specific models is essential.
- The attention research is primarily on retrieval tasks; coding agent instruction following may have different dynamics.
- KV cache benefits depend on inference server implementation — not all setups support prefix caching.

## Sources

1. [Lost in the Middle (arXiv 2307.03172)](https://arxiv.org/abs/2307.03172) — positional attention, Stanford
2. [Runtime Reinforcement: Preventing Instruction Decay (Towards AI)](https://towardsai.net/p/machine-learning/runtime-reinforcement-preventing-instruction-decay-in-long-context-windows) — instruction decay
3. [Marking Up the Prompt: How Markdown Formatting Influences LLM Responses (NeuralBuddies)](https://www.neuralbuddies.com/p/marking-up-the-prompt-how-markdown-formatting-influences-llm-responses) — formatting effects
4. [Mixture-of-Instructions (arXiv 2404.18410)](https://arxiv.org/pdf/2404.18410) — Qwen-7B position sensitivity
5. [Impact of Prompt Bloat on LLM Output Quality (MLOps Community)](https://home.mlops.community/public/blogs/the-impact-of-prompt-bloat-on-llm-output-quality) — bloat effects
6. [Style-Compress (arXiv 2410.14042)](https://arxiv.org/pdf/2410.14042) — compression research
7. [Role-Prompting Analysis (PromptHub)](https://www.prompthub.us/blog/role-prompting-does-adding-personas-to-your-prompts-really-make-a-difference) — role framing evidence
8. [Building Effective AI Coding Agents for the Terminal (arXiv 2603.05344)](https://arxiv.org/pdf/2603.05344) — agent prompt architecture
9. [Automatic Prefix Caching (vLLM docs)](https://docs.vllm.ai/en/stable/design/prefix_caching/) — KV cache mechanics
10. [Persistent Q4 KV Cache (arXiv 2603.04428)](https://arxiv.org/html/2603.04428v1) — edge device KV reuse

## Open Questions

- How much does quantization (Q4 vs Q8) affect system prompt instruction following specifically?
- At what system prompt length do small models start dropping entire sections reliably?
- Is there a measurable difference between `##` headers and numbered top-level sections for small models?
