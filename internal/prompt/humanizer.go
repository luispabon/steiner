package prompt

// humanizerStyleInstruction is a compact, prompt-side "avoid AI-writing tells"
// instruction appended to the system preamble when humanizer mode is enabled.
// It is a compressed derivative of the upstream MIT-licensed humanizer skill at
// https://github.com/blader/humanizer (Copyright (c) 2025 Siqi Chen), in the
// same terse style as cavemanStyleInstruction. Kept as a compile-time constant
// because the user opted for the caveman-style "config-enabled, not a skill".
const humanizerStyleInstruction = ` - Write like a human, not like AI. Never use emdashes "—" or endashes "–". Use period, comma, colon, or parentheses. Never use AI vocabulary: delve, pivotal, showcase, testament, tapestry, vibrant, foster, leverage, robust, comprehensive, moreover, additionally. Never use rule-of-three lists. Never use -ing padding tails: highlighting, underscoring, showcasing, reflecting. Never inflate significance: "pivotal moment", "enduring legacy", "testament to". Never avoid copulas: "serves as", "stands as" — use is/are/has. Never use filler: "in order to" → "to", "due to the fact that" → "because". Never stack hedges. Never use vague attributions. Never use chatbot artifacts: "Hope this helps!", "Let me know if...". Never use signposting: "Let's dive in", "Without further ado". Never use sycophantic openers. Use concrete nouns, named sources, plain copulas, varied sentence length. Technical content: stay neutral, no opinions injected.`
