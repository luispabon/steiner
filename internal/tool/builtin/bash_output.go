package builtin

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Pure formatting of bash output into an inline preview and a capped
// artifact body. No I/O happens here.
//
// Byte accounting: retained Head/Tail bytes are first converted to valid
// UTF-8 (each invalid byte becomes U+FFFD, 3 bytes), and every bound and
// omission count is measured on that converted text. bashOutStream.Omitted is
// added as-is (source bytes the capture dropped between Head and Tail). All
// cuts land on rune boundaries.

const (
	bashOutStdoutLabel = "[stdout]\n"
	bashOutStderrLabel = "[stderr]\n"
	// bashOutArtifactSep separates the stdout and stderr sections of an artifact.
	bashOutArtifactSep = "\n"
	// head share of a truncated stream, in percent; the tail gets the rest.
	bashOutHeadPercent = 30
	// priority stream share of the artifact content budget, in percent.
	bashOutPriorityPercent = 70
)

// bashOutStream is one independent stream as captured: the first bytes (Head),
// the last bytes (Tail), and the count of source bytes dropped between them.
// With Omitted == 0 the stream is Head+Tail, fully captured.
type bashOutStream struct {
	Head, Tail []byte
	Omitted    int64
}

// bashOutInput is the formatter input. InlineLimit bounds the preview in
// bytes. ArtifactLimit bounds the artifact body; zero or values above
// bashArtifactMaxBytes mean bashArtifactMaxBytes.
type bashOutInput struct {
	Stdout, Stderr bashOutStream
	ExitCode       int
	InlineLimit    int
	ArtifactLimit  int
}

// bashOutPreview is the inline preview. Complete is true only when Text holds
// the entire output of both streams (no capture or preview omission).
// OmittedBytes counts every byte of both streams absent from Text.
type bashOutPreview struct {
	Text         string
	Complete     bool
	OmittedBytes int64
}

// bashOutArtifact is the artifact body with labeled [stdout] and [stderr]
// sections. Complete is true only when Body holds every byte of both streams;
// a recovered artifact must never be called complete otherwise.
type bashOutArtifact struct {
	Body          string
	Complete      bool
	StdoutOmitted int64
	StderrOmitted int64
}

// bashOutView is a stream converted to valid UTF-8 text.
type bashOutView struct {
	head, tail string
	omitted    int64
}

func (v bashOutView) total() int64 { return int64(len(v.head)+len(v.tail)) + v.omitted }

// all returns the whole text when nothing was omitted.
func (v bashOutView) all() string { return v.head + v.tail }

func newBashOutView(s bashOutStream) bashOutView {
	return bashOutView{head: bashOutValidUTF8(s.Head), tail: bashOutValidUTF8(s.Tail), omitted: max(s.Omitted, 0)}
}

func bashOutValidUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune(utf8.RuneError)
		} else {
			sb.Write(b[:size])
		}
		b = b[size:]
	}
	return sb.String()
}

func bashOutMarker(n int64) string {
	return fmt.Sprintf("\n[... %d bytes omitted ...]\n", n)
}

// bashOutFit renders v within budget bytes, marker included. It returns the
// text and the number of v's bytes missing from it. Fully captured text that
// fits is returned verbatim; otherwise 30% of the space after the marker goes
// to the head and 70% to the tail (unused share moves to the other side). If
// the marker itself cannot fit, the text is empty and all bytes are omitted.
func bashOutFit(v bashOutView, budget int) (string, int64) {
	total := v.total()
	if total == 0 {
		return "", 0
	}
	if v.omitted == 0 && total <= int64(budget) {
		return v.all(), 0
	}
	markerMax := len(bashOutMarker(total)) // digit count only shrinks below total
	if budget < markerMax {
		return "", total
	}
	hsrc, tsrc := v.head, v.tail
	if v.omitted == 0 {
		hsrc = v.all()
		tsrc = hsrc
	}
	avail := budget - markerMax
	hq := avail * bashOutHeadPercent / 100
	tq := avail - hq
	if hq > len(hsrc) {
		hq = len(hsrc)
		tq = min(len(tsrc), avail-hq)
	} else if tq > len(tsrc) {
		tq = len(tsrc)
		hq = min(len(hsrc), avail-tq)
	}
	// Cut on rune boundaries: the first excluded byte of the head must start
	// a rune; the first kept byte of the tail must start a rune.
	for hq > 0 && hq < len(hsrc) && !utf8.RuneStart(hsrc[hq]) {
		hq--
	}
	ti := len(tsrc) - tq
	for ti < len(tsrc) && !utf8.RuneStart(tsrc[ti]) {
		ti++
	}
	head, tail := hsrc[:hq], tsrc[ti:]
	omitted := total - int64(len(head)+len(tail))
	return head + bashOutMarker(omitted) + tail, omitted
}

// bashOutNeed is the budget at which v is rendered with only its capture
// omission (if any) elided.
func bashOutNeed(v bashOutView) int {
	if v.omitted == 0 {
		return len(v.all())
	}
	return len(v.head) + len(v.tail) + len(bashOutMarker(v.total()))
}

