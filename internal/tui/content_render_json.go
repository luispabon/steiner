package tui

import (
	"encoding/json"
	"io"
	"sort"
	"strconv"
	"strings"
)

const jsonBodyMaxLines = 10

func parseJSONToolResult(raw string) (any, string, bool) {
	value, ok := decodeJSONValue(raw)
	if !ok {
		return nil, "", false
	}

	if object, ok := value.(map[string]any); ok {
		_, hasResult := object["result"]
		_, hasError := object["error"]
		if envelopeOK, isBool := object["ok"].(bool); isBool && (hasResult || hasError) {
			if !envelopeOK {
				message := jsonEnvelopeErrorText(object["error"])
				if message == "" {
					return nil, "", false
				}
				return nil, message, true
			}

			result, hasResult := object["result"]
			if !hasResult {
				return nil, "", false
			}
			if resultString, isString := result.(string); isString {
				result, ok = decodeJSONValue(resultString)
				if !ok {
					return nil, "", false
				}
			}
			if !isJSONDocument(result) {
				return nil, "", false
			}
			return result, "", true
		}
	}

	if !isJSONDocument(value) {
		return nil, "", false
	}
	return value, "", true
}

func decodeJSONValue(raw string) (any, bool) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, false
	}
	return value, true
}

func isJSONDocument(value any) bool {
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}

func jsonEnvelopeErrorText(value any) string {
	errorObject, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	message, _ := errorObject["message"].(string)
	if strings.TrimSpace(message) != "" {
		return message
	}
	kind, _ := errorObject["kind"].(string)
	if strings.TrimSpace(kind) == "" {
		return ""
	}
	return kind
}

func (b *contentBuffer) buildJSONLines(tc *toolCallSegment) []string {
	value, errorText, ok := parseJSONToolResult(tc.body)
	if !ok {
		return b.buildPlainLines(tc)
	}
	if errorText != "" {
		return []string{b.styles.Removed.Render("✗ " + errorText)}
	}

	logical := make([]jsonLine, 0)
	appendJSONValue(&logical, 0, "", "", value)

	lines := make([]string, 0, min(len(logical), jsonBodyMaxLines)+1)
	limit := min(len(logical), jsonBodyMaxLines)
	for _, line := range logical[:limit] {
		lines = append(lines, b.renderJSONLine(line))
	}
	if len(logical) > jsonBodyMaxLines {
		lines = append(lines, b.styles.FgMute.Render("+ "+strconv.Itoa(len(logical)-jsonBodyMaxLines)+" more lines"))
	}
	return lines
}

type jsonLine struct {
	indent int
	prefix string
	key    string
	value  string
	hasVal bool
}

func appendJSONValue(lines *[]jsonLine, indent int, prefix, key string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 0 {
			appendJSONScalar(lines, indent, prefix, key, "{}")
			return
		}
		if key != "" {
			*lines = append(*lines, jsonLine{indent: indent, prefix: prefix, key: key})
		}
		childIndent := indent + len(prefix)
		if key != "" {
			childIndent += 2
		}
		keys := make([]string, 0, len(typed))
		for childKey := range typed {
			keys = append(keys, childKey)
		}
		sort.Strings(keys)
		for _, childKey := range keys {
			appendJSONValue(lines, childIndent, "", childKey, typed[childKey])
		}
	case []any:
		if len(typed) == 0 {
			appendJSONScalar(lines, indent, prefix, key, "[]")
			return
		}
		if key != "" {
			*lines = append(*lines, jsonLine{indent: indent, prefix: prefix, key: key})
		}
		itemIndent := indent + len(prefix)
		if key != "" {
			itemIndent += 2
		}
		for i, item := range typed {
			if i == 3 {
				*lines = append(*lines, jsonLine{
					indent: itemIndent,
					value:  "+ " + strconv.Itoa(len(typed)-3) + " more items",
				})
				break
			}
			appendJSONArrayItem(lines, itemIndent, item)
		}
	default:
		appendJSONScalar(lines, indent, prefix, key, jsonScalarText(typed))
	}
}

func appendJSONArrayItem(lines *[]jsonLine, indent int, value any) {
	object, isObject := value.(map[string]any)
	if !isObject {
		appendJSONValue(lines, indent, "- ", "", value)
		return
	}
	if len(object) == 0 {
		appendJSONValue(lines, indent, "- ", "", map[string]any{})
		return
	}

	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	appendJSONValue(lines, indent, "- ", keys[0], object[keys[0]])
	for _, key := range keys[1:] {
		appendJSONValue(lines, indent+2, "", key, object[key])
	}
}

func appendJSONScalar(lines *[]jsonLine, indent int, prefix, key, value string) {
	*lines = append(*lines, jsonLine{indent: indent, prefix: prefix, key: key, value: value, hasVal: true})
}

func jsonScalarText(value any) string {
	if stringValue, ok := value.(string); ok {
		runes := []rune(stringValue)
		if len(runes) > 100 {
			stringValue = string(runes[:99]) + "…"
		}
		quoted := strconv.Quote(stringValue)
		return quoted[1 : len(quoted)-1]
	}
	if number, ok := value.(json.Number); ok {
		return number.String()
	}
	if value == nil {
		return "null"
	}
	return strconv.FormatBool(value.(bool))
}

func (b *contentBuffer) renderJSONLine(line jsonLine) string {
	left := strings.Repeat(" ", line.indent) + line.prefix
	if line.key != "" {
		key := b.styles.FgDim.Render(left + line.key + ":")
		if line.hasVal {
			return key + " " + line.value
		}
		return key
	}
	return b.styles.FgDim.Render(left + line.value)
}
