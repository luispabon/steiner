package agent

import (
	"strings"

	"github.com/luispabon/steiner/internal/prompt"
)

// activeSkillBlocks returns the skill blocks still active in the latest
// generation. The scan covers the full generation, so it sees early activation
// messages alongside any late switch retained in the tail.
func activeSkillBlocks(state RunState) []prompt.SkillBlock {
	scan := state.Lineage.FullMessages()
	if len(scan) == 0 {
		scan = state.Conversation
	}
	contents := make([]string, 0, len(scan))
	for _, msg := range scan {
		if msg.Role == MessageRoleUser {
			contents = append(contents, msg.Content)
		}
	}
	return prompt.EffectiveSkills(contents)
}

// stripRetainedSkillBlocks removes skill block envelopes from retained user
// messages, dropping any that become empty. All non-content fields survive.
func stripRetainedSkillBlocks(retained []Message) []Message {
	out := make([]Message, 0, len(retained))
	for _, msg := range retained {
		if msg.Role == MessageRoleUser {
			if _, blocks, _ := prompt.SplitSkillBlocks(msg.Content); len(blocks) > 0 {
				msg.Content = prompt.StripSkillBlocks(msg.Content)
				if msg.Content == "" {
					continue
				}
			}
		}
		out = append(out, msg)
	}
	return out
}

// replayedSkillMessage builds the single user message that re-injects the active
// skill envelopes verbatim, reporting false when there are none.
func replayedSkillMessage(active []prompt.SkillBlock) (Message, bool) {
	if len(active) == 0 {
		return Message{}, false
	}
	texts := make([]string, 0, len(active))
	for _, block := range active {
		texts = append(texts, block.Text)
	}
	return Message{Role: MessageRoleUser, Content: strings.Join(texts, "\n\n")}, true
}
