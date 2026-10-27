package tool

import "context"

// KindInvalidToolInput is the ToolExecutionError kind for a malformed tool call.
const KindInvalidToolInput = "invalid_tool_input"

// toolInputDecodeErrorKey is the context key carrying the provider's decode
// error for a malformed tool call into Execute.
type toolInputDecodeErrorKey struct{}

// WithToolInputDecodeError marks ctx so Execute rejects the call as
// invalid_tool_input, quoting msg. An empty msg returns ctx unchanged.
func WithToolInputDecodeError(ctx context.Context, msg string) context.Context {
	if msg == "" {
		return ctx
	}
	return context.WithValue(ctx, toolInputDecodeErrorKey{}, msg)
}

// toolInputDecodeError returns the decode error attached by
// WithToolInputDecodeError, and false when absent.
func toolInputDecodeError(ctx context.Context) (string, bool) {
	msg, ok := ctx.Value(toolInputDecodeErrorKey{}).(string)
	return msg, ok
}
