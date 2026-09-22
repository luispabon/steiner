package config

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const renderTruncateRunes = 60

// diffProject walks projectNode and returns the leaves it overrides relative
// to globalNode (falling back to defaultsNode), sorted security-first then by
// path.
func diffProject(projectNode, globalNode, defaultsNode *yaml.Node) []FieldChange {
	if projectNode == nil {
		return nil
	}
	var changes []FieldChange
	walkMapping(projectNode, nil, globalNode, defaultsNode, &changes)
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Security != changes[j].Security {
			return changes[i].Security
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func walkMapping(node *yaml.Node, segs []string, globalNode, defaultsNode *yaml.Node, changes *[]FieldChange) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		childSegs := appendSeg(segs, key.Value)
		if value.Kind == yaml.MappingNode && len(value.Content) > 0 {
			walkMapping(value, childSegs, globalNode, defaultsNode, changes)
			continue
		}
		emitLeaf(childSegs, resolveAlias(value), globalNode, defaultsNode, changes)
	}
}

// appendSeg returns segs with seg appended, copying the backing array so
// sibling calls in walkMapping never alias each other's slices.
func appendSeg(segs []string, seg string) []string {
	child := make([]string, len(segs)+1)
	copy(child, segs)
	child[len(segs)] = seg
	return child
}

func emitLeaf(segs []string, value *yaml.Node, globalNode, defaultsNode *yaml.Node, changes *[]FieldChange) {
	after := render(value)

	beforeNode, found := lookupPath(globalNode, segs)
	if !found {
		beforeNode, found = lookupPath(defaultsNode, segs)
	}
	unmaskedBefore := "(unset)"
	if found {
		unmaskedBefore = render(beforeNode)
	}
	if unmaskedBefore == after {
		return
	}

	before := unmaskedBefore
	if found && maskBefore(segs) {
		before = "(set)"
	}

	*changes = append(*changes, FieldChange{
		Path:     renderPath(segs),
		Before:   truncateRunes(sanitizeRendered(before)),
		After:    truncateRunes(sanitizeRendered(after)),
		Security: isSecurityPath(segs),
	})
}

// lookupPath walks segs through node's nested mappings, returning the
// resolved leaf node and whether it was found.
func lookupPath(node *yaml.Node, segs []string) (*yaml.Node, bool) {
	if node == nil {
		return nil, false
	}
	current := node
	for _, seg := range segs {
		current = resolveAlias(current)
		if current.Kind != yaml.MappingNode {
			return nil, false
		}
		next, found := mappingGet(current, seg)
		if !found {
			return nil, false
		}
		current = next
	}
	return resolveAlias(current), true
}

func mappingGet(node *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}
	return nil, false
}

func resolveAlias(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		return node.Alias
	}
	return node
}

// renderPath joins segs into a dotted display path. A segment is quoted with
// strconv.Quote when it contains "." or a `"`, or any rune that is not
// unicode.IsGraphic or is whitespace, so a hostile map key (an MCP/LSP server
// name, tool name, or provider name) cannot be mistaken for a path separator
// or forge additional path segments.
func renderPath(segs []string) string {
	parts := make([]string, len(segs))
	for i, seg := range segs {
		if segNeedsQuote(seg) {
			parts[i] = strconv.Quote(seg)
		} else {
			parts[i] = seg
		}
	}
	return strings.Join(parts, ".")
}

func segNeedsQuote(seg string) bool {
	if seg == "" || strings.ContainsAny(seg, ".\"") {
		return true
	}
	for _, r := range seg {
		if !unicode.IsGraphic(r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// sanitizeRendered escapes any rune in s that is not a plain space and not
// unicode.IsGraphic, so a hostile config value cannot inject control
// characters or ANSI escapes (e.g. to forge or hide dialog lines) into
// rendered Before/After text.
func sanitizeRendered(s string) string {
	if strings.IndexFunc(s, isDangerousRune) < 0 {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isDangerousRune(r) {
			b.WriteString(strconv.QuoteRune(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isDangerousRune(r rune) bool {
	return r != ' ' && !unicode.IsGraphic(r)
}

// maskBefore reports whether the before-side value at segs should be masked
// as "(set)" instead of rendered in full.
func maskBefore(segs []string) bool {
	last := segs[len(segs)-1]
	if last == "api_key" || strings.HasSuffix(last, "_api_key") {
		return true
	}
	for _, seg := range segs {
		if seg == "headers" || seg == "env" {
			return true
		}
	}
	return false
}

// render renders node's value as it would appear in YAML, unexpanded and
// untruncated.
func render(node *yaml.Node) string {
	if node == nil {
		return "null"
	}
	node = resolveAlias(node)
	switch node.Kind {
	case yaml.ScalarNode:
		switch node.Tag {
		case "!!null":
			return "null"
		case "!!str":
			return strconv.Quote(node.Value)
		default:
			return node.Value
		}
	case yaml.SequenceNode, yaml.MappingNode:
		return strings.TrimSpace(marshalFlow(node))
	default:
		return node.Value
	}
}

func marshalFlow(node *yaml.Node) string {
	data, err := yaml.Marshal(flowCopy(node))
	if err != nil {
		return ""
	}
	return string(data)
}

func flowCopy(node *yaml.Node) *yaml.Node {
	clone := *node
	clone.Style = yaml.FlowStyle
	clone.HeadComment = ""
	clone.LineComment = ""
	clone.FootComment = ""
	if len(node.Content) > 0 {
		clone.Content = make([]*yaml.Node, len(node.Content))
		for i, child := range node.Content {
			clone.Content[i] = flowCopy(child)
		}
	}
	return &clone
}

// truncateRunes truncates s to renderTruncateRunes runes, appending "…" when
// truncated.
func truncateRunes(s string) string {
	runes := []rune(s)
	if len(runes) <= renderTruncateRunes {
		return s
	}
	return string(runes[:renderTruncateRunes]) + "…"
}
