package repl

import (
	"sort"
	"strings"
)

type Completer struct {
	Commands []string
	Skills   []string
}

func (c Completer) Complete(prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}
	normalized := strings.TrimPrefix(prefix, "/")
	candidates := make([]string, 0, len(c.Commands)+len(c.Skills))
	for _, name := range c.Commands {
		candidates = append(candidates, "/"+name)
	}
	for _, name := range c.Skills {
		candidates = append(candidates, "/"+name)
	}
	matches := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if strings.HasPrefix(strings.TrimPrefix(candidate, "/"), normalized) {
			matches = append(matches, candidate)
		}
	}
	sort.Strings(matches)
	return dedupe(matches)
}

func dedupe(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
