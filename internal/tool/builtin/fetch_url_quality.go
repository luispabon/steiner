package builtin

import (
	"regexp"
	"strings"
	"unicode"
)

// navigationAdvisoryMessage is appended to a result's Message when
// looksLikeNavigation flags the returned markdown as nav-like rather than
// article prose. Advisory only: fetch_url never rewrites or re-fetches a
// different URL.
const navigationAdvisoryMessage = "This response looks like site navigation rather than article text; a .md, raw, or print variant of the URL may return the article content."

// navLinkDensityThreshold is the fraction of non-whitespace characters that
// must sit inside markdown link constructs ([text](url)) before content is
// considered link-dense enough to flag as navigation.
const navLinkDensityThreshold = 0.5

// navMaxProseLines is the maximum number of non-link prose lines allowed
// before content is no longer considered nav-like, even with high link
// density — a page can have many links and still be an article.
const navMaxProseLines = 5

// navMinNonWhitespaceRunes is the minimum amount of non-whitespace content
// required before the navigation heuristic runs at all; very short content
// is not classified either way.
const navMinNonWhitespaceRunes = 100

// navProseLineMinRunes is the minimum number of non-whitespace, non-link
// runes a line must retain before it counts as a "prose line".
const navProseLineMinRunes = 20

var markdownLinkPattern = regexp.MustCompile(`\[[^\]\n]*\]\([^)\n]*\)`)

// looksLikeNavigation reports whether markdown looks like a wall of site
// navigation links rather than article prose. The heuristic is deliberately
// cheap and dependency-free: it measures the fraction of non-whitespace
// characters that sit inside markdown link constructs, and combines that
// with a count of lines that still contain meaningful non-link text.
func looksLikeNavigation(markdown string) bool {
	nonWhitespace := countNonWhitespace(markdown)
	if nonWhitespace < navMinNonWhitespaceRunes {
		return false
	}

	linkChars := 0
	for _, match := range markdownLinkPattern.FindAllString(markdown, -1) {
		linkChars += countNonWhitespace(match)
	}

	if float64(linkChars)/float64(nonWhitespace) <= navLinkDensityThreshold {
		return false
	}

	return countProseLines(markdown) <= navMaxProseLines
}

func countNonWhitespace(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

// countProseLines counts lines that retain meaningful non-link text after
// markdown link constructs are stripped out.
func countProseLines(markdown string) int {
	count := 0
	for _, line := range strings.Split(markdown, "\n") {
		stripped := markdownLinkPattern.ReplaceAllString(line, "")
		stripped = strings.Trim(strings.TrimSpace(stripped), "-*>#|")
		if countNonWhitespace(stripped) >= navProseLineMinRunes {
			count++
		}
	}
	return count
}
