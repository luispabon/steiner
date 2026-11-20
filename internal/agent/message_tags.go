package agent

import (
	"regexp"
	"strings"
)

// summaryTagPattern matches the tags the summary projection wraps stored model
// text in. A summary body is model output, so it may close the wrapper and forge
// a second request block; neutralizing these matches keeps it inside its own
// wrapper. Whitespace inside the angle brackets is tolerated so a spaced tag
// cannot slip through.
var summaryTagPattern = regexp.MustCompile(`(?i)<\s*(/?)\s*(summary|latest_user_request)\s*>`)

// requestTagPattern matches only the request wrapper's tag. The request is the
// user's verbatim text, so a <summary> tag it contains is left alone, but it may
// not close its own wrapper.
var requestTagPattern = regexp.MustCompile(`(?i)<\s*(/?)\s*latest_user_request\s*>`)

// escapeCompactionTags replaces the angle brackets of every pattern match with
// HTML entities. Every other byte is untouched, so the projection of text
// without these tags stays identical and cache-stable.
func escapeCompactionTags(text string, pattern *regexp.Regexp) string {
	return pattern.ReplaceAllStringFunc(text, func(tag string) string {
		return strings.ReplaceAll(strings.ReplaceAll(tag, "<", "&lt;"), ">", "&gt;")
	})
}
