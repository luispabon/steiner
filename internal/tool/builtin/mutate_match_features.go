package builtin

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/luispabon/steiner/internal/tool"
)

// Caps on match-feature computation. Every feature is derived from bounded
// scans so a pathological old_string or file cannot make a failed mutate
// expensive (issue #673).
const (
	// maxFeatureFileBytes bounds the file size a full line scan runs over.
	// Beyond it only the cheap scalar features are computed.
	maxFeatureFileBytes = 4 << 20
	// maxFeatureOldLines bounds how many old_string lines take part in the
	// line-scanning features.
	maxFeatureOldLines = 400
	// maxPositionsPerLine bounds how many file positions are tracked for one
	// distinct line during the longest-run and prefix scans.
	maxPositionsPerLine = 64
	// maxMatchSampleBytes bounds each raw sample field under capture_bodies.
	maxMatchSampleBytes = 4096
)

// matchFailureError wraps a failed replace's original error with the derived
// feature set and, under capture_bodies, a bounded raw sample. Error and
// Unwrap preserve the original, so model-facing output and
// classifyMutateError are unaffected.
type matchFailureError struct {
	err    error
	match  *tool.MatchFailure
	sample *tool.MatchSample
}

func (e *matchFailureError) Error() string { return e.err.Error() }

func (e *matchFailureError) Unwrap() error { return e.err }

// matchFeatureInput is the bounded input to computeMatchFailure: the attempted
// old_string, the current planned content, the pre-call content, whether the
// file hash was supplied, and the agent's read state for the path.
type matchFeatureInput struct {
	old              string
	content          []byte
	original         []byte
	touched          bool
	fileHashSupplied bool
	read             tool.FileReadState
}

// matchFailure wraps err with the derived feature set for an eligible failed
// replace and, at capture Bodies within the per-call sample cap, a bounded raw
// sample. At capture Off it returns err unchanged, so nothing is computed when
// the tool stream is disabled.
func (p *mutatePlanner) matchFailure(err error, state *mutateFileState, op MutateOperation) error {
	if p.env.Capture == tool.DiagnosticsCaptureOff {
		return err
	}
	read := tool.FileReadState{}
	if p.env.FileReadLookup != nil {
		read = p.env.FileReadLookup(state.path)
	}
	features := computeMatchFailure(matchFeatureInput{
		old:              op.OldString,
		content:          state.content,
		original:         state.original,
		touched:          state.touched,
		fileHashSupplied: op.FileHash != "",
		read:             read,
	})
	wrapped := &matchFailureError{err: err, match: &features}
	if p.env.Capture == tool.DiagnosticsCaptureBodies && p.samplesUsed < maxDetailedMutateFailures {
		wrapped.sample = buildMatchSample(state.displayPath, op.OldString, state.content, features.LocusLine)
		p.samplesUsed++
	}
	return wrapped
}

// computeMatchFailure derives the scalar feature set recorded for a failed
// mutate replace. It never returns file content: every field is an int, bool
// or short enum, so the result can be held on MutateResult without escaping
// file text to the agent.
func computeMatchFailure(in matchFeatureInput) tool.MatchFailure {
	allOldLines := splitFeatureLines(in.old)
	f := tool.MatchFailure{
		OldBytes:         len(in.old),
		OldLines:         len(allOldLines),
		NonBlankLines:    countNonBlankLines(allOldLines),
		MatchCount:       bytes.Count(in.content, []byte(in.old)),
		CRLFMismatch:     bytes.Contains(in.content, []byte("\r\n")) != strings.Contains(in.old, "\r\n"),
		FileHashSupplied: in.fileHashSupplied,
	}
	f.ReadState, f.TurnsSinceRead = readStateFeatures(in.read)

	if len(in.content) > maxFeatureFileBytes {
		// Too large to scan line-wise: cheap scalars only. The prefix fields
		// report -1, every other line-scanning feature stays at its zero value.
		f.Truncated = true
		f.ExactPrefixLines = -1
		f.TrimPrefixLines = -1
		return f
	}

	oldLines := allOldLines
	if len(oldLines) > maxFeatureOldLines {
		oldLines = oldLines[:maxFeatureOldLines]
		f.Truncated = true
	}
	fileLines := splitFeatureLines(string(in.content))
	f.FileLines = len(fileLines)

	f.LinesFound = linesFoundCount(oldLines, fileLines)

	fileNB, fileNBLines := nonBlankTrimmedLines(fileLines)
	oldNB, _ := nonBlankTrimmedLines(oldLines)
	run, startNB, runTruncated := longestTrimmedRun(oldNB, fileNB)
	f.LongestRun = run
	if runTruncated {
		f.Truncated = true
	}

	exact, exactTruncated := prefixMatchLines(oldLines, fileLines, false)
	f.ExactPrefixLines = exact
	if exactTruncated {
		f.Truncated = true
	}
	trimmed, trimTruncated := prefixMatchLines(oldLines, fileLines, true)
	f.TrimPrefixLines = trimmed
	if trimTruncated {
		f.Truncated = true
	}

	matchedRegion, matchedLine, haveRegion := extractNormalizedMatch(in.content, in.old)
	f.WhitespaceKind, f.IndentDeltaMax = whitespaceFeatures(in.content, in.old, matchedRegion, haveRegion)

	if oldStyle, oldOK := indentStyle(oldLines); oldOK {
		if fileStyle, fileOK := indentStyle(fileLines); fileOK && fileStyle != oldStyle {
			f.TabsVsSpaces = true
		}
	}

	f.UnescapeMatches = unescapeMatches(in.content, in.old)
	f.LinePrefix = linePrefixDominant(oldLines)

	f.MatchesOriginal = in.touched && bytes.Contains(in.original, []byte(in.old)) && !bytes.Contains(in.content, []byte(in.old))

	locus := locusLine(in, f.MatchCount, haveRegion, matchedLine, run, startNB, fileNBLines)
	f.LocusLine = locus
	f.InReadRange = inReadRange(locus, in.read)

	return f
}

