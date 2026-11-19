package builtin

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hbollon/go-edlib"
)

const (
	// maxSuggestedKeyRunes bounds how much of an unknown field name is shown
	// and matched in suggestions.
	maxSuggestedKeyRunes = 64
	// maxReportedUnknownFields bounds how many unknown fields one error reports.
	maxReportedUnknownFields = 3
	// maxReportedValidFields bounds the valid field list in one error.
	maxReportedValidFields = 12
)

// fieldNameSuggestion is a valid JSON field name split into normalized tokens.
type fieldNameSuggestion struct {
	name       string
	normalized string
	tokens     []string
}

// unknownFieldError reports unknown JSON fields in a strict decode. Each of the
// first reported fields gets an advisory suggestion when exactly one valid
// field is a unique close match.
func unknownFieldError(unknown []string, raw map[string]any, known map[string]int) error {
	sorted := append([]string(nil), unknown...)
	sort.Strings(sorted)

	suggestions := suggestUnknownFields(sorted, raw, known)

	reported := sorted
	omitted := 0
	if len(reported) > maxReportedUnknownFields {
		omitted = len(reported) - maxReportedUnknownFields
		reported = reported[:maxReportedUnknownFields]
	}

	parts := make([]string, 0, len(reported)+1)
	for _, key := range reported {
		part := "unknown field " + quoteFieldName(key)
		if suggestion, ok := suggestions[key]; ok {
			part += " (did you mean " + quoteFieldName(suggestion) + "?)"
		}
		parts = append(parts, part)
	}
	if omitted == 1 {
		parts = append(parts, "1 more unknown field")
	} else if omitted > 1 {
		parts = append(parts, fmt.Sprintf("%d more unknown fields", omitted))
	}

	return fmt.Errorf("decode input: %s; valid fields: %s", strings.Join(parts, "; "), validFieldList(known))
}

// suggestUnknownFields returns one unique suggestion per unknown key. Keys that
// share a suggestion suppress each other so the error never proposes the same
// target for multiple invalid inputs.
func suggestUnknownFields(unknown []string, raw map[string]any, known map[string]int) map[string]string {
	candidates := make([]fieldNameSuggestion, 0, len(known))
	for name := range known {
		if _, present := raw[name]; present {
			continue
		}
		tokens := splitFieldName(name)
		candidates = append(candidates, fieldNameSuggestion{
			name:       name,
			normalized: strings.Join(tokens, ""),
			tokens:     tokens,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].name < candidates[j].name })

	suggestions := make(map[string]string, len(unknown))
	suggestedBy := make(map[string][]string, len(unknown))
	for _, key := range unknown {
		if utf8.RuneCountInString(key) > maxSuggestedKeyRunes {
			continue
		}
		tokens := splitFieldName(key)
		target, ok := bestFieldSuggestion(fieldNameSuggestion{
			name:       key,
			normalized: strings.Join(tokens, ""),
			tokens:     tokens,
		}, candidates)
		if !ok {
			continue
		}
		suggestions[key] = target
		suggestedBy[target] = append(suggestedBy[target], key)
	}

	for _, keys := range suggestedBy {
		if len(keys) < 2 {
			continue
		}
		for _, key := range keys {
			delete(suggestions, key)
		}
	}
	return suggestions
}

// bestFieldSuggestion applies the first nonempty match tier: normalized
// equality, contiguous whole-token containment, then OSA edit distance. A tie
// within a tier yields no suggestion rather than falling through.
func bestFieldSuggestion(key fieldNameSuggestion, candidates []fieldNameSuggestion) (string, bool) {
	var matches []string

	for _, candidate := range candidates {
		if candidate.normalized == key.normalized {
			matches = append(matches, candidate.name)
		}
	}
	if len(matches) > 0 {
		return singleSuggestion(matches)
	}

	for _, candidate := range candidates {
		if tokenSequenceContains(candidate.tokens, key.tokens) || tokenSequenceContains(key.tokens, candidate.tokens) {
			matches = append(matches, candidate.name)
		}
	}
	if len(matches) > 0 {
		return singleSuggestion(matches)
	}

	bestDistance := -1
	for _, candidate := range candidates {
		distance, ok := osaSuggestionDistance(key.normalized, candidate.normalized)
		if !ok {
			continue
		}
		switch {
		case bestDistance < 0 || distance < bestDistance:
			bestDistance = distance
			matches = matches[:0]
			matches = append(matches, candidate.name)
		case distance == bestDistance:
			matches = append(matches, candidate.name)
		}
	}
	return singleSuggestion(matches)
}

