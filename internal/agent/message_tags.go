package agent

import (
	"regexp"
	"strings"
)

// compactionTagFlats are the characters that may sit inside a wrapper tag's
// angle brackets besides the newline: ASCII tab, form feed, carriage return,
// vertical tab and space, the Unicode separator characters \p{Z} (no-break
// space, the line and paragraph separators, and the other spaces), the format
// characters \p{Cf} (soft hyphen, zero-width spaces and joiners, word joiner,
// and byte order mark), and NEL (U+0085), which is in neither class.
const compactionTagFlats = `\t\f\r \v\p{Z}\p{Cf}\x{0085}`

// compactionTagSpace matches those characters plus the newline, since a tag may
// wrap inside its angle brackets.
const compactionTagSpace = `[` + compactionTagFlats + `\n]`

// compactionTagTail is the tail of an opening tag, whose name has no slash
// before it: either only the space characters compactionTagSpace lists, which
// already include the newline, or a run that starts with a character that
// cannot continue a tag name and is neither a newline nor the closing bracket,
// then any characters up to the closing bracket. An opening tag ends on its
// first line, so "<summary" followed by a newline and more text is not a tag and
// "<summary\nfoo>" stays untouched.
//
// Both tails take that second form for attributes, a stray slash, a bare "=", a
// quote, and prose such as "<summary is short and the request > fine"; escaping
// those is harmless because the words stay between the entities. A name
// continued by a name character is not a tag, so "<summary-table>",
// "<summary.x>", "<summary:x>", "<summary_x>", and "<summaryish>" stay
// untouched.
const compactionTagTail = `(?:` + compactionTagSpace + `*|[^A-Za-z0-9_.:\-\n>][^>\n]*)?>`

// compactionTagEndTail is the tail of a closing tag, whose name follows a slash:
// the same two forms, but the run that starts with a character which cannot
// continue a tag name may cross newlines before the closing bracket, because an
// HTML tokenizer reads that run as the end tag's attributes. So "</summary"
// followed by a newline and text, by "\r\n" and text, by another slash, or by an
// attribute whose quoted value spans lines closes the wrapper and is escaped.
const compactionTagEndTail = `(?:` + compactionTagSpace + `*|[^A-Za-z0-9_.:\->][^>]*)?>`

// summaryTagName and latestUserRequestTagName are the wrapper tag names, and
// compactionTagNames is the alternation a summary body may forge.
const (
	summaryTagName           = "summary"
	latestUserRequestTagName = "latest_user_request"

	compactionTagNames = `(` + summaryTagName + `|` + latestUserRequestTagName + `)`
)

// compactionTagPattern matches wrapper tags whose names equal names: a closing
// tag, where a slash precedes the name, with compactionTagEndTail, or an opening
// tag with compactionTagTail. The slash may sit before or after the space
// characters that follow the bracket.
func compactionTagPattern(names string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)<` + compactionTagSpace + `*(?:/` + compactionTagSpace + `*` + names + compactionTagEndTail + `|` + names + compactionTagTail + `)`)
}

// summaryTagPattern matches the tags the summary projection wraps stored model
// text in. A summary body is model output, so it may close the wrapper and forge
// a second request block; neutralizing these matches keeps it inside its own
// wrapper. It tolerates both tag names, either slash position, a self-closing
// slash, attributes, any case, and the space characters compactionTagSpace
// matches between the bracket, the slash, and the name. A closing tag may span
// lines; an opening tag ends on its first line.
var summaryTagPattern = compactionTagPattern(compactionTagNames)

// requestTagPattern matches only the request wrapper's tag. The request is the
// user's verbatim text, so a <summary> tag it contains is left alone, but it may
// not close its own wrapper. A closing tag may span lines; an opening tag ends
// on its first line.
var requestTagPattern = compactionTagPattern(latestUserRequestTagName)

// truncatedSummaryTagName is the tag a condense retry quotes a truncated draft
// in. It is not a wrapper tag, so it stays out of compactionTagNames: only the
// condense instruction wraps model text in it, and escaping matching tags keeps
// the draft from closing that wrapper early.
const truncatedSummaryTagName = "truncated_summary"

// truncatedSummaryTagPattern matches that quote wrapper's tags, so a draft can
// not close the wrapper it is quoted in.
var truncatedSummaryTagPattern = compactionTagPattern(truncatedSummaryTagName)

// escapeCompactionTags replaces the angle brackets of every pattern match with
// HTML entities. Every other byte is untouched, so the projection of text
// without these tags stays identical and cache-stable.
func escapeCompactionTags(text string, pattern *regexp.Regexp) string {
	return pattern.ReplaceAllStringFunc(text, func(tag string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tag, "<", "&lt;"), ">", "&gt;")
	})
}
