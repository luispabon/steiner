package tool

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"
)

type StreamCapture struct {
	Bytes     int    `json:"bytes"`
	Shown     int    `json:"shown,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
	Preview   string `json:"preview,omitempty"`
}

type ExecutionMetadata struct {
	ExitCode int           `json:"exit_code"`
	Stdout   StreamCapture `json:"stdout"`
	Stderr   StreamCapture `json:"stderr"`
}

type ExecutionResult struct {
	Value    any               `json:"value,omitempty"`
	Metadata ExecutionMetadata `json:"metadata"`
}

type boundedCapture struct {
	limit     int
	total     int
	truncated bool
	buf       bytes.Buffer
}

func newBoundedCapture(limit int) *boundedCapture {
	if limit < 0 {
		limit = 0
	}
	return &boundedCapture{limit: limit}
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	if c == nil {
		return len(p), nil
	}
	c.total += len(p)
	if c.buf.Len() < c.limit {
		remaining := c.limit - c.buf.Len()
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = c.buf.Write(p[:remaining])
		if remaining < len(p) {
			c.truncated = true
		}
	} else if len(p) > 0 {
		c.truncated = true
	}
	return len(p), nil
}

func (c *boundedCapture) Capture() StreamCapture {
	if c == nil {
		return StreamCapture{}
	}

	raw := c.buf.Bytes()
	capture := StreamCapture{
		Bytes:     c.total,
		Truncated: c.truncated,
		Binary:    isBinaryCapture(raw, c.truncated),
	}
	if capture.Binary {
		previewBytes := raw
		if len(previewBytes) > 16 {
			previewBytes = previewBytes[:16]
		}
		capture.Shown = len(previewBytes)
		capture.Preview = hex.EncodeToString(previewBytes)
		return capture
	}

	previewBytes := raw
	if len(previewBytes) > 0 && !utf8.Valid(previewBytes) {
		previewBytes = bytes.ToValidUTF8(previewBytes, []byte("?"))
	}
	capture.Shown = len(previewBytes)
	capture.Preview = string(previewBytes)
	return capture
}

func (c *boundedCapture) Bytes() []byte {
	if c == nil {
		return nil
	}
	return append([]byte(nil), c.buf.Bytes()...)
}

func (c StreamCapture) Summary() string {
	switch {
	case c.Binary:
		return fmt.Sprintf("<binary output shown=%d total=%d>", c.Shown, c.Bytes)
	case c.Truncated:
		if c.Preview == "" {
			return fmt.Sprintf("<truncated output shown=%d total=%d>", c.Shown, c.Bytes)
		}
		return fmt.Sprintf("%s\n<truncated output shown=%d total=%d>", c.Preview, c.Shown, c.Bytes)
	default:
		return c.Preview
	}
}

func (m ExecutionMetadata) Truncated() bool {
	return m.Stdout.Truncated || m.Stderr.Truncated
}

func (m ExecutionMetadata) Binary() bool {
	return m.Stdout.Binary || m.Stderr.Binary
}

func (m ExecutionMetadata) Summary() string {
	parts := make([]string, 0, 3)
	if stdout := strings.TrimSpace(m.Stdout.Summary()); stdout != "" {
		parts = append(parts, "stdout="+stdout)
	}
	if stderr := strings.TrimSpace(m.Stderr.Summary()); stderr != "" {
		parts = append(parts, "stderr="+stderr)
	}
	if m.ExitCode != 0 {
		parts = append(parts, fmt.Sprintf("exit_code=%d", m.ExitCode))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func isBinaryCapture(raw []byte, truncated bool) bool {
	if len(raw) == 0 {
		return false
	}
	if bytes.IndexByte(raw, 0) >= 0 {
		return true
	}
	if truncated {
		return false
	}
	return !utf8.Valid(raw)
}
