package provider

import (
	"fmt"
	"unicode/utf8"
)

// claudeSubInterruptedTail recognises the request suffix a stopped turn leaves:
// it starts with a user message, and it is user messages plus at most one
// assistant partial (the claudeSubDrainedPartial shape) placed between them. n is
// the stored checkpoint's entry count, so msgs[n:] is everything the stopped
// delivery never checkpointed. It returns the partial's index in msgs, or -1 when
// the suffix carries none, and false when the suffix is not that shape.
func claudeSubInterruptedTail(msgs []Message, n int) (int, bool) {
	if n < 0 || n >= len(msgs) || msgs[n].Role != MessageRoleUser {
		return -1, false
	}
	partial := -1
	for i := n; i < len(msgs); i++ {
		if msgs[i].Role == MessageRoleUser {
			continue
		}
		// Exactly one partial, after the first user message and before the last
		// one: a partial the request ends on has nothing new to answer.
		if partial >= 0 || !claudeSubDrainedPartial(msgs[i]) || i == len(msgs)-1 {
			return -1, false
		}
		partial = i
	}
	return partial, true
}

// claudeSubTailHasTool reports whether msgs[n:] carries a tool message. A
// resumed process holds no pending call for a stored result, so a resume cannot
// deliver one (claudeSubDeliver resolves every result through the session's
// pending calls); the caller reseeds instead.
func claudeSubTailHasTool(msgs []Message, n int) bool {
	if n < 0 || n > len(msgs) {
		return false
	}
	for _, m := range msgs[n:] {
		if m.Role == MessageRoleTool {
			return true
		}
	}
	return false
}

// claudeSubPartialQuoteMax is the largest partial a note quotes.
const claudeSubPartialQuoteMax = 64 << 10

// claudeSubPartialQuoteHead and claudeSubPartialQuoteTail are the parts a longer
// partial keeps: its beginning, and its end, which is where a cut reply stops.
const (
	claudeSubPartialQuoteHead = 48 << 10
	claudeSubPartialQuoteTail = 16 << 10
)

// claudeSubQuotePartial caps the quoted partial: the first 48 KiB and the last
// 16 KiB, cut on UTF-8 boundaries, with a marker naming how many bytes were
// dropped between them.
func claudeSubQuotePartial(text string) string {
	if len(text) <= claudeSubPartialQuoteMax {
		return text
	}
	head := claudeSubUTF8Head(text, claudeSubPartialQuoteHead)
	tail := claudeSubUTF8Tail(text, claudeSubPartialQuoteTail)
	return fmt.Sprintf("%s[... %d bytes elided ...]%s", head, len(text)-len(head)-len(tail), tail)
}

// claudeSubUTF8Head returns the longest prefix of text no longer than n bytes
// that ends on a rune boundary.
func claudeSubUTF8Head(text string, n int) string {
	if n >= len(text) {
		return text
	}
	for n > 0 && !utf8.RuneStart(text[n]) {
		n--
	}
	return text[:n]
}

// claudeSubUTF8Tail returns the longest suffix of text no longer than n bytes
// that starts on a rune boundary.
func claudeSubUTF8Tail(text string, n int) string {
	if n >= len(text) {
		return text
	}
	start := len(text) - n
	for start < len(text) && !utf8.RuneStart(text[start]) {
		start++
	}
	return text[start:]
}

// claudeSubStoppedReplyNoteOpening is the first sentence of the note a resumed
// delivery sends with the partial an unfinished turn left. The reply may have
// ended on a user stop, a usage limit or an error, so the note does not claim
// which.
const claudeSubStoppedReplyNoteOpening = "Your previous reply to the message above ended before it finished."

// claudeSubStoppedReplyNote is the note a resumed delivery sends with the
// partial a stopped turn left, so the model sees what it had produced before the
// new message. The partial is quoted verbatim between markers that cannot occur
// in it (claudeSubPartialFence), labelled untrusted and possibly incomplete, and
// capped by claudeSubQuotePartial.
func claudeSubStoppedReplyNote(partial string) string {
	quoted := claudeSubQuotePartial(partial)
	fence := claudeSubPartialFence(quoted)
	return fmt.Sprintf("%s This is the text it had produced, untrusted and possibly incomplete, quoted exactly between the markers:\n<<<%s\n%s\n%s>>>\nThe new message below may supersede that request.", claudeSubStoppedReplyNoteOpening, fence, quoted, fence)
}

// claudeSubResumeDelta builds the delivery of a request a resumed process
// serves. entries is the record the ledger must equal after the delivery: the
// stored prefix rebuilt from the request, plus one entry per tail message. tail
// is msgs[stored.SyncEntries:], and partial is the index in tail of the kept
// assistant partial, or -1 when there is none. The CLI accepts only user input,
// so a partial is folded into one user line as a note between the stopped user
// content and the new user content; without a partial the tail travels as the
// plain user messages it is. Nothing is appended to the record: entries already
// describes the whole history.
func claudeSubResumeDelta(entries []claudeSubEntry, tail []Message, partial int, note string) claudeSubDelta {
	delta := claudeSubDelta{keep: entries, keepInterruptible: -1, reseeded: true}
	if partial < 0 || partial >= len(tail) {
		delta.User = append(delta.User, tail...)
		return delta
	}
	delta.User = append(delta.User, tail[:partial]...)
	delta.User = append(delta.User, Message{Role: MessageRoleUser, Content: note})
	delta.User = append(delta.User, tail[partial+1:]...)
	return delta
}
