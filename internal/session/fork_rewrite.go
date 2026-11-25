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
	parentHistory string
	forkHistory   string
	// copiedRecall holds the recall names whose files the fork copied, so a
	// marker naming a parent file the fork does not have keeps its path.
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

// rewriteContent rewrites one message: the history path in a summary's history
// note lines, and elision markers anywhere in the message.
func (rw forkReferenceRewrite) rewriteContent(msg agent.Message) string {
	content := msg.Content
	if msg.Role == agent.MessageRoleSummary {
		content = rw.rewriteHistoryNote(content)
	}
	if len(rw.copiedRecall) == 0 {
		return content
	}
	return rw.rewriteMarkersInMessage(content)
}

// rewriteHistoryNote replaces the parent history path in history note lines and
// leaves every other line, and every other path in a note, alone.
func (rw forkReferenceRewrite) rewriteHistoryNote(content string) string {
	if rw.parentHistory == rw.forkHistory || !strings.Contains(content, rw.parentHistory) {
		return content
	}
	lines := strings.Split(content, "\n")
	changed := false
	for i, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), agent.HistoryNoteMarker) {
			continue
		}
		if !strings.Contains(line, rw.parentHistory) {
			continue
		}
		lines[i] = strings.ReplaceAll(line, rw.parentHistory, rw.forkHistory)
		changed = true
	}
	if !changed {
		return content
	}
	return strings.Join(lines, "\n")
}

// rewriteMarkersInMessage rewrites inherited elision markers in content. A
// marker inside a sub-agent result envelope changes the envelope's inner
// length, so the envelope is rebuilt through ReplaceSubAgentResultInner to keep
// its bytes attribute and its parseability.
func (rw forkReferenceRewrite) rewriteMarkersInMessage(content string) string {
	if !strings.Contains(content, elisionMarkerPrefix) {
		return content
	}
	spans := prompt.ResultEnvelopeSpans(content)
	if len(spans) == 0 {
		return rw.rewriteMarkers(content)
	}
	var b strings.Builder
	b.Grow(len(content))
	pos := 0
	for _, span := range spans {
		b.WriteString(rw.rewriteMarkers(content[pos:span.Start]))
		b.WriteString(rw.rewriteEnvelope(content[span.Start:span.End]))
		pos = span.End
	}
	b.WriteString(rw.rewriteMarkers(content[pos:]))
	return b.String()
}

// rewriteEnvelope rewrites markers inside one sub-agent result envelope and
// returns raw unchanged when it holds none or cannot be rebuilt.
func (rw forkReferenceRewrite) rewriteEnvelope(raw string) string {
	parsed, ok := agent.ParseSubAgentResultEnvelope(raw)
	if !ok {
		return raw
	}
	inner := rw.rewriteMarkers(parsed.Inner)
	if inner == parsed.Inner {
		return raw
	}
	rewritten, ok := agent.ReplaceSubAgentResultInner(raw, inner)
	if !ok {
		return raw
	}
	return rewritten
}

// rewriteMarkers replaces the read path of every elision marker in text whose
// recall file the fork copied. Text that only looks like a marker is left as is.
func (rw forkReferenceRewrite) rewriteMarkers(text string) string {
	var b strings.Builder
	last, search := 0, 0
	for {
		rel := strings.Index(text[search:], elisionMarkerPrefix)
		if rel < 0 {
			break
		}
		nameStart := search + rel + len(elisionMarkerPrefix)
		name, path, pathStart, markerEnd, ok := parseElisionMarker(text, nameStart)
		if !ok {
			search = nameStart
			continue
		}
		if forkPath, ok := rw.recallReplacement(name, path); ok {
			b.WriteString(text[last:pathStart])
			b.WriteString(forkPath)
			last = pathStart + len(path)
		}
		search = markerEnd
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// parseElisionMarker parses the marker whose recall name starts at nameStart.
// It returns the name, the read path, the path's offset, and the offset just
// past the marker.
func parseElisionMarker(text string, nameStart int) (name, path string, pathStart, end int, ok bool) {
	readRel := strings.Index(text[nameStart:], elisionMarkerRead)
	if readRel < 0 {
		return "", "", 0, 0, false
	}
	name = text[nameStart : nameStart+readRel]
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
	// The copied name is a single path element, so a marker-shaped line with a
	// separator in its name never becomes a fork path.
	if err := safeRecallName(name); err != nil {
		return "", false
	}
	parentPath := filepath.Join(rw.parentRecallDir, name+recallFileExt)
	if path != parentPath {
		return "", false
	}
	return filepath.Join(rw.forkRecallDir, name+recallFileExt), true
}
