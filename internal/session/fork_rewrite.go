package session

import (
	"path/filepath"
	"strings"

	"github.com/luispabon/steiner/internal/agent"
	"github.com/luispabon/steiner/internal/prompt"
)

// The elision marker compaction writes in place of a result body it dropped from
// the active context: the recall file name and its absolute path between fixed
// text. Fork rewriting matches this shape textually rather than through the
// compaction code that writes it.
const (
	elisionMarkerPrefix = "[Tool output omitted from active context: recall "
	elisionMarkerRead   = ", read "
	elisionMarkerSuffix = "; for long lines use bash head -c/tail -c]"
)

// forkReferenceRewrite maps the parent sidecar paths a fork inherited to the
// fork's copies.
type forkReferenceRewrite struct {
	// copyHistory reports that the fork holds its own history archive, so a
	// summary note can name it instead of the parent's.
	copyHistory   bool
	parentHistory string
	forkHistory   string
	// copiedRecall holds the recall names whose files the fork copied, so a
	// marker naming a parent file the fork does not have keeps its path. A
	// summary note naming the recall directory is rewritten only when at least
	// one file was copied, because that is when the fork's directory exists.
	copiedRecall    map[string]bool
	parentRecallDir string
	forkRecallDir   string
}

// rewriteForkReferences rewrites the inherited parent references in the latest
// generation of forked, which is the only generation session.Fork keeps.
func rewriteForkReferences(forked Session, rw forkReferenceRewrite) Session {
	generations := forked.Lineage.Generations
	if len(generations) == 0 {
		return forked
	}
	latest := &generations[len(generations)-1]
	for _, messages := range [][]agent.Message{latest.SummaryPrefix, latest.Messages} {
		for i := range messages {
			messages[i].Content = rw.rewriteContent(messages[i])
		}
	}
	return forked
}

// rewriteContent rewrites one message: the sidecar paths in a summary's history
// note lines, the whole-content elision marker of a tool message, and the
// whole-inner elision marker of a sub-agent result envelope. Every other byte,
// including marker-shaped user text, is left alone.
func (rw forkReferenceRewrite) rewriteContent(msg agent.Message) string {
	content := msg.Content
	if msg.Role == agent.MessageRoleSummary {
		content = rw.rewriteHistoryNote(content)
	}
	if len(rw.copiedRecall) == 0 {
		return content
	}
	switch msg.Role {
	case agent.MessageRoleTool:
		rewritten, ok := rw.rewriteMarkerText(content)
		if !ok {
			return content
		}
		return rewritten
	case agent.MessageRoleUser:
		return rw.rewriteEnvelopeMarkers(content)
	default:
		return content
	}
}

// rewriteHistoryNote replaces the parent history path and recall directory in
// history note lines, and leaves every other line, and every other path in a
// note, alone.
func (rw forkReferenceRewrite) rewriteHistoryNote(content string) string {
	if len(rw.copiedRecall) == 0 && (!rw.copyHistory || rw.parentHistory == rw.forkHistory) {
		return content
	}
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), agent.HistoryNoteMarker) {
			continue
		}
		rewritten := line
		if rw.copyHistory {
			rewritten = strings.ReplaceAll(rewritten, rw.parentHistory, rw.forkHistory)
		}
		if len(rw.copiedRecall) > 0 {
			rewritten = strings.ReplaceAll(rewritten, rw.parentRecallDir, rw.forkRecallDir)
		}
		if rewritten == line {
			continue
		}
		lines[i] = rewritten
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
}

// rewriteMarkerText rewrites content when it is exactly one elision marker, the
// form compaction writes over a whole result body.
func (rw forkReferenceRewrite) rewriteMarkerText(content string) (string, bool) {
	name, path, pathStart, pathEnd, ok := parseWholeMarker(content)
	if !ok {
		return "", false
	}
	forkPath, ok := rw.recallReplacement(name, path)
	if !ok {
		return "", false
	}
	return content[:pathStart] + forkPath + content[pathEnd:], true
}

