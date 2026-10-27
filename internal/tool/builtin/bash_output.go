package builtin

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Pure formatting of bash output into an inline preview and a capped
// artifact body. No I/O happens here.
//
// Contract: the input bytes are the already selected representation. They are
// never converted or normalized, so invalid UTF-8 passes through unchanged and
// every length is a raw byte count. A cut that would split a valid multi-byte
// rune is moved to the rune boundary; a cut among invalid bytes is made as is.
// Head and Tail of a stream with Omitted == 0 are joined first, so a rune
// split across them is intact.
//
// Counting: all omission counts are stream bytes absent from the rendered
// text. They never include markers, labels or separators. Markers state the
// stream bytes dropped at that position. Source (capture) omissions are
// reported separately from the total.

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
// every byte of both streams. StdoutOmitted and StderrOmitted count stream
// bytes absent from Text (capture gaps plus rendered cuts); SourceOmitted is
// the capture-gap part of that, copied from the input.
type bashOutPreview struct {
	Text          string
	Complete      bool
	StdoutOmitted int64
	StderrOmitted int64
	SourceOmitted int64
}

// bashOutArtifact is the artifact body with labeled [stdout] and [stderr]
// sections. Complete is true only when Body holds every byte of both streams.
// Omission fields have the same meaning as in bashOutPreview. If the limit is
// below the length of the required labels and separator, Body is empty, the
// labels are absent and Complete is false even when both streams are empty.
type bashOutArtifact struct {
	Body          string
	Complete      bool
	StdoutOmitted int64
	StderrOmitted int64
	SourceOmitted int64
}

// bashOutView is one stream as text.
type bashOutView struct {
	head, tail string
	omitted    int64
}

func (v bashOutView) total() int64 { return int64(len(v.head)+len(v.tail)) + v.omitted }

// all returns the whole text when nothing was omitted.
func (v bashOutView) all() string { return v.head + v.tail }

func newBashOutView(s bashOutStream) bashOutView {
	return bashOutView{head: string(s.Head), tail: string(s.Tail), omitted: max(s.Omitted, 0)}
}

func bashOutMarker(n int64) string {
	return fmt.Sprintf("\n[... %d bytes omitted ...]\n", n)
}

// bashOutSel is a cut of a view: hsrc[:hq], then (if marker) a marker for n
// bytes, then tsrc[ti:]. Without marker, only hsrc[:hq] is kept (whole text,
// or nothing when hq is 0) and n is the number of dropped bytes.
type bashOutSel struct {
	hsrc, tsrc string
	hq, ti     int
	marker     bool
	n          int64
}

func (s bashOutSel) render() string {
	if !s.marker {
		return s.hsrc[:s.hq]
	}
	return s.hsrc[:s.hq] + bashOutMarker(s.n) + s.tsrc[s.ti:]
}

