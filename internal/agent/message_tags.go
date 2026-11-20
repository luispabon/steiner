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

// compactionTagGap matches the space between a tag's name and its attributes.
// It excludes the newline, so a line such as "<summary\nfoo>" is not a tag.
const compactionTagGap = `[` + compactionTagFlats + `]`

// compactionTagValue is one attribute value: double-quoted, single-quoted, or
// unquoted. None may contain the closing bracket, so a value cannot swallow it.
const compactionTagValue = `(?:"[^"\n]*"|'[^'\n]*'|[^\s"'=<>\x60]+)`

// compactionTagAttr is one attribute: a name, optionally followed by "=" and a
// value. The tail requires at least one gap before the first attribute, so
// "<summary-table>", "<summary.x>", "<summary:x>", "<summary_x>", and
// "<summaryish>" are not tags.
const compactionTagAttr = `[A-Za-z_:][-A-Za-z0-9_:.]*(?:` + compactionTagGap + `*=` + compactionTagGap + `*` + compactionTagValue + `)?`

// compactionTagTail matches what may follow a tag name: no attribute at all
// (just optional space and an optional self-closing slash), or at least one gap
// then attribute tokens, then the closing bracket. Bare attribute names are
// allowed, so prose such as "<summary is short and the request > fine" also
// matches; escaping it is harmless because the words stay between entities.
const compactionTagTail = `(?:` + compactionTagSpace + `*/?>` + `|` + compactionTagGap + `+` + compactionTagAttr + `(?:` + compactionTagGap + `+` + compactionTagAttr + `)*` + compactionTagSpace + `*/?>)`

// summaryTagName and latestUserRequestTagName are the wrapper tag names, and
// compactionTagNames is the alternation a summary body may forge.
const (
	summaryTagName           = "summary"
	latestUserRequestTagName = "latest_user_request"

	compactionTagNames = `(` + summaryTagName + `|` + latestUserRequestTagName + `)`
)

// summaryTagPattern matches the tags the summary projection wraps stored model
// text in. A summary body is model output, so it may close the wrapper and forge
// a second request block; neutralizing these matches keeps it inside its own
// wrapper. It tolerates both tag names, either slash position, a self-closing
// slash, attributes, any case, and the space characters compactionTagSpace
// matches between the bracket, the slash, and the name.
var summaryTagPattern = regexp.MustCompile(`(?i)<` + compactionTagSpace + `*/?` + compactionTagSpace + `*` + compactionTagNames + compactionTagTail)

// requestTagPattern matches only the request wrapper's tag. The request is the
// user's verbatim text, so a <summary> tag it contains is left alone, but it may
// not close its own wrapper.
var requestTagPattern = regexp.MustCompile(`(?i)<` + compactionTagSpace + `*/?` + compactionTagSpace + `*` + latestUserRequestTagName + compactionTagTail)

// escapeCompactionTags replaces the angle brackets of every pattern match with
// HTML entities. Every other byte is untouched, so the projection of text
// without these tags stays identical and cache-stable.
func escapeCompactionTags(text string, pattern *regexp.Regexp) string {
	return pattern.ReplaceAllStringFunc(text, func(tag string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tag, "<", "&lt;"), ">", "&gt;")
	})
}
