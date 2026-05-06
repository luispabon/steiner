package patchdoc

import (
	"fmt"
	"strings"
)

const (
	beginMarker              = "*** Begin Patch"
	endMarker                = "*** End Patch"
	addFileMarkerPrefix      = "*** Add File: "
	deleteFileMarkerPrefix   = "*** Delete File: "
	updateFileMarkerPrefix   = "*** Update File: "
	moveToMarkerPrefix       = "*** Move to: "
	changeContextMarker      = "@@ "
	emptyChangeContextMarker = "@@"
	eofMarker                = "*** End of File"
)

// Parse parses a Codex-style patch document into a Patch AST.
func Parse(input string) (*Patch, error) {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	trimmed := strings.TrimSpace(normalized)
	if trimmed == "" {
		return nil, parseErr{msg: "patch must begin with \"*** Begin Patch\" and end with \"*** End Patch\""}
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return nil, parseErr{msg: "patch must begin with \"*** Begin Patch\" and end with \"*** End Patch\""}
	}
	if lines[0] != beginMarker {
		return nil, parseErr{line: 1, msg: fmt.Sprintf("expected %q", beginMarker)}
	}
	if lines[len(lines)-1] != endMarker {
		return nil, parseErr{line: len(lines), msg: fmt.Sprintf("expected %q", endMarker)}
	}

	p := &parser{lines: lines}
	p.pos = 1

	var hunks []Hunk
	for p.pos < len(lines)-1 {
		if lines[p.pos] == "" {
			return nil, p.errorf("unexpected blank line at top level")
		}
		hunk, err := p.parseHunk()
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
	}

	return &Patch{Hunks: hunks}, nil
}

type parser struct {
	lines []string
	pos   int
}

func (p *parser) parseHunk() (Hunk, error) {
	if p.pos >= len(p.lines) {
		return nil, p.errorf("unexpected end of patch")
	}

	line := p.lines[p.pos]
	switch {
	case strings.HasPrefix(line, addFileMarkerPrefix):
		return p.parseAddFile()
	case strings.HasPrefix(line, deleteFileMarkerPrefix):
		return p.parseDeleteFile()
	case strings.HasPrefix(line, updateFileMarkerPrefix):
		return p.parseUpdateFile()
	default:
		return nil, p.errorf("unexpected top-level marker %q", line)
	}
}

func (p *parser) parseAddFile() (Hunk, error) {
	line := p.lines[p.pos]
	path := strings.TrimPrefix(line, addFileMarkerPrefix)
	start := p.pos + 1
	p.pos++

	var contents strings.Builder
	for p.pos < len(p.lines)-1 {
		line = p.lines[p.pos]
		if isTopLevelMarker(line) {
			break
		}
		if strings.HasPrefix(line, "+") {
			line = line[1:]
		}
		contents.WriteString(line)
		contents.WriteByte('\n')
		p.pos++
	}

	if p.pos == start {
		return AddFile{PathValue: path, Contents: ""}, nil
	}

	return AddFile{PathValue: path, Contents: contents.String()}, nil
}

func (p *parser) parseDeleteFile() (Hunk, error) {
	line := p.lines[p.pos]
	path := strings.TrimPrefix(line, deleteFileMarkerPrefix)
	p.pos++
	return DeleteFile{PathValue: path}, nil
}

func (p *parser) parseUpdateFile() (Hunk, error) {
	line := p.lines[p.pos]
	path := strings.TrimPrefix(line, updateFileMarkerPrefix)
	p.pos++

	movePath := ""
	if p.pos < len(p.lines)-1 && strings.HasPrefix(p.lines[p.pos], moveToMarkerPrefix) {
		movePath = strings.TrimPrefix(p.lines[p.pos], moveToMarkerPrefix)
		p.pos++
	}

	if p.pos >= len(p.lines)-1 || isTopLevelMarker(p.lines[p.pos]) {
		return nil, p.errorf("update file %q has no chunks", path)
	}

	chunks := make([]UpdateFileChunk, 0, 1)
	for p.pos < len(p.lines)-1 {
		if isTopLevelMarker(p.lines[p.pos]) {
			break
		}

		chunk, err := p.parseUpdateChunk()
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
		if chunk.EndOfFile {
			break
		}
	}

	if len(chunks) == 0 {
		return nil, p.errorf("update file %q has no chunks", path)
	}

	return UpdateFile{
		PathValue: path,
		MovePath:  movePath,
		Chunks:    chunks,
	}, nil
}

func (p *parser) parseUpdateChunk() (UpdateFileChunk, error) {
	if p.pos >= len(p.lines)-1 {
		return UpdateFileChunk{}, p.errorf("unexpected end of patch")
	}

	line := p.lines[p.pos]
	chunk := UpdateFileChunk{}
	switch {
	case line == emptyChangeContextMarker:
		chunk.HasContext = true
	case strings.HasPrefix(line, changeContextMarker):
		chunk.HasContext = true
		chunk.ChangeContext = strings.TrimPrefix(line, changeContextMarker)
	default:
		return UpdateFileChunk{}, p.errorf("expected update chunk context marker, got %q", line)
	}
	p.pos++

	bodyLines := 0
	for p.pos < len(p.lines) {
		line = p.lines[p.pos]
		if line == eofMarker {
			if bodyLines == 0 {
				return UpdateFileChunk{}, p.errorf("update chunk has no lines")
			}
			chunk.EndOfFile = true
			p.pos++
			return chunk, nil
		}
		if line == emptyChangeContextMarker || strings.HasPrefix(line, changeContextMarker) {
			if bodyLines == 0 {
				return UpdateFileChunk{}, p.errorf("update chunk has no lines")
			}
			break
		}
		if isTopLevelMarker(line) {
			if bodyLines == 0 {
				return UpdateFileChunk{}, p.errorf("update chunk has no lines")
			}
			break
		}

		switch {
		case line == "":
			chunk.OldLines = append(chunk.OldLines, "")
			chunk.NewLines = append(chunk.NewLines, "")
		case strings.HasPrefix(line, " "):
			text := line[1:]
			chunk.OldLines = append(chunk.OldLines, text)
			chunk.NewLines = append(chunk.NewLines, text)
		case strings.HasPrefix(line, "+"):
			chunk.NewLines = append(chunk.NewLines, line[1:])
		case strings.HasPrefix(line, "-"):
			chunk.OldLines = append(chunk.OldLines, line[1:])
		default:
			return UpdateFileChunk{}, p.errorf("unexpected line in update chunk: %q", line)
		}

		bodyLines++
		p.pos++
	}

	if bodyLines == 0 {
		return UpdateFileChunk{}, p.errorf("update chunk has no lines")
	}

	return chunk, nil
}

func isTopLevelMarker(line string) bool {
	return strings.HasPrefix(line, "***")
}

type parseErr struct {
	line int
	msg  string
}

func (e parseErr) Error() string {
	if e.line > 0 {
		return fmt.Sprintf("line %d: %s", e.line, e.msg)
	}
	return e.msg
}

func (p *parser) errorf(format string, args ...any) error {
	return parseErr{
		line: p.pos + 1,
		msg:  fmt.Sprintf(format, args...),
	}
}