// bashOutSelect cuts v to fit budget bytes, marker included. Fully captured
// text that fits is kept whole; otherwise 30% of the space after the marker
// goes to the head and 70% to the tail (unused share moves to the other
// side). If the marker cannot fit, nothing is kept.
func bashOutSelect(v bashOutView, budget int) bashOutSel {
	total := v.total()
	hsrc, tsrc := v.head, v.tail
	if v.omitted == 0 {
		hsrc = v.all() // join first so a rune split across head/tail stays intact
		tsrc = hsrc
	}
	if total == 0 {
		return bashOutSel{hsrc: hsrc, tsrc: tsrc, ti: len(tsrc)}
	}
	if v.omitted == 0 && total <= int64(budget) {
		return bashOutSel{hsrc: hsrc, tsrc: tsrc, hq: len(hsrc), ti: len(tsrc)}
	}
	markerMax := len(bashOutMarker(total)) // digit count only shrinks below total
	if budget < markerMax {
		return bashOutSel{hsrc: hsrc, tsrc: tsrc, ti: len(tsrc), n: total}
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
	hq = bashOutHeadCut(hsrc, hq)
	ti := bashOutTailCut(tsrc, len(tsrc)-tq)
	kept := int64(hq + len(tsrc) - ti)
	return bashOutSel{hsrc: hsrc, tsrc: tsrc, hq: hq, ti: ti, marker: true, n: total - kept}
}

// bashOutRuneAt returns the start of the valid multi-byte rune that strictly
// spans position pos (start < pos < end), or -1 if pos is not inside one.
func bashOutRuneAt(s string, pos int) (start, end int) {
	for k := 1; k < utf8.UTFMax && pos-k >= 0; k++ {
		i := pos - k
		if !utf8.RuneStart(s[i]) {
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return -1, -1
		}
		if i+size > pos {
			return i, i + size
		}
		return -1, -1
	}
	return -1, -1
}

// bashOutHeadCut moves a head cut back out of a valid rune.
func bashOutHeadCut(s string, hq int) int {
	if hq <= 0 || hq >= len(s) {
		return hq
	}
	if start, _ := bashOutRuneAt(s, hq); start >= 0 {
		return start
	}
	return hq
}

// bashOutTailCut moves a tail start forward past a valid rune it would split.
func bashOutTailCut(s string, ti int) int {
	if ti <= 0 || ti >= len(s) {
		return ti
	}
	if _, end := bashOutRuneAt(s, ti); end >= 0 {
		return end
	}
	return ti
}

// bashOutFit renders v within budget bytes and returns the text and the
// number of v's bytes missing from it.
func bashOutFit(v bashOutView, budget int) (string, int64) {
	s := bashOutSelect(v, budget)
	return s.render(), s.n
}

// bashOutNeed is the budget at which v is rendered with only its capture
// omission (if any) elided.
func bashOutNeed(v bashOutView) int {
	if v.omitted == 0 {
		return len(v.all())
	}
	return len(v.head) + len(v.tail) + len(bashOutMarker(v.total()))
}

// bashOutSep returns the newline needed between pre and a following label.
func bashOutSep(pre string) string {
	if pre == "" || strings.HasSuffix(pre, "\n") {
		return ""
	}
	return "\n"
}

// bashOutCombo is stdout followed by stderr as one view with at most one gap.
// hb and tb are where stderr starts inside v's head and tail coordinates (hb
// == len(head) means no stderr in the head; tb == 0 means the tail is all
// stderr). Retained bytes lying between two capture gaps cannot be shown and
// count as omitted.
type bashOutCombo struct {
	v      bashOutView
	hb, tb int
}

func bashOutCombine(a, b bashOutView) bashOutCombo {
	switch {
	case a.omitted == 0 && b.omitted == 0:
		all := a.all() + b.all()
		return bashOutCombo{v: bashOutView{head: all}, hb: len(a.all()), tb: len(a.all())}
	case b.omitted == 0:
		return bashOutCombo{v: bashOutView{head: a.head, tail: a.tail + b.all(), omitted: a.omitted}, hb: len(a.head), tb: len(a.tail)}
	case a.omitted == 0:
		return bashOutCombo{v: bashOutView{head: a.all() + b.head, tail: b.tail, omitted: b.omitted}, hb: len(a.all())}
	}
	mid := int64(len(a.tail) + len(b.head))
	return bashOutCombo{v: bashOutView{head: a.head, tail: b.tail, omitted: a.omitted + mid + b.omitted}, hb: len(a.head)}
}

// render lays out sel with the stderr label before the first kept stderr
// byte, and reports how many stdout and stderr bytes were kept. No label is
// written when no stderr byte is kept.
func (c bashOutCombo) render(s bashOutSel) (text string, keptOut, keptErr int64) {
	tailKept := len(s.tsrc) - s.ti
	headOut := min(s.hq, c.hb)
	tailOut := min(max(0, c.tb-s.ti), tailKept)
	keptOut = int64(headOut + tailOut)
	keptErr = int64(s.hq-headOut) + int64(tailKept-tailOut)
	head, tail := s.hsrc[:s.hq], s.tsrc[s.ti:]
	marker := ""
	if s.marker {
		marker = bashOutMarker(s.n)
	} else {
		tail = ""
	}
	label := bashOutStderrLabel
	switch {
	case s.hq > c.hb:
		pre := head[:c.hb]
		return pre + bashOutSep(pre) + label + head[c.hb:] + marker + tail, keptOut, keptErr
	case tailKept-tailOut > 0 && tailOut > 0:
		pre := tail[:tailOut]
		return head + marker + pre + bashOutSep(pre) + label + tail[tailOut:], keptOut, keptErr
	case tailKept-tailOut > 0:
		return head + marker + label + tail, keptOut, keptErr
	}
	return head + marker + tail, keptOut, keptErr
}

// formatBashPreview builds the inline preview. Output that fits InlineLimit
// is returned whole, stderr after stdout under a [stderr] label. Otherwise on
// success stdout then labeled stderr are one text with a 30% head / 70% tail
// cut; the label is kept whenever any stderr byte is shown. On nonzero exit
// stderr has priority: all of it if it fits, else its own 30/70 cut, and
// stdout gets the remaining space. Text never exceeds InlineLimit.
func formatBashPreview(in bashOutInput) bashOutPreview {
	limit := max(in.InlineLimit, 0)
	out, errV := newBashOutView(in.Stdout), newBashOutView(in.Stderr)
	p := bashOutPreview{SourceOmitted: out.omitted + errV.omitted}
	if errV.total() == 0 {
		text, om := bashOutFit(out, limit)
		p.Text, p.StdoutOmitted = text, om
		p.Complete = om == 0
		return p
	}
	if out.omitted == 0 && errV.omitted == 0 {
		pre := out.all()
		whole := pre + bashOutSep(pre) + bashOutStderrLabel + errV.all()
		if len(whole) <= limit {
			p.Text, p.Complete = whole, true
			return p
		}
	}
	if in.ExitCode == 0 {
		c := bashOutCombine(out, errV)
		// Reserve the label and a possible separator newline.
		s := bashOutSelect(c.v, limit-len(bashOutStderrLabel)-1)
		text, keptOut, keptErr := c.render(s)
		if keptErr == 0 {
			// No stderr shown, so no label is needed: refit stdout alone with
			// the full limit (stderr stays fully omitted) if that shows more.
			if t2, om := bashOutFit(out, limit); len(t2) > len(text) {
				text, keptOut = t2, out.total()-om
			}
		}
		p.Text = text
		p.StdoutOmitted, p.StderrOmitted = out.total()-keptOut, errV.total()-keptErr
	} else {
		p.Text, p.StdoutOmitted, p.StderrOmitted = bashOutPreviewStderrFirst(out, errV, limit)
	}
	p.Complete = p.StdoutOmitted == 0 && p.StderrOmitted == 0
	return p
}

func bashOutPreviewStderrFirst(out, errV bashOutView, limit int) (string, int64, int64) {
	label := bashOutStderrLabel
	var errText string
	errOm := errV.total()
	if limit > len(label) {
		errText, errOm = bashOutFit(errV, limit-len(label))
	}
	if errText == "" {
		// Nothing of stderr fits (not even a marker): no orphan label, and
		// stdout may use the whole limit.
		outText, outOm := bashOutFit(out, limit)
		return outText, outOm, errV.total()
	}
	rem := limit - len(label) - len(errText)
	outText, outOm := bashOutFit(out, rem)
	if outText != "" && !strings.HasSuffix(outText, "\n") {
		// The separator newline needs one byte; refit without it if absent.
		if len(outText) >= rem {
			outText, outOm = bashOutFit(out, rem-1)
		}
	}
	return outText + bashOutSep(outText) + label + errText, outOm, errOm
}

// formatBashArtifact builds the artifact body:
//
//	[stdout]\n<stdout>\n[stderr]\n<stderr>
//
// Both labels are always present. The content budget (ArtifactLimit minus
// labels) goes 70% to the priority stream (stderr on nonzero exit, stdout on
// success) and 30% to the other; a stream needing less leaves its unused
// share to the other. A stream that still does not fit is cut 30% head / 70%
// tail with a marker. Body never exceeds the limit. If the limit cannot hold
// the labels, Body is empty and everything counts as omitted.
func formatBashArtifact(in bashOutInput) bashOutArtifact {
	limit := in.ArtifactLimit
	if limit <= 0 || limit > bashArtifactMaxBytes {
		limit = bashArtifactMaxBytes
	}
	out, errV := newBashOutView(in.Stdout), newBashOutView(in.Stderr)
	a := bashOutArtifact{SourceOmitted: out.omitted + errV.omitted}
	overhead := len(bashOutStdoutLabel) + len(bashOutArtifactSep) + len(bashOutStderrLabel)
	content := limit - overhead
	if content < 0 {
		a.StdoutOmitted, a.StderrOmitted = out.total(), errV.total()
		return a
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
	a.Body = bashOutStdoutLabel + outText + bashOutArtifactSep + bashOutStderrLabel + errText
	a.StdoutOmitted, a.StderrOmitted = outOm, errOm
	a.Complete = outOm == 0 && errOm == 0
	return a
}
