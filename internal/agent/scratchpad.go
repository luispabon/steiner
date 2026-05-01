package agent

import "strings"

type Scratchpad struct {
	Goal string
	Plan string
	Step string
	Next string
	Open string
}

func ParseScratchpad(text string, previous Scratchpad) (Scratchpad, bool) {
	clean := stripTaggedBlock(text, "thinking")
	body, ok := extractTaggedBlock(clean, "scratchpad")
	if !ok {
		return previous, false
	}

	next := previous
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "goal":
			next.Goal = value
		case "plan":
			next.Plan = value
		case "step":
			next.Step = value
		case "next":
			next.Next = value
		case "open":
			next.Open = value
		}
	}
	return next, true
}

func (s Scratchpad) Render() string {
	lines := []string{
		"<scratchpad>",
		"goal: " + s.Goal,
		"plan: " + s.Plan,
		"step: " + s.Step,
		"next: " + s.Next,
		"open: " + s.Open,
		"</scratchpad>",
	}
	return strings.Join(lines, "\n")
}

func StripScratchpad(text string) string {
	return strings.TrimSpace(stripTaggedBlock(text, "scratchpad"))
}

func extractTaggedBlock(text, tag string) (string, bool) {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	start := strings.Index(text, open)
	if start < 0 {
		return "", false
	}
	start += len(open)
	end := strings.Index(text[start:], close)
	if end < 0 {
		return "", false
	}
	return strings.TrimSpace(text[start : start+end]), true
}

func stripTaggedBlock(text, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], close)
		if end < 0 {
			return text
		}
		end += start + len(close)
		text = text[:start] + text[end:]
	}
}
