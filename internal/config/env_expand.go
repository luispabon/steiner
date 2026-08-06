package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// undefinedVar records one unresolved environment variable reference.
type undefinedVar struct {
	Path string // YAML path, e.g. "mcp.servers.github.headers.Authorization"
	Line int    // yaml.Node.Line of the scalar holding the reference
	Name string // the variable name
}

// envExpander expands environment references in scalar values, collecting
// references that do not resolve instead of silently dropping them.
type envExpander struct {
	lookup  func(string) (string, bool)
	missing []string // names collected during the current expand call; includes unresolved references found inside default expressions but not the defaulted name itself
}

// expand returns the expanded string plus the names of unresolved references found in it.
// The missing names list is reset per call.
func (e *envExpander) expand(input string) (string, []string) {
	if input == "" {
		e.missing = nil
		return input, nil
	}

	e.missing = nil
	var b strings.Builder
	for i := 0; i < len(input); {
		if input[i] != '$' {
			b.WriteByte(input[i])
			i++
			continue
		}

		next, ok := e.expandToken(input, i, &b)
		if !ok {
			b.WriteByte(input[i])
			i++
			continue
		}
		i = next
	}
	result := b.String()
	missing := e.missing
	e.missing = nil
	return result, missing
}

func (e *envExpander) expandToken(input string, i int, b *strings.Builder) (int, bool) {
	if i+1 < len(input) && input[i+1] == '$' {
		b.WriteByte('$')
		return i + 2, true
	}
	if i+1 < len(input) && input[i+1] == '{' {
		return e.expandBraceToken(input, i, b)
	}
	return e.expandBareToken(input, i, b)
}

func (e *envExpander) expandBraceToken(input string, i int, b *strings.Builder) (int, bool) {
	end := strings.IndexByte(input[i+2:], '}')
	if end < 0 {
		return i, false
	}
	expr := input[i+2 : i+2+end]
	name, defaultValue, hasDefault := strings.Cut(expr, ":-")
	if !isEnvName(name) {
		b.WriteString("${")
		b.WriteString(expr)
		b.WriteByte('}')
		return i + end + 3, true
	}
	if value, ok := e.lookup(name); ok && value != "" {
		b.WriteString(value)
	} else if hasDefault {
		sub := &envExpander{lookup: e.lookup}
		expanded, missing := sub.expand(defaultValue)
		b.WriteString(expanded)
		e.missing = append(e.missing, missing...)
	} else if !ok {
		e.missing = append(e.missing, name)
	}
	return i + end + 3, true
}

func (e *envExpander) expandBareToken(input string, i int, b *strings.Builder) (int, bool) {
	j := i + 1
	for j < len(input) && isEnvContinue(input[j], j == i+1) {
		j++
	}
	if j == i+1 {
		return i, false
	}
	name := input[i+1 : j]
	if value, ok := e.lookup(name); ok {
		b.WriteString(value)
	} else {
		e.missing = append(e.missing, name)
	}
	return j, true
}

// expandNodeTree expands environment references in scalar values throughout a YAML tree,
// collecting unresolved references. It never expands mapping keys.
func expandNodeTree(root *yaml.Node, lookup func(string) (string, bool)) []undefinedVar {
	expander := &envExpander{lookup: lookup}
	var result []undefinedVar
	expandNode(root, "", expander, &result)
	return result
}

func expandNode(node *yaml.Node, path string, expander *envExpander, result *[]undefinedVar) {
	if node == nil {
		return
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) > 0 {
			expandNode(node.Content[0], "", expander, result)
		}
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			keyStr := keyNode.Value

			var newPath string
			if path == "" {
				newPath = keyStr
			} else {
				newPath = path + "." + keyStr
			}
			expandNode(valueNode, newPath, expander, result)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			newPath := path + "[" + fmt.Sprintf("%d", i) + "]"
			expandNode(child, newPath, expander, result)
		}
	case yaml.ScalarNode:
		expanded, missing := expander.expand(node.Value)
		node.Value = expanded
		for _, name := range missing {
			*result = append(*result, undefinedVar{
				Path: path,
				Line: node.Line,
				Name: name,
			})
		}
	case yaml.AliasNode:
		// Skip aliases; they are expanded at their definition site
	}
}
