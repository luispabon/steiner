package provider

import (
	"bufio"
	"io"
	"strings"
)

// readSSEFrame reads one server-sent event frame and returns its last event:
// name and its data: lines joined with "\n". A frame ends at a blank line or
// at EOF. A frame is returned when it has data or an event name, so an
// event-only frame yields (name, "", nil). Blank frames are skipped. Other
// fields, including ':' comments, are ignored. A non-EOF read error is
// returned as-is and discards the partial frame. At EOF with no pending frame
// it returns io.EOF.
func readSSEFrame(reader *bufio.Reader) (string, string, error) {
	var data strings.Builder
	var eventType string
	for {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if data.Len() > 0 || eventType != "" {
				return eventType, data.String(), nil
			}
			if err == io.EOF {
				return "", "", io.EOF
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if err == io.EOF {
			if data.Len() > 0 || eventType != "" {
				return eventType, data.String(), nil
			}
			return "", "", io.EOF
		}
	}
}