// bashOutCombine concatenates a then join then b, keeping at most one gap
// (the capture omission). Retained bytes between two gaps are folded into the
// omitted count.
func bashOutCombine(a, b bashOutView, join string) bashOutView {
	switch {
	case a.omitted == 0 && b.omitted == 0:
		return bashOutView{head: a.all() + join + b.all()}
	case b.omitted == 0:
		return bashOutView{head: a.head, tail: a.tail + join + b.all(), omitted: a.omitted}
	case a.omitted == 0:
		return bashOutView{head: a.all() + join + b.head, tail: b.tail, omitted: b.omitted}
	}
	mid := int64(len(a.tail) + len(join) + len(b.head))
	return bashOutView{head: a.head, tail: b.tail, omitted: a.omitted + mid + b.omitted}
}

// bashOutJoin returns the separator and label that introduce stderr after out.
func bashOutJoin(out bashOutView) string {
	end := out.all()
	if out.omitted > 0 {
		end = out.tail
	}
	if out.total() > 0 && !strings.HasSuffix(end, "\n") {
		return "\n" + bashOutStderrLabel
	}
	return bashOutStderrLabel
}

// formatBashPreview builds the inline preview. Output that fits InlineLimit
// is returned whole. Otherwise on success stdout then labeled stderr are
// treated as one text with a 30% head / 70% tail cut. On nonzero exit stderr
// has priority: all of it if it fits, else its own 30/70 cut, and stdout gets
// the remaining space. Text never exceeds InlineLimit.
func formatBashPreview(in bashOutInput) bashOutPreview {
	limit := max(in.InlineLimit, 0)
	out, errV := newBashOutView(in.Stdout), newBashOutView(in.Stderr)
	if errV.total() == 0 {
		text, om := bashOutFit(out, limit)
		return bashOutPreview{Text: text, Complete: om == 0, OmittedBytes: om}
	}
	combined := bashOutCombine(out, errV, bashOutJoin(out))
	if combined.omitted == 0 && len(combined.all()) <= limit {
		return bashOutPreview{Text: combined.all(), Complete: true}
	}
	if in.ExitCode == 0 {
		text, om := bashOutFit(combined, limit)
		// om is measured on the combined text, so it can include the few
		// synthetic separator/label bytes; it is always > 0 here.
		return bashOutPreview{Text: text, Complete: om == 0, OmittedBytes: om}
	}
	return bashOutPreviewStderrFirst(out, errV, limit)
}

func bashOutPreviewStderrFirst(out, errV bashOutView, limit int) bashOutPreview {
	label := bashOutStderrLabel
	if limit <= len(label) {
		return bashOutPreview{OmittedBytes: out.total() + errV.total()}
	}
	errText, errOm := bashOutFit(errV, limit-len(label))
	sepReserve := 1
	var outText string
	outOm := out.total()
	if rem := limit - len(label) - len(errText) - sepReserve; rem > 0 {
		outText, outOm = bashOutFit(out, rem)
	}
	text := outText
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += label + errText
	om := outOm + errOm
	return bashOutPreview{Text: text, Complete: om == 0, OmittedBytes: om}
}

// formatBashArtifact builds the artifact body:
//
//	[stdout]\n<stdout>\n[stderr]\n<stderr>
//
// Both labels are always present. The content budget (ArtifactLimit minus
// labels) goes 70% to the priority stream (stderr on nonzero exit, stdout on
// success) and 30% to the other; a stream needing less leaves its unused
// share to the other. A stream that still does not fit is cut 30% head / 70%
// tail with a marker. Body never exceeds the limit.
func formatBashArtifact(in bashOutInput) bashOutArtifact {
	limit := in.ArtifactLimit
	if limit <= 0 || limit > bashArtifactMaxBytes {
		limit = bashArtifactMaxBytes
	}
	out, errV := newBashOutView(in.Stdout), newBashOutView(in.Stderr)
	overhead := len(bashOutStdoutLabel) + len(bashOutArtifactSep) + len(bashOutStderrLabel)
	content := limit - overhead
	if content < 0 {
		return bashOutArtifact{StdoutOmitted: out.total(), StderrOmitted: errV.total()}
	}
	pri, oth := out, errV
	if in.ExitCode != 0 {
		pri, oth = errV, out
	}
	shareP := content * bashOutPriorityPercent / 100
	shareO := content - shareP
	bP, bO := shareP, shareO
	if nP := bashOutNeed(pri); nP <= shareP {
		bP, bO = nP, content-nP
	} else if nO := bashOutNeed(oth); nO <= shareO {
		bP, bO = content-nO, nO
	}
	bOut, bErr := bP, bO
	if in.ExitCode != 0 {
		bOut, bErr = bO, bP
	}
	outText, outOm := bashOutFit(out, bOut)
	errText, errOm := bashOutFit(errV, bErr)
	return bashOutArtifact{
		Body:          bashOutStdoutLabel + outText + bashOutArtifactSep + bashOutStderrLabel + errText,
		Complete:      outOm == 0 && errOm == 0,
		StdoutOmitted: outOm,
		StderrOmitted: errOm,
	}
}