func singleSuggestion(matches []string) (string, bool) {
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}

// splitFieldName splits camelCase, separators, and punctuation into lowercase
// tokens. "head_limit", "HeadLimit", and "head-limit" all yield [head, limit].
func splitFieldName(name string) []string {
	runes := []rune(name)
	tokens := make([]string, 0, 4)
	current := make([]rune, 0, len(runes))
	flush := func() {
		if len(current) == 0 {
			return
		}
		tokens = append(tokens, string(current))
		current = current[:0]
	}

	for i, r := range runes {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush()
			continue
		}
		if len(current) > 0 {
			prev := current[len(current)-1]
			if unicode.IsUpper(r) {
				switch {
				case unicode.IsLower(prev) || unicode.IsDigit(prev):
					flush()
				case unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]):
					flush()
				}
			}
		}
		current = append(current, unicode.ToLower(r))
	}
	flush()
	return tokens
}

// tokenSequenceContains reports whether inner appears contiguously in outer and
// includes an anchor token of at least three runes.
func tokenSequenceContains(outer, inner []string) bool {
	if len(inner) == 0 || len(inner) > len(outer) {
		return false
	}

	anchored := false
	for _, token := range inner {
		if utf8.RuneCountInString(token) >= 3 {
			anchored = true
			break
		}
	}
	if !anchored {
		return false
	}

	for start := 0; start+len(inner) <= len(outer); start++ {
		matched := true
		for offset := range inner {
			if outer[start+offset] != inner[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// osaSuggestionDistance reports an OSA edit distance when the normalized names
// are close enough to suggest: distance <=1 for 4-7 runes and <=2 for 8 or
// more. Names shorter than four runes never match.
func osaSuggestionDistance(a, b string) (int, bool) {
	aLen := utf8.RuneCountInString(a)
	bLen := utf8.RuneCountInString(b)
	shorter := min(aLen, bLen)
	if shorter < 4 {
		return 0, false
	}

	maxDistance := 1
	if shorter >= 8 {
		maxDistance = 2
	}

	lengthDifference := aLen - bLen
	if lengthDifference < 0 {
		lengthDifference = -lengthDifference
	}
	if lengthDifference > maxDistance {
		return 0, false
	}

	distance := edlib.OSADamerauLevenshteinDistance(a, b)
	if distance > maxDistance {
		return 0, false
	}
	return distance, true
}

// quoteFieldName escapes a field name and bounds its displayed length.
func quoteFieldName(name string) string {
	if utf8.RuneCountInString(name) <= maxSuggestedKeyRunes {
		return strconv.Quote(name)
	}
	runes := []rune(name)
	return strconv.Quote(string(runes[:maxSuggestedKeyRunes-3]) + "...")
}

// validFieldList returns a sorted, bounded list of JSON field names.
func validFieldList(known map[string]int) string {
	fields := make([]string, 0, len(known))
	for name := range known {
		fields = append(fields, name)
	}
	sort.Strings(fields)

	omitted := 0
	if len(fields) > maxReportedValidFields {
		omitted = len(fields) - maxReportedValidFields
		fields = fields[:maxReportedValidFields]
	}

	joined := strings.Join(fields, ", ")
	if omitted > 0 {
		return fmt.Sprintf("%s (+%d more)", joined, omitted)
	}
	return joined
}