// rewriteEnvelopeMarkers rewrites the sub-agent result envelope of a user
// message whose inner is exactly one elision marker. A rewritten inner changes
// the envelope's length, so the envelope is rebuilt through
// ReplaceSubAgentResultInner to keep its bytes attribute and parseability. Every
// other byte of the message, including marker-shaped text outside the
// envelopes, is kept.
func (rw forkReferenceRewrite) rewriteEnvelopeMarkers(content string) string {
	spans := prompt.ResultEnvelopeSpans(content)
	if len(spans) == 0 {
		return content
	}
	var b strings.Builder
	b.Grow(len(content))
	pos := 0
	changed := false
	for _, span := range spans {
		b.WriteString(content[pos:span.Start])
		raw := content[span.Start:span.End]
		if rewritten, ok := rw.rewriteEnvelope(raw); ok {
			raw = rewritten
			changed = true
		}
		b.WriteString(raw)
		pos = span.End
	}
	b.WriteString(content[pos:])
	if !changed {
		return content
	}
	return b.String()
}

// rewriteEnvelope rewrites one sub-agent result envelope whose inner is exactly
// one elision marker and reports whether it changed.
func (rw forkReferenceRewrite) rewriteEnvelope(raw string) (string, bool) {
	parsed, ok := agent.ParseSubAgentResultEnvelope(raw)
	if !ok {
		return "", false
	}
	inner, ok := rw.rewriteMarkerText(parsed.Inner)
	if !ok {
		return "", false
	}
	rewritten, ok := agent.ReplaceSubAgentResultInner(raw, inner)
	if !ok {
		return "", false
	}
	return rewritten, true
}

// parseWholeMarker parses text that is exactly one elision marker: the prefix
// at offset 0 and the suffix at the end of text. A marker-shaped fragment, such
// as a bare marker prefix followed by other text, does not parse.
func parseWholeMarker(text string) (name, path string, pathStart, pathEnd int, ok bool) {
	if !strings.HasPrefix(text, elisionMarkerPrefix) {
		return "", "", 0, 0, false
	}
	name, path, pathStart, end, ok := parseElisionMarker(text, len(elisionMarkerPrefix))
	if !ok || end != len(text) {
		return "", "", 0, 0, false
	}
	return name, path, pathStart, pathStart + len(path), true
}

// parseElisionMarker parses the marker whose recall name starts at nameStart.
// It returns the name, the read path, the path's offset, and the offset just
// past the marker. The name must be one path element, as agent.RecallName
// produces; rejecting anything else stops a bare marker prefix earlier in the
// text from pairing with a later marker's delimiters and hiding that marker.
func parseElisionMarker(text string, nameStart int) (name, path string, pathStart, end int, ok bool) {
	readRel := strings.Index(text[nameStart:], elisionMarkerRead)
	if readRel < 0 {
		return "", "", 0, 0, false
	}
	name = text[nameStart : nameStart+readRel]
	// A real recall name never contains a newline or a nested marker prefix.
	// Rejecting both stops a bare marker prefix earlier in the text from
	// pairing with a later marker's delimiters and hiding that marker.
	if safeRecallName(name) != nil || strings.Contains(name, elisionMarkerPrefix) || strings.ContainsAny(name, "\r\n") {
		return "", "", 0, 0, false
	}
	pathStart = nameStart + readRel + len(elisionMarkerRead)
	suffixRel := strings.Index(text[pathStart:], elisionMarkerSuffix)
	if suffixRel < 0 {
		return "", "", 0, 0, false
	}
	return name, text[pathStart : pathStart+suffixRel], pathStart, pathStart + suffixRel + len(elisionMarkerSuffix), true
}

// recallReplacement returns the fork path for a marker that names the parent's
// recall file for name, and false for any other name, path, or uncopied file.
func (rw forkReferenceRewrite) recallReplacement(name, path string) (string, bool) {
	if !rw.copiedRecall[name] {
		return "", false
	}
	parentPath := filepath.Join(rw.parentRecallDir, name+recallFileExt)
	if path != parentPath {
		return "", false
	}
	return filepath.Join(rw.forkRecallDir, name+recallFileExt), true
}
