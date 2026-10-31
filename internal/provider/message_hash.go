package provider

import "encoding/json"

// AppendMessageHashInput appends to dst the exact message data used by cache
// and output message fingerprints. It preserves role, content, reasoning
// content, tool-call order, each tool-call name, successfully marshaled
// arguments, raw arguments, and image order with each image's media type and
// data. Callers hash the result, so it never carries content into diagnostics
// output; appending into a reused buffer never materializes a fingerprint
// string per message.
func AppendMessageHashInput(dst []byte, msg Message) []byte {
	dst = append(dst, msg.Role...)
	dst = append(dst, msg.Content...)
	dst = append(dst, msg.ReasoningContent...)
	for _, call := range msg.ToolCalls {
		dst = append(dst, call.Name...)
		if arguments, err := json.Marshal(call.Arguments); err == nil {
			dst = append(dst, arguments...)
		}
		dst = append(dst, call.RawArguments...)
	}
	for _, image := range msg.Images {
		dst = append(dst, image.MediaType...)
		dst = append(dst, image.Data...)
	}
	return dst
}
