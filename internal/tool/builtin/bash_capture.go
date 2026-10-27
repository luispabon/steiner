package builtin

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
)

// streamCapture is a bounded capture of a byte stream: the first bytes (Head)
// and the actual last bytes (Tail), with any middle gap dropped.
type streamCapture struct {
	// Head is the first min(TotalBytes, headLimit) bytes.
	Head []byte
	// Tail is the last min(TotalBytes, tailLimit) bytes, trimmed so it does
	// not overlap Head.
	Tail []byte
	// TotalBytes counts every byte before the marker (or before the error).
	TotalBytes int64
	// HeadTruncated reports that TotalBytes exceeded the head limit.
	HeadTruncated bool
}

// captureUntilMarker reads r until marker appears, excluding the marker bytes
// and consuming nothing past them. Output is retained as bounded head and
// tail; the reader is drained after the caps are reached. On a reader error
// (including io.EOF) the partial capture is returned with the error.
func captureUntilMarker(r *bufio.Reader, marker string, headLimit, tailLimit int) (streamCapture, error) {
	if marker == "" {
		return streamCapture{}, errors.New("capture marker is empty")
	}
	headLimit = max(headLimit, 0)
	tailLimit = max(tailLimit, 0)

	var (
		total int64
		head  []byte
		tail  []byte
		carry []byte // unconfirmed suffix that might begin the marker
	)
	commit := func(p []byte) {
		if len(p) == 0 {
			return
		}
		total += int64(len(p))
		if room := headLimit - len(head); room > 0 {
			head = append(head, p[:min(room, len(p))]...)
		}
		if tailLimit > 0 {
			tail = append(tail, p...)
			if len(tail) > 2*tailLimit {
				n := copy(tail, tail[len(tail)-tailLimit:])
				tail = tail[:n]
			}
		}
	}
	finish := func(err error) (streamCapture, error) {
		if len(tail) > tailLimit {
			tail = tail[len(tail)-tailLimit:]
		}
		// Tail covers stream offsets [total-len(tail), total); drop the part
		// already held by head.
		if overlap := int64(len(head)) - (total - int64(len(tail))); overlap > 0 {
			tail = tail[min(overlap, int64(len(tail))):]
		}
		return streamCapture{
			Head:          head,
			Tail:          append([]byte(nil), tail...),
			TotalBytes:    total,
			HeadTruncated: total > int64(headLimit),
		}, err
	}

	mk := []byte(marker)
	for {
		if r.Buffered() == 0 {
			if _, err := r.Peek(1); err != nil {
				commit(carry)
				return finish(fmt.Errorf("read until marker: %w", err))
			}
		}
		chunk, err := r.Peek(r.Buffered())
		if err != nil {
			commit(carry)
			return finish(fmt.Errorf("read until marker: %w", err))
		}
		window := make([]byte, 0, len(carry)+len(chunk))
		window = append(window, carry...)
		window = append(window, chunk...)

		if i := bytes.Index(window, mk); i >= 0 {
			commit(window[:i])
			if _, err := r.Discard(i + len(mk) - len(carry)); err != nil {
				return finish(fmt.Errorf("read until marker: %w", err))
			}
			return finish(nil)
		}
		keep := min(len(window), len(mk)-1)
		commit(window[:len(window)-keep])
		carry = append(carry[:0], window[len(window)-keep:]...)
		if _, err := r.Discard(len(chunk)); err != nil {
			return finish(fmt.Errorf("read until marker: %w", err))
		}
	}
}
