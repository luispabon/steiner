package tool

import "encoding/json"

// CloneJSONValue returns a deep copy of v, handling nil, bool, numeric, string,
// []any, map[string]any, and json.RawMessage. Other types are returned as-is.
func CloneJSONValue(v any) any {
	switch val := v.(type) {
	case nil:
		return nil
	case map[string]any:
		return CloneJSONMap(val)
	case []any:
		cloned := make([]any, len(val))
		for i, child := range val {
			cloned[i] = CloneJSONValue(child)
		}
		return cloned
	case json.RawMessage:
		return append(json.RawMessage(nil), val...)
	default:
		return val
	}
}

// CloneJSONMap returns a deep copy of m. If m is nil, CloneJSONMap returns nil.
func CloneJSONMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	cloned := make(map[string]any, len(m))
	for key, value := range m {
		cloned[key] = CloneJSONValue(value)
	}
	return cloned
}
