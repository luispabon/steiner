package provider

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

var (
	errDecodeChatCompletionResponse = errors.New("decode chat completion response")
	errDecodeStreamChunk            = errors.New("decode stream chunk")
	errDecodeStreamChunkUnexpected  = errors.New("decode stream chunk unexpected end of JSON input")
	// errResponsesStreamFailed marks a terminal response.failed event. The
	// server rejected the response itself, so resending would fail the same way.
	errResponsesStreamFailed = errors.New("responses stream failed")
	// errResponsesStreamFailedTransient marks a response.failed event whose
	// error code signals a transient server condition worth another attempt.
	errResponsesStreamFailedTransient = errors.New("responses stream failed transiently")
)

// maxArgumentDecodeErrorBytes bounds ToolCall.ArgumentDecodeError.
const maxArgumentDecodeErrorBytes = 512

// argumentDecodeError marks a tool-argument decode failure that the provider
// records on one call instead of failing the whole response.
type argumentDecodeError struct{ err error }

func (e *argumentDecodeError) Error() string { return e.err.Error() }

func (e *argumentDecodeError) Unwrap() error { return e.err }

// markArgumentDecodeError returns call with empty arguments and the decode
// error recorded in ArgumentDecodeError. The invalid raw text is dropped and
// RawArguments is "{}", so every encoder replays a valid empty object.
func markArgumentDecodeError(call ToolCall, err error) ToolCall {
	call.Arguments = map[string]any{}
	call.RawArguments = "{}"
	call.ArgumentDecodeError = truncateUTF8Bytes(fmt.Sprintf("decode %q arguments: %v", call.Name, err), maxArgumentDecodeErrorBytes)
	return call
}

// truncateUTF8Bytes returns s cut to at most limit bytes on a rune boundary.
func truncateUTF8Bytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
