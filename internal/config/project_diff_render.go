package config

import (
	"sort"
	"strconv"
	"strings"

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
	walkMapping(projectNode, "", globalNode, defaultsNode, &changes)
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Security != changes[j].Security {
			return changes[i].Security
		}
		return changes[i].Path < changes[j].Path
	})
	return changes
}

func walkMapping(node *yaml.Node, prefix string, globalNode, defaultsNode *yaml.Node, changes *[]FieldChange) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		path := joinPath(prefix, key.Value)
		if value.Kind == yaml.MappingNode && len(value.Content) > 0 {
			walkMapping(value, path, globalNode, defaultsNode, changes)
			continue
		}
		emitLeaf(path, resolveAlias(value), globalNode, defaultsNode, changes)
	}
}

func emitLeaf(path string, value *yaml.Node, globalNode, defaultsNode *yaml.Node, changes *[]FieldChange) {
	after := render(value)

	beforeNode, found := lookupPath(globalNode, path)
	if !found {
		beforeNode, found = lookupPath(defaultsNode, path)
	}
	unmaskedBefore := "(unset)"
	if found {
		unmaskedBefore = render(beforeNode)
	}
	if unmaskedBefore == after {
		return
	}

	before := unmaskedBefore
	if found && maskBefore(path) {
		before = "(set)"
	}

	*changes = append(*changes, FieldChange{
		Path:     path,
		Before:   truncateRunes(before),
		After:    truncateRunes(after),
		Security: isSecurityPath(path),
	})
}

// lookupPath walks a dotted path through node's nested mappings, returning
// the resolved leaf node and whether it was found.
func lookupPath(node *yaml.Node, path string) (*yaml.Node, bool) {
	if node == nil {
		return nil, false
	}
	current := node
	for _, seg := range strings.Split(path, ".") {
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

func joinPath(prefix, segment string) string {
	if prefix == "" {
		return segment
	}
	return prefix + "." + segment
}

// maskBefore reports whether the before-side value at path should be masked
// as "(set)" instead of rendered in full.
func maskBefore(path string) bool {
	segs := strings.Split(path, ".")
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
