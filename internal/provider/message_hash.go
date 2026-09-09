package provider

import (
	"encoding/json"
	"strings"
)

// MessageHashInput returns the exact message data used by cache and output
// message fingerprints. It preserves role, content, tool-call order, each
// tool-call name, successfully marshaled arguments, and raw arguments.
func MessageHashInput(msg Message) string {
	var b strings.Builder
	b.WriteString(string(msg.Role))
	b.WriteString(msg.Content)
	for _, call := range msg.ToolCalls {
		b.WriteString(call.Name)
		if arguments, err := json.Marshal(call.Arguments); err == nil {
			b.Write(arguments)
		}
		b.WriteString(call.RawArguments)
	}
	return b.String()
}
