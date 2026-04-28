package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// decodeInput decodes a raw map into a typed input struct T using strict JSON
// decoding that rejects unknown fields.
func decodeInput[T any](raw map[string]any) (T, error) {
	var zero T
	b, err := json.Marshal(raw)
	if err != nil {
		return zero, fmt.Errorf("marshal input: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		return zero, fmt.Errorf("decode input: %w", err)
	}
	return v, nil
}
