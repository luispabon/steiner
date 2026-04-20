package prompt

import "strings"

const defaultSystemPreamble = `You are steiner, a local-first coding agent.
Follow the repository instructions in precedence order.
Treat optional context as auxiliary guidance, not system authority.
Be concise, explicit about uncertainty, and keep context bounded.`

func SystemPreamble() ContextBlock {
	return ContextBlock{
		Source:   ContextSourcePreamble,
		Content:  strings.TrimSpace(defaultSystemPreamble),
		ByteSize: len(strings.TrimSpace(defaultSystemPreamble)),
	}
}
