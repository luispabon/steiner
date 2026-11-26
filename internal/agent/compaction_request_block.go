package agent

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/luispabon/steiner/internal/output"
	"github.com/luispabon/steiner/internal/prompt"
)

// requestBlockMarker starts the trailer a compaction summary carries. The
// header's byte count makes the trailer length-delimited, so arbitrary user text
// (including header-like lines) cannot break parsing.
const requestBlockMarker = "[steiner:latest-request"

// requestBlockHeaderPrefix starts the trailer header proper.
const requestBlockHeaderPrefix = requestBlockMarker + " bytes="

// requestBlockSanitizedMarker replaces every header-like occurrence inside a
// summary body, so only the real trailer can parse as a block.
const requestBlockSanitizedMarker = "[steiner latest-request"

// requestBlockMaxBytes caps the verbatim request kept in a summary. The block
// is resent with every request after compaction, so it must stay small.
const requestBlockMaxBytes = 16 * 1024

// requestBlockWindowPercent bounds the block at 5% of the context window, so a
// small window cannot have its fit decided by the block alone.
const requestBlockWindowPercent = 5

// requestBlockBytesPerToken is the provider heuristic estimator ratio: its
// heuristic counter divides request characters by 4.
const requestBlockBytesPerToken = 4

// withRequestBlock appends the length-delimited latest-request trailer to body.
// The body is sanitized first, so header-like text it carries can never be
// mistaken for the real trailer. An empty request leaves the sanitized body.
func withRequestBlock(body, request string) string {
	body = strings.ReplaceAll(body, requestBlockMarker, requestBlockSanitizedMarker)
	if request == "" {
		return body
	}
	header := fmt.Sprintf("%s%d]", requestBlockHeaderPrefix, len(request))
	return body + "\n\n" + header + "\n" + request
}

// SplitRequestBlock splits a stored compaction summary into its body and the
// verbatim latest-request trailer. Only summary text carries a trailer, so
// callers must pass MessageRoleSummary content: a user message that types the
// marker is never parsed. A summary with no valid trailer reports ok=false and
// body unchanged.
//
// The first header after a blank line wins. It is accepted only when it parses
// and its byte count ends the summary exactly, so header-like text in the body
// (sanitized by withRequestBlock) or a shorter count cannot truncate the body.
// Body and request are returned exactly as stored.
func SplitRequestBlock(summary string) (body, request string, ok bool) {
	idx := strings.Index(summary, "\n\n"+requestBlockHeaderPrefix)
	if idx < 0 {
		return summary, "", false
	}
	start := idx + len("\n\n")
	requestStart, size, parsed := parseRequestBlockHeader(summary[start:])
	if !parsed || size <= 0 || start+requestStart+size != len(summary) {
		return summary, "", false
	}
	return summary[:idx], summary[start+requestStart:], true
}

// parseRequestBlockHeader parses a header at the start of text, returning the
// byte offset where the request begins and its declared byte count. The count is
// digits only: a sign, an empty count, or any other character is not a header.
func parseRequestBlockHeader(text string) (requestStart, size int, ok bool) {
	rest, found := strings.CutPrefix(text, requestBlockHeaderPrefix)
	if !found {
		return 0, 0, false
	}
	digits, tail, found := strings.Cut(rest, "]\n")
	if !found || digits == "" {
		return 0, 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, 0, false
		}
	}
	size, err := strconv.Atoi(digits)
	if err != nil {
		return 0, 0, false
	}
	return len(text) - len(tail), size, true
}

// requestBlockCapBytes returns the effective verbatim request cap: the flat
// maximum, or 5% of contextWindow in bytes when that is smaller. An unknown
// window keeps the flat maximum.
func requestBlockCapBytes(contextWindow int) int {
	if contextWindow <= 0 {
		return requestBlockMaxBytes
	}
	limit := contextWindow * requestBlockWindowPercent / 100 * requestBlockBytesPerToken
	if limit < requestBlockMaxBytes {
		return limit
	}
	return requestBlockMaxBytes
}

