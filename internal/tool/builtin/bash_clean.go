package builtin

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

const (
	// bashCleanStartWindow is how far into a chunk cleanBashText looks for a
	// newline when aligning a chunk that begins mid-stream.
	bashCleanStartWindow = 4096
	// bashCleanLabelMax caps how many raw bytes of a malformed escape are shown.
	bashCleanLabelMax = 8
	// bashCleanMaxExpansion bounds output length relative to input length:
	// the worst case is a lone ESC byte rendered as "[malformed escape: \x1b]"
	// (24 bytes), so output never exceeds 24*len(input) bytes.
	bashCleanMaxExpansion = 24
)

const (
	asciiBEL = 0x07
	asciiESC = 0x1b
)

// cleanBashText turns raw terminal output into readable text. CRLF becomes
// LF; standalone CR, other C0 controls and UTF-8 encoded C1 characters pass
// through. Valid ANSI sequences and OSC sequences other than OSC 8 are
// stripped. OSC 8 hyperlinks become "text (uri)", whatever the terminator
// (BEL or ST). Malformed or incomplete escapes are replaced with
// "[malformed escape: \x1b...]".
//
// cutStart means the chunk begins mid-stream: leading bytes are dropped up to
// and including the first newline within the first 4 KiB, otherwise up to the
// next UTF-8 boundary, and the number of dropped bytes is returned as skipped.
// cutEnd means the chunk was truncated by capture: a trailing incomplete
// escape or UTF-8 rune is dropped silently instead of being labelled
// malformed. Open OSC 8 links are always closed at the end.
func cleanBashText(chunk []byte, cutStart, cutEnd bool) (text string, skipped int) {
	if cutStart {
		skipped = bashCleanStartSkip(chunk)
		chunk = chunk[skipped:]
	}
	if cutEnd {
		chunk = trimIncompleteRune(chunk)
	}

	var (
		out   strings.Builder
		cr    bool // a CR is held back in case an LF follows
		state byte
		p     = ansi.NewParser()
		link  string
		open  bool
	)
	flushCR := func() {
		if cr {
			out.WriteByte('\r')
			cr = false
		}
	}
	closeLink := func() {
		if !open {
			return
		}
		if link != "" {
			flushCR()
			out.WriteString(" (" + link + ")")
		}
		open, link = false, ""
	}

	for len(chunk) > 0 {
		seq, _, n, next := ansi.DecodeSequence(chunk, state, p)
		chunk = chunk[n:]
		state = next
		if len(seq) == 0 || seq[0] != asciiESC {
			for _, b := range seq {
				switch {
				case b == '\r':
					flushCR()
					cr = true
				case b == '\n':
					cr = false
					out.WriteByte(b)
				default:
					flushCR()
					out.WriteByte(b)
				}
			}
			continue
		}
		if len(chunk) == 0 && next != ansi.NormalState {
			if !cutEnd {
				flushCR()
				writeMalformed(&out, seq)
			}
			break
		}
		switch classifyEscape(seq, p.Command()) {
		case escStrip:
		case escMalformed:
			flushCR()
			writeMalformed(&out, seq)
		case escOSC8:
			closeLink()
			link = osc8URI(seq)
			open = link != ""
		}
	}
	closeLink()
	flushCR()
	return out.String(), skipped
}

type escapeKind int

const (
	escStrip escapeKind = iota
	escMalformed
	escOSC8
)

// classifyEscape decides what to do with a completed ESC-led sequence. cmd is
// the parser command for the sequence.
func classifyEscape(seq []byte, cmd int) escapeKind {
	if len(seq) < 2 {
		return escMalformed
	}
	switch seq[1] {
	case ']':
		if !(seq[len(seq)-1] == asciiBEL || bytes.HasSuffix(seq, []byte{asciiESC, '\\'})) || len(seq) < 3 {
			return escMalformed
		}
		payload := oscPayload(seq)
		if payload == "8" || strings.HasPrefix(payload, "8;") {
			if strings.Count(payload, ";") < 2 {
				return escMalformed
			}
			return escOSC8
		}
		return escStrip
	case 'P', 'X', '^', '_':
		if len(seq) >= 4 && bytes.HasSuffix(seq, []byte{asciiESC, '\\'}) {
			return escStrip
		}
		return escMalformed
	}
	if cmd == 0 {
		return escMalformed
	}
	return escStrip
}

// oscPayload returns the text between "ESC ]" and the terminator.
func oscPayload(seq []byte) string {
	body := seq[2:]
	if bytes.HasSuffix(body, []byte{asciiESC, '\\'}) {
		body = body[:len(body)-2]
	} else {
		body = body[:len(body)-1]
	}
	return string(body)
}

// osc8URI extracts the URI from an OSC 8 sequence, dropping control bytes.
// An empty result means the link is being closed.
func osc8URI(seq []byte) string {
	payload := oscPayload(seq)
	parts := strings.SplitN(payload, ";", 3)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, parts[2])
}

func writeMalformed(out *strings.Builder, seq []byte) {
	shown := seq
	truncated := len(shown) > bashCleanLabelMax
	if truncated {
		shown = shown[:bashCleanLabelMax]
	}
	out.WriteString("[malformed escape: ")
	for _, b := range shown {
		switch {
		case b >= 0x20 && b < 0x7f:
			out.WriteByte(b)
		default:
			out.WriteString(`\x`)
			if b < 0x10 {
				out.WriteByte('0')
			}
			out.WriteString(strconv.FormatUint(uint64(b), 16))
		}
	}
	if truncated {
		out.WriteString("...")
	}
	out.WriteByte(']')
}

// bashCleanStartSkip returns how many leading bytes to drop so a mid-stream
// chunk starts on a line or UTF-8 boundary.
func bashCleanStartSkip(chunk []byte) int {
	window := chunk
	if len(window) > bashCleanStartWindow {
		window = window[:bashCleanStartWindow]
	}
	if i := bytes.IndexByte(window, '\n'); i >= 0 {
		return i + 1
	}
	n := 0
	for n < len(chunk) && n < utf8.UTFMax-1 && !utf8.RuneStart(chunk[n]) {
		n++
	}
	return n
}

// trimIncompleteRune drops a trailing partial UTF-8 sequence.
func trimIncompleteRune(chunk []byte) []byte {
	for i := 1; i < utf8.UTFMax && i <= len(chunk); i++ {
		tail := chunk[len(chunk)-i:]
		if !utf8.RuneStart(tail[0]) {
			continue
		}
		if tail[0] >= utf8.RuneSelf && !utf8.FullRune(tail) {
			return chunk[:len(chunk)-i]
		}
		break
	}
	return chunk
}