// linesFoundCount counts how many non-blank old lines appear (after trimming)
// among the file's trimmed lines.
func linesFoundCount(oldLines, fileLines []string) int {
	fileTrimmed := make(map[string]struct{}, len(fileLines))
	for _, l := range fileLines {
		fileTrimmed[strings.TrimSpace(l)] = struct{}{}
	}
	found := 0
	for _, l := range oldLines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" {
			continue
		}
		if _, ok := fileTrimmed[trimmed]; ok {
			found++
		}
	}
	return found
}

// whitespaceFeatures derives the bounded whitespace kind and, for the indent
// kinds, the largest per-line indent delta against the matched region.
func whitespaceFeatures(content []byte, old, matchedRegion string, haveRegion bool) (kind string, indentDelta int) {
	kind = whitespaceKind(content, old, matchedRegion, haveRegion)
	if kind == "indent_uniform" || kind == "indent_nonuniform" {
		indentDelta = indentDeltaMax(old, matchedRegion)
	}
	return kind, indentDelta
}

// linePrefixDominant reports whether at least half of the non-blank old lines
// carry a line-number prefix.
func linePrefixDominant(oldLines []string) bool {
	nonBlank, prefixed := linePrefixCounts(oldLines)
	return prefixed > 0 && prefixed*2 >= nonBlank
}

// locusLine picks the 1-based locus line for a failed replace: the first exact
// occurrence wins when the text is present at all (ambiguous_match, or
// stale_read whose edit would have matched); otherwise it falls back to the
// normalized window, the longest-run alignment, then the nearest diagnostic
// anchor.
func locusLine(in matchFeatureInput, matchCount int, haveRegion bool, matchedLine, run, startNB int, fileNBLines []int) int {
	switch {
	case matchCount > 0:
		return lineNumberAt(in.content, bytes.Index(in.content, []byte(in.old)))
	case haveRegion:
		return matchedLine
	case run > 0 && startNB >= 0 && startNB < len(fileNBLines):
		return fileNBLines[startNB]
	default:
		if _, _, lineNum, _, ok := findDiagnosticAnchor(in.content, in.old); ok {
			return lineNum
		}
	}
	return 0
}

// buildMatchSample builds the bounded raw sample recorded for an eligible
// failed replace under capture_bodies: the attempted old_string and a small
// file region, both truncated to maxMatchSampleBytes. Only the last read range
// is tracked, so the region is evidence, not proof of what the model saw.
func buildMatchSample(displayPath, old string, content []byte, locus int) *tool.MatchSample {
	sample := &tool.MatchSample{Path: displayPath}

	truncOld := truncateDiagnosticText(old, maxMatchSampleBytes)
	sample.OldString = truncOld
	sample.OldTruncated = truncOld != old

	region := ""
	startLine := 0
	if matched, lineNum, ok := extractNormalizedMatch(content, old); ok {
		region = matched
		startLine = lineNum
	} else if locus > 0 {
		fileLines := splitFeatureLines(string(content))
		start := locus - 6
		if start < 1 {
			start = 1
		}
		end := locus + 5
		if end > len(fileLines) {
			end = len(fileLines)
		}
		if start <= end {
			region = strings.Join(fileLines[start-1:end], "\n")
			startLine = start
		}
	}
	truncRegion := truncateDiagnosticText(region, maxMatchSampleBytes)
	sample.Region = truncRegion
	sample.RegionTruncated = truncRegion != region
	sample.RegionStartLine = startLine
	return sample
}

// splitFeatureLines normalises CRLF to LF, splits on LF, and drops one
// trailing empty element so a text ending in a newline does not count an
// extra blank line.
func splitFeatureLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func countNonBlankLines(lines []string) int {
	n := 0
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// nonBlankTrimmedLines returns the trimmed non-blank lines and their 1-based
// line numbers, in order.
func nonBlankTrimmedLines(lines []string) ([]string, []int) {
	var trimmed []string
	var lineNos []int
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		trimmed = append(trimmed, t)
		lineNos = append(lineNos, i+1)
	}
	return trimmed, lineNos
}

