package agent

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// requestBlockHeaderPrefix starts the trailer a compaction summary carries. The
// header's byte count makes the trailer length-delimited, so arbitrary user text
// (including header-like lines) cannot break parsing.
const requestBlockHeaderPrefix = "[steiner:latest-request bytes="

// requestBlockMaxBytes caps the verbatim request kept in a summary. The block
// is resent with every request after compaction, so it must stay small.
const requestBlockMaxBytes = 16 * 1024

// withRequestBlock appends the length-delimited latest-request trailer to body.
// An empty request leaves body unchanged.
func withRequestBlock(body, request string) string {
	if request == "" {
		return body
	}
	header := fmt.Sprintf("%s%d]", requestBlockHeaderPrefix, len(request))
	return body + "\n\n" + header + "\n" + request
}

// SplitRequestBlock splits a stored compaction summary into its body and the
// verbatim latest-request trailer. A summary with no valid trailer reports
// ok=false and body unchanged. The trailer is the last header prefix occurrence
// whose byte count ends the summary exactly, so text inside the request cannot
// be mistaken for the header as long as it does not end on a matching count.
func SplitRequestBlock(summary string) (body, request string, ok bool) {
	searchEnd := len(summary)
	for {
		idx := strings.LastIndex(summary[:searchEnd], requestBlockHeaderPrefix)
		if idx < 0 {
			return summary, "", false
		}
		requestStart, size, parsed := parseRequestBlockHeader(summary[idx:])
		if parsed && size > 0 && idx+requestStart+size == len(summary) && strings.HasSuffix(summary[:idx], "\n\n") {
			prefix := idx - 2
			return strings.TrimRight(summary[:prefix], " \t\r\n"), summary[idx+requestStart:], true
		}
		searchEnd = idx
	}
}

// parseRequestBlockHeader parses a header at the start of text, returning the
// byte offset where the request begins and its declared byte count.
func parseRequestBlockHeader(text string) (requestStart, size int, ok bool) {
	rest, found := strings.CutPrefix(text, requestBlockHeaderPrefix)
	if !found {
		return 0, 0, false
	}
	digits, tail, found := strings.Cut(rest, "]\n")
	if !found || digits == "" {
		return 0, 0, false
	}
	size, err := strconv.Atoi(digits)
	if err != nil || size < 0 {
		return 0, 0, false
	}
	return len(text) - len(tail), size, true
}

// capRequestBlock truncates request to requestBlockMaxBytes on a UTF-8 boundary
// and marks how many bytes were dropped.
func capRequestBlock(request string) string {
	if len(request) <= requestBlockMaxBytes {
		return request
	}
	cut := requestBlockMaxBytes
	for cut > 0 && !utf8.RuneStart(request[cut]) {
		cut--
	}
	return request[:cut] + fmt.Sprintf("\n[truncated: %d bytes omitted]", len(request)-cut)
}

// latestRequestBlock selects the request block for a compaction stage whose
// source is sourceMessages: the newest real user message still being summarized,
// else the block carried by the newest summary message in the source.
func latestRequestBlock(sourceMessages []Message) (request, status string) {
	for i := len(sourceMessages) - 1; i >= 0; i-- {
		message := sourceMessages[i]
		if !IsRealUserMessage(message) {
			continue
		}
		if text := strings.TrimSpace(prompt.StripSkillBlocks(message.Content)); text != "" {
			return capRequestBlock(text), output.RequestBlockNew
		}
	}
	for i := len(sourceMessages) - 1; i >= 0; i-- {
		message := sourceMessages[i]
		if message.Role != MessageRoleSummary {
			continue
		}
		if _, request, ok := SplitRequestBlock(message.Content); ok {
			return request, output.RequestBlockCarried
		}
	}
	return "", output.RequestBlockNone
}

// stripRequestBlocks returns a copy of messages without the latest-request
// trailer on summary messages, so a summarizer never sees or re-quotes it.
func stripRequestBlocks(messages []Message) []Message {
	out := cloneMessages(messages)
	for i := range out {
		if out[i].Role != MessageRoleSummary {
			continue
		}
		if body, _, ok := SplitRequestBlock(out[i].Content); ok {
			out[i].Content = body
		}
	}
	return out
}
