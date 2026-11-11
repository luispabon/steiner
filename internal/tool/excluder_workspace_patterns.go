package tool

import (
	"errors"
	"runtime"
	"slices"
	"strings"

	"github.com/gobwas/glob"

	"github.com/luispabon/steiner/internal/globexpand"
)

// maxInvalidExcludePatternReports bounds the number of malformed exclude
// patterns reported to callers. The total count stays unbounded.
const maxInvalidExcludePatternReports = 8

// Reasons reported in InvalidExcludePattern.Reason. They are fixed strings that
// never include pattern text, so a report cannot leak the contents of a
// configured exclusion.
const (
	invalidExcludeReasonUnmatchedBrace = "unmatched brace"
	invalidExcludeReasonTooManyForms   = "expands to too many forms"
	invalidExcludeReasonInvalidPattern = "invalid pattern"
)

// InvalidExcludePattern identifies one configured exclude pattern that could
// not be compiled. Index is the zero-based position of the pattern in the
// configuration, and Reason is one of the fixed reasons: "unmatched brace",
// "expands to too many forms", or "invalid pattern".
type InvalidExcludePattern struct {
	Index  int
	Reason string
}

// InvalidExcludePatterns returns a copy of the configured exclude patterns that
// could not be compiled, in configuration order and capped at eight entries,
// plus the total number of malformed patterns. The remaining valid patterns
// apply as usual.
func (e WorkspaceExcluder) InvalidExcludePatterns() ([]InvalidExcludePattern, int) {
	return slices.Clone(e.invalidPatterns), e.invalidPatternCount
}

// InvalidWorkspaceExcludePatterns reports the malformed entries in patterns
// using the same normalisation, expansion, compilation, and reporting limits the
// workspace excluder applies on this platform. It lets callers validate
// configured exclusions without building an excluder.
func InvalidWorkspaceExcludePatterns(patterns []string) ([]InvalidExcludePattern, int) {
	_, _, report := compileWorkspaceExcludePatterns(patterns, runtime.GOOS == "windows")
	return report.entries, report.count
}

// invalidExcludeReport collects malformed exclude patterns. Entries are bounded
// so a large misconfiguration cannot flood a report, while count stays total.
type invalidExcludeReport struct {
	entries []InvalidExcludePattern
	count   int
}

// add records one malformed pattern at index with reason.
func (r *invalidExcludeReport) add(index int, reason string) {
	r.count++
	if len(r.entries) >= maxInvalidExcludePatternReports {
		return
	}
	r.entries = append(r.entries, InvalidExcludePattern{Index: index, Reason: reason})
}

// compileWorkspaceExcludePatterns compiles configured patterns into
// workspace-relative slash patterns and component patterns, and records the
// entries that cannot be compiled. Braces are expanded before compilation so
// gobwas never sees a brace group: an empty group such as "aa{}" compiles but
// its matcher slices past the input. Classification reads the original
// normalised pattern, so a slash-free alternative of a slash pattern stays
// workspace-anchored instead of turning into a component pattern. A pattern is
// applied only when every expanded form compiles, so one malformed alternative
// cannot half-apply an entry.
func compileWorkspaceExcludePatterns(patterns []string, windows bool) ([]workspacePattern, []glob.Glob, invalidExcludeReport) {
	var (
		slashPatterns     []workspacePattern
		componentPatterns []glob.Glob
		report            invalidExcludeReport
	)
	for index, raw := range patterns {
		if raw == "" {
			continue
		}
		pattern := normalizeWorkspacePattern(raw, windows)
		if pattern == "" {
			continue
		}
		slash := strings.Contains(pattern, "/")
		forms, err := globexpand.ExpandBraces(pattern)
		if err != nil {
			report.add(index, invalidExcludeReasonFor(err))
			continue
		}
		entrySlash, entryComponents, reason := compileWorkspaceExcludeForms(forms, slash)
		if reason != "" {
			report.add(index, reason)
			continue
		}
		slashPatterns = append(slashPatterns, entrySlash...)
		componentPatterns = append(componentPatterns, entryComponents...)
	}
	return slashPatterns, componentPatterns, report
}

// compileWorkspaceExcludeForms compiles every expanded form of one exclude
// pattern, returning a fixed reason when any form fails. Each form is normalised
// on its own after expansion, so an empty brace alternative collapses to the
// path it names ("a/{b,}/c" also matches "a/c") and a form left with no
// segments is skipped.
func compileWorkspaceExcludeForms(forms []string, slash bool) ([]workspacePattern, []glob.Glob, string) {
	var (
		slashPatterns     []workspacePattern
		componentPatterns []glob.Glob
	)
	seen := make(map[string]struct{}, len(forms))
	for _, raw := range forms {
		form := normalizeWorkspaceExcludeForm(raw)
		if form == "" {
			continue
		}
		if _, duplicate := seen[form]; duplicate {
			continue
		}
		seen[form] = struct{}{}
		if slash {
			compiled, ok := compileWorkspaceSlashPattern(form)
			if !ok {
				return nil, nil, invalidExcludeReasonInvalidPattern
			}
			slashPatterns = append(slashPatterns, compiled)
			continue
		}
		matcher, err := glob.Compile(form)
		if err != nil {
			return nil, nil, invalidExcludeReasonInvalidPattern
		}
		componentPatterns = append(componentPatterns, matcher)
	}
	return slashPatterns, componentPatterns, ""
}

// normalizeWorkspaceExcludeForm drops empty path segments from one expanded
// brace form. An empty alternative contributes no segment, so the "a//c" form of
// "a/{b,}/c" names "a/c"; a leading separator left by an alternative such as
// "/build" or a trailing one as in "build/" names the same anchored component
// as "build". Only separators of the whole expanded form are considered, so the
// prefix in "x{/a,b}" still binds to its alternative and the form stays "x/a"
// rather than collapsing to "xa". Separators inside a character class or escaped
// by a backslash are left alone.
func normalizeWorkspaceExcludeForm(form string) string {
	segments := make([]string, 0, strings.Count(form, "/")+1)
	start := 0
	for i := 0; i < len(form); i++ {
		switch form[i] {
		case '\\':
			i++
		case '[':
			i = globexpand.SkipClass(form, i)
		case '/':
			if segment := form[start:i]; segment != "" {
				segments = append(segments, segment)
			}
			start = i + 1
		}
	}
	if segment := form[start:]; segment != "" {
		segments = append(segments, segment)
	}
	return strings.Join(segments, "/")
}

// invalidExcludeReasonFor maps an expansion error to its fixed report reason.
func invalidExcludeReasonFor(err error) string {
	switch {
	case errors.Is(err, globexpand.ErrUnmatchedBrace):
		return invalidExcludeReasonUnmatchedBrace
	case errors.Is(err, globexpand.ErrTooComplex):
		return invalidExcludeReasonTooManyForms
	default:
		return invalidExcludeReasonInvalidPattern
	}
}