// longestTrimmedRun returns the longest run of consecutive trimmed non-blank
// old lines that appears consecutively among the trimmed non-blank file lines,
// the file index where that run starts, and whether a position cap was hit.
func longestTrimmedRun(oldNB, fileNB []string) (best, bestStart int, truncated bool) {
	positions := make(map[string][]int, len(fileNB))
	for j, fl := range fileNB {
		pos := positions[fl]
		if len(pos) < maxPositionsPerLine {
			positions[fl] = append(pos, j)
		} else {
			truncated = true
		}
	}
	bestStart = -1
	prev := make(map[int]int)
	for _, ol := range oldNB {
		cur := make(map[int]int)
		for _, j := range positions[ol] {
			run := 1
			if v, ok := prev[j-1]; ok {
				run = v + 1
			}
			cur[j] = run
			if run > best {
				best = run
				bestStart = j - run + 1
			}
		}
		prev = cur
	}
	return best, bestStart, truncated
}

// prefixMatchLines returns the longest prefix of oldLines found at any file
// line, comparing exactly or after trimming, and whether the start cap was hit.
// It returns -1 when the first old line is not found at all.
func prefixMatchLines(oldLines, fileLines []string, trim bool) (best int, truncated bool) {
	if len(oldLines) == 0 {
		return -1, false
	}
	eq := func(a, b string) bool { return a == b }
	if trim {
		eq = func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) }
	}
	best = -1
	starts := 0
	for s := 0; s < len(fileLines); s++ {
		if !eq(oldLines[0], fileLines[s]) {
			continue
		}
		if starts >= maxPositionsPerLine {
			truncated = true
			break
		}
		starts++
		j := 0
		for j < len(oldLines) && s+j < len(fileLines) && eq(oldLines[j], fileLines[s+j]) {
			j++
		}
		if j > best {
			best = j
		}
	}
	return best, truncated
}

// whitespaceKindByClass maps classifyWhitespaceMismatch's kinds to the bounded
// enum recorded in ws_kind.
var whitespaceKindByClass = map[string]string{
	"blank-line-count":               "blank_lines",
	"internal-whitespace":            "internal_spacing",
	"leading-indentation-uniform":    "indent_uniform",
	"leading-indentation-nonuniform": "indent_nonuniform",
	"":                               "trailing",
}

func whitespaceKind(content []byte, old, matchedRegion string, haveRegion bool) string {
	if haveRegion {
		class, _ := classifyWhitespaceMismatch(old, matchedRegion)
		return whitespaceKindByClass[class]
	}
	if normalizedWhitespaceMatchExists(content, old) {
		return "collapsed_only"
	}
	return "none"
}

func indentDeltaMax(old, region string) int {
	oldNB := nonBlankLinesOf(old)
	regionNB := nonBlankLinesOf(region)
	best := 0
	for i := range oldNB {
		if i >= len(regionNB) {
			break
		}
		d := leadingWhitespaceLength(regionNB[i]) - leadingWhitespaceLength(oldNB[i])
		if d < 0 {
			d = -d
		}
		if d > best {
			best = d
		}
	}
	return best
}

func nonBlankLinesOf(text string) []string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// indentStyle reports the dominant indent character among lines whose first
// byte is a space or a tab. It reports false when neither style appears or the
// counts tie.
func indentStyle(lines []string) (string, bool) {
	tabs, spaces := 0, 0
	for _, l := range lines {
		if l == "" {
			continue
		}
		switch l[0] {
		case '\t':
			tabs++
		case ' ':
			spaces++
		}
	}
	switch {
	case tabs == 0 && spaces == 0:
		return "", false
	case tabs > spaces:
		return "tab", true
	case spaces > tabs:
		return "space", true
	default:
		return "", false
	}
}

var unescapeReplacer = strings.NewReplacer(
	`\n`, "\n",
	`\t`, "\t",
	`\"`, `"`,
	`\\`, `\`,
)

func unescapeMatches(content []byte, old string) bool {
	if !strings.Contains(old, `\n`) && !strings.Contains(old, `\t`) {
		return false
	}
	unescaped := unescapeReplacer.Replace(old)
	return unescaped != "" && bytes.Contains(content, []byte(unescaped))
}

var linePrefixPattern = regexp.MustCompile(`^\s*\d+(:|\t|\||│|→)`)

func linePrefixCounts(oldLines []string) (nonBlank, prefixed int) {
	for _, l := range oldLines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		nonBlank++
		if linePrefixPattern.MatchString(l) {
			prefixed++
		}
	}
	return nonBlank, prefixed
}

func readStateFeatures(read tool.FileReadState) (state string, turnsSinceRead int) {
	switch {
	case !read.Known:
		return "unknown", -1
	case !read.Observed:
		if read.Pruned {
			return "pruned", -1
		}
		return "never_read", -1
	case read.MutatedSinceRead:
		return "self_mutated", read.TurnsSinceRead
	case read.ChangedSinceRead:
		return "external_change", read.TurnsSinceRead
	default:
		return "unchanged", read.TurnsSinceRead
	}
}

func inReadRange(locus int, read tool.FileReadState) string {
	if locus == 0 || !read.Known || !read.Observed {
		return "unknown"
	}
	if read.StartLine <= locus && locus <= read.EndLine {
		return "yes"
	}
	return "no"
}
