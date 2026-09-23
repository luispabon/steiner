package provider

import (
	"encoding/json"
	"strings"
)

// MessageHashInput returns the exact message data used by cache and output
// message fingerprints. It preserves role, content, reasoning content,
// tool-call order, each tool-call name, successfully marshaled arguments, raw
// arguments, and image order with each image's media type and data. Callers
// hash the result, so it never carries content into diagnostics output.
func MessageHashInput(msg Message) string {
	var b strings.Builder
	b.WriteString(string(msg.Role))
	b.WriteString(msg.Content)
	b.WriteString(msg.ReasoningContent)
	for _, call := range msg.ToolCalls {
		b.WriteString(call.Name)
		if arguments, err := json.Marshal(call.Arguments); err == nil {
			b.Write(arguments)
		}
		b.WriteString(call.RawArguments)
	}
	for _, image := range msg.Images {
		b.WriteString(image.MediaType)
		b.WriteString(image.Data)
	}
	return b.String()
}
