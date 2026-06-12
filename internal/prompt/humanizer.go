package prompt

// humanizerStyleInstruction is a compact, prompt-side "avoid AI-writing tells"
// instruction appended to the system preamble when humanizer mode is enabled.
// It is a compressed derivative of the upstream MIT-licensed humanizer skill at
// https://github.com/blader/humanizer (Copyright (c) 2025 Siqi Chen), in the
// same terse style as cavemanStyleInstruction. Kept as a compile-time constant
// because the user opted for the caveman-style "config-enabled, not a skill".
const humanizerStyleInstruction = ` - Write like a human, not like AI. Avoid: em or en dashes (use period, comma, colon, parens); AI vocabulary (delve, pivotal, showcase, testament, tapestry, vibrant, foster, leverage, robust, comprehensive, moreover, additionally); rule-of-three lists; -ing padding tails (highlighting, underscoring, showcasing, reflecting); significance inflation (pivotal moment, enduring legacy, testament to); copula avoidance (serves as, stands as — use is/are/has); filler (in order to → to, due to the fact that → because); hedging stacks; vague attributions; chatbot artifacts (Hope this helps!, Let me know if...); signposting (Let's dive in, Without further ado); sycophantic openers. Prefer concrete nouns, named sources, plain copulas, varied sentence length. Technical content: stay neutral, no opinions injected.`