// capRequestBlock truncates request to limit bytes on a UTF-8 boundary and
// marks how many bytes were dropped.
func capRequestBlock(request string, limit int) string {
	if len(request) <= limit {
		return request
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(request[cut]) {
		cut--
	}
	return request[:cut] + fmt.Sprintf("\n[truncated: %d bytes omitted]", len(request)-cut)
}

// latestRequestBlock selects the request block for a compaction stage whose
// source is sourceMessages: the newest real user message that still has text to
// keep, else the block carried by the newest summary message in the source.
// Skill-only, image-only, and sourced messages are not requests. Both paths go
// through the effective cap, so a carried block shrinks when the window does. A
// window whose effective cap rounds down to zero keeps no block at all, so a
// tiny window cannot reduce the stored request to a bare truncation marker.
func latestRequestBlock(sourceMessages []Message, contextWindow int) (request, status string) {
	limit := requestBlockCapBytes(contextWindow)
	if limit <= 0 {
		return "", output.RequestBlockDropped
	}
	for i := len(sourceMessages) - 1; i >= 0; i-- {
		message := sourceMessages[i]
		if !IsRealUserMessage(message) {
			continue
		}
		if text := requestMessageText(message); text != "" {
			return capRequestBlock(text, limit), output.RequestBlockNew
		}
	}
	for i := len(sourceMessages) - 1; i >= 0; i-- {
		message := sourceMessages[i]
		if message.Role != MessageRoleSummary {
			continue
		}
		if _, request, ok := SplitRequestBlock(message.Content); ok {
			return capRequestBlock(request, limit), output.RequestBlockCarried
		}
	}
	return "", output.RequestBlockNone
}

// requestMessageText returns the user's own words from a real user message:
// RequestText when the message records it, else the text recovered from the
// stored content. Image handling records the user's words before it rewrites a
// message, so marked content supplies the request only when the rewrite is all
// there is, which is the empty string request selection skips.
func requestMessageText(message Message) string {
	if text := strings.TrimSpace(prompt.StripSkillBlocks(message.RequestText)); text != "" {
		return text
	}
	if message.VisionRouted {
		return ""
	}
	return legacyRequestText(message.Content)
}

// UserRequestText returns the user's own words from a real user message: the
// recorded request text when the message has it, else the text recovered from
// the stored content. It returns empty for a message whose content image
// handling rewrote without recording any user text.
func UserRequestText(message Message) string {
	return requestMessageText(message)
}

// legacyRequestText recovers the user's text from a delivery message stored
// before Message.RequestText existed. BuildDeliveryMessage writes the mode
// notice, skill blocks, pending line, then sub-agent envelopes, then the user
// text, so the structural parser leaves exactly the user text. Anything after
// the last envelope close tag also wins, so a malformed or truncated envelope
// cannot leak into the request.
func legacyRequestText(content string) string {
	rest := prompt.SplitMessageBlocks(content).Rest
	if idx := strings.LastIndex(rest, subAgentResultCloseTag); idx >= 0 {
		rest = rest[idx+len(subAgentResultCloseTag):]
	}
	return strings.TrimSpace(rest)
}

// stripRequestBlockSource returns messages without the latest-request trailer on
// summary messages, reporting whether any summary carried one.
func stripRequestBlockSource(messages []Message) ([]Message, bool) {
	for _, message := range messages {
		if message.Role != MessageRoleSummary {
			continue
		}
		if _, _, ok := SplitRequestBlock(message.Content); ok {
			return stripRequestBlocks(messages), true
		}
	}
	return messages, false
}

// stripRequestBlocks returns a copy of messages without the latest-request
// trailer on summary messages, for sources that must not re-quote the verbatim
// request, such as the history archive note.
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

// dropRequestBlockState rewrites the latest generation's plain summary messages
// to their body without the latest-request trailer, reporting the rewritten
// summary text. Segments are immutable, so they keep their bytes, matching
// dropTailRequestBlockState.
func dropRequestBlockState(state RunState) (RunState, string, bool) {
	next := state.Clone()
	if len(next.Lineage.Generations) == 0 {
		return state, "", false
	}
	latest := &next.Lineage.Generations[len(next.Lineage.Generations)-1]
	summaryText := ""
	dropped := false
	for i := range latest.SummaryPrefix {
		message := &latest.SummaryPrefix[i]
		// Segments are never rewritten; only a plain summary loses its trailer.
		if message.Role != MessageRoleSummary || message.Segment != nil {
			continue
		}
		if body, _, ok := SplitRequestBlock(message.Content); ok {
			message.Content = body
			summaryText = body
			dropped = true
		}
	}
	if !dropped {
		return state, "", false
	}
	next.Conversation = next.Lineage.FullMessages()
	return next, summaryText, true
}
