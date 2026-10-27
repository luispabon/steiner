package builtin

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// bashCleanStartWindow is how far into a chunk cleanBashText looks for a
	// newline when aligning a chunk that begins mid-stream.
	bashCleanStartWindow = 4096
	// bashCleanLabelMax caps how many raw bytes of a malformed escape are shown;
	// the label states how many further bytes were omitted.
	bashCleanLabelMax = 8
	// bashCleanMaxExpansion bounds output length relative to input length:
	// the worst case is a lone ESC byte rendered as "[malformed escape: \x1b]"
	// (24 bytes), so output never exceeds 24*len(input) bytes. Longer labels
	// are capped at about 80 bytes plus the omitted-count digits.
	bashCleanMaxExpansion = 24
)

const (
	asciiBEL = 0x07
	asciiCAN = 0x18
	asciiSUB = 0x1a
	asciiESC = 0x1b
	c1DCS    = 0x90
	c1SOS    = 0x98
	c1CSI    = 0x9b
	c1ST     = 0x9c
	c1OSC    = 0x9d
	c1PM     = 0x9e
	c1APC    = 0x9f
)

// cleanBashText turns raw terminal output into readable text. CRLF becomes
// LF only when the CR and LF are adjacent in the input; standalone CR, other
// C0 controls and UTF-8 encoded C1 characters pass through. Valid ANSI
// sequences (7-bit ESC forms and raw 8-bit C1 introducers CSI, OSC, DCS, SOS,
// PM and APC) and OSC sequences other than OSC 8 are stripped. OSC 8
// hyperlinks become "text (uri)", whatever the terminator (BEL, ESC \ or raw
// ST 0x9c). Malformed or incomplete escapes are replaced with
// "[malformed escape: \x1b...]"; labels longer than 8 bytes end in
// "...(N bytes omitted)" with the exact omitted byte count.
//
// cutStart means the chunk begins mid-stream: leading bytes are dropped up to
// and including the first newline within the first 4 KiB, otherwise up to the
// next UTF-8 boundary. cutEnd means the chunk was truncated by capture: a
// trailing incomplete UTF-8 rune or escape sequence is dropped instead of
// being labelled malformed. skipped is the total number of input bytes
// discarded at both boundaries (cut start plus cut end), so callers can
// report the omission. Open OSC 8 links are always closed at the end.
func cleanBashText(chunk []byte, cutStart, cutEnd bool) (text string, skipped int) {
	if cutStart {
		skipped = bashCleanStartSkip(chunk)
		chunk = chunk[skipped:]
	}
	if cutEnd {
		trimmed := trimIncompleteRune(chunk)
		skipped += len(chunk) - len(trimmed)
		chunk = trimmed
	}

	var (
		out  strings.Builder
		cr   bool // a CR is held back in case an LF follows
		link string
		open bool
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

	for i := 0; i < len(chunk); {
		c := chunk[i]
		if !isSequenceIntroducer(c) {
			switch {
			case c == '\r':
				flushCR()
				cr = true
				i++
			case c == '\n':
				cr = false
				out.WriteByte(c)
				i++
			default:
				flushCR()
				size := 1
				if c >= utf8.RuneSelf {
					// Copy a whole valid rune so continuation bytes such as
					// 0x9c are never mistaken for C1 controls.
					if _, sz := utf8.DecodeRune(chunk[i:]); sz > 1 {
						size = sz
					}
				}
				out.Write(chunk[i : i+size])
				i += size
			}
			continue
		}
		n, status := scanSequence(chunk[i:])
		seq := chunk[i : i+n]
		i += n
		flushCR()
		switch status {
		case seqIncomplete:
			if cutEnd {
				skipped += n
			} else {
				writeMalformed(&out, seq)
			}
		case seqMalformed:
			writeMalformed(&out, seq)
		case seqOK:
			switch classifyEscape(seq) {
			case escMalformed:
				writeMalformed(&out, seq)
			case escOSC8:
				closeLink()
				link = osc8URI(seq)
				open = link != ""
			}
		}
	}
	closeLink()
	flushCR()
	return out.String(), skipped
}

// isSequenceIntroducer reports whether c starts an escape sequence when it
// appears where a rune starts: ESC, or a raw C1 byte (never valid UTF-8 on
// its own, so it cannot be part of a text rune).
func isSequenceIntroducer(c byte) bool {
	switch c {
	case asciiESC, c1DCS, c1SOS, c1CSI, c1OSC, c1PM, c1APC:
		return true
	}
	return false
}

type seqStatus int

const (
	seqOK         seqStatus = iota // complete, valid sequence
	seqMalformed                   // invalid or interrupted before its end
	seqIncomplete                  // input ended inside the sequence
)

// scanSequence measures the escape sequence starting at b[0], which must be an
// introducer. It returns the bytes consumed and whether the sequence is valid.
// For malformed sequences the offending byte is not consumed (except for a
// lone introducer, which consumes itself so scanning always progresses).
func scanSequence(b []byte) (int, seqStatus) {
	switch b[0] {
	case c1CSI:
		return scanCSI(b, 1)
	case c1OSC:
		return scanString(b, 1, true)
	case c1DCS, c1SOS, c1PM, c1APC:
		return scanString(b, 1, false)
	}
	if len(b) == 1 {
		return 1, seqIncomplete
	}
	switch b[1] {
	case '[':
		return scanCSI(b, 2)
	case ']':
		return scanString(b, 2, true)
	case 'P', 'X', '^', '_':
		return scanString(b, 2, false)
	}
	i := 1
	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x2f {
		i++
	}
	switch {
	case i == len(b):
		return i, seqIncomplete
	case b[i] >= 0x30 && b[i] <= 0x7e:
		return i + 1, seqOK
	}
	return i, seqMalformed
}

// scanCSI scans CSI parameters, intermediates and the final byte from b[i:].
// Parameter count is unbounded; nothing is stored.
func scanCSI(b []byte, i int) (int, seqStatus) {
	for i < len(b) && b[i] >= 0x30 && b[i] <= 0x3f {
		i++
	}
	for i < len(b) && b[i] >= 0x20 && b[i] <= 0x2f {
		i++
	}
	switch {
	case i == len(b):
		return i, seqIncomplete
	case b[i] >= 0x40 && b[i] <= 0x7e:
		return i + 1, seqOK
	}
	return i, seqMalformed
}

// scanString scans a string-type sequence body from b[i:] up to its
// terminator: ST (ESC \ or raw 0x9c), or BEL when bel is set.
func scanString(b []byte, i int, bel bool) (int, seqStatus) {
	for i < len(b) {
		c := b[i]
		switch {
		case c == asciiBEL && bel:
			return i + 1, seqOK
		case c == c1ST:
			return i + 1, seqOK
		case c == asciiESC:
			if i+1 == len(b) {
				return len(b), seqIncomplete
			}
			if b[i+1] == '\\' {
				return i + 2, seqOK
			}
			return i, seqMalformed
		case c == asciiCAN || c == asciiSUB:
			return i, seqMalformed
		case c >= utf8.RuneSelf:
			// Skip whole valid runes so a continuation byte 0x9c is not ST.
			if _, sz := utf8.DecodeRune(b[i:]); sz > 1 {
				i += sz
				continue
			}
		}
		i++
	}
	return len(b), seqIncomplete
}

type escapeKind int

const (
	escStrip escapeKind = iota
	escMalformed
	escOSC8
)

// classifyEscape decides what to do with a complete, valid sequence.
func classifyEscape(seq []byte) escapeKind {
	if !isOSC(seq) {
		return escStrip
	}
	payload := oscPayload(seq)
	if payload == "" {
		return escMalformed
	}
	if payload == "8" || strings.HasPrefix(payload, "8;") {
		if strings.Count(payload, ";") < 2 {
			return escMalformed
		}
		return escOSC8
	}
	return escStrip
}

func isOSC(seq []byte) bool {
	return seq[0] == c1OSC || (len(seq) > 1 && seq[0] == asciiESC && seq[1] == ']')
}

// oscPayload returns the text between the OSC introducer and the terminator.
func oscPayload(seq []byte) string {
	body := seq[1:]
	if seq[0] == asciiESC {
		body = seq[2:]
	}
	switch {
	case bytes.HasSuffix(body, []byte{asciiESC, '\\'}):
		body = body[:len(body)-2]
	default:
		body = body[:len(body)-1] // BEL or raw ST
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
		out.WriteString("..." + "(" + strconv.Itoa(len(seq)-bashCleanLabelMax) + " bytes omitted)")
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
