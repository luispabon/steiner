package prompt

import "strings"

const (
	templateCaveHumanVoice            = "cave_human_voice.md.tmpl"
	templateCompactionCaveHumanBody   = "compaction_cave_human_body.md.tmpl"
	templateCaveHumanCompactionEncode = "compaction_cave_human_encoding.md.tmpl"
)

// caveHumanInstruction is a compact, prompt-side instruction block derived
// from the upstream MIT-licensed humanizer skill at
// https://github.com/blader/humanizer (Copyright (c) 2025 Siqi Chen).
// Kept in-tree as an embedded template because the user opted for the
// caveman-style "config-enabled, not a skill".
func caveHumanInstruction() string {
	return strings.TrimSpace(renderTemplate(templateCaveHumanVoice, nil))
}

// caveHumanCompactionVoice is the unified compaction instruction used when
// cave_human is enabled, combining the compaction system instruction, the
// section body, and dense encoding directives in place of the generic
// user-facing output voice. The encoding directives are dense,
// machine-to-machine rules for compaction output, replacing the user-facing
// voice block (which targets chat responses, not handoff summaries).
func caveHumanCompactionVoice() string {
	return strings.Join([]string{
		compactionPromptSystemInstruction(),
		strings.TrimSpace(renderTemplate(templateCompactionCaveHumanBody, nil)),
		strings.TrimSpace(renderTemplate(templateCaveHumanCompactionEncode, nil)),
	}, "\n\n")
}
