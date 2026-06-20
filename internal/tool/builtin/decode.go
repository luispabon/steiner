package builtin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// decodeInput decodes a raw map into a typed input struct T using strict
// decoding that rejects unknown fields.
//
// The input often comes from an LLM as a loosely typed map with numbers in
// float64 form and strings that need to survive nested struct/slice decoding.
// We normalize the raw value tree first, then round-trip through
// encoding/json so the final assignment path stays on the standard library.
func decodeInput[T any](raw map[string]any) (T, error) {
	var zero T

	t := reflect.TypeFor[T]()
	if t == nil {
		return zero, fmt.Errorf("decode input: nil type")
	}

	baseType, err := inputBaseType(t)
	if err != nil {
		return zero, err
	}

	cleaned, err := coerceInputValue(baseType, raw)
	if err != nil {
		return zero, err
	}

	payload, err := json.Marshal(cleaned)
	if err != nil {
		return zero, fmt.Errorf("decode input: marshal input: %w", err)
	}

	target := reflect.New(baseType)
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target.Interface()); err != nil {
		return zero, fmt.Errorf("decode input: %w", err)
	}

	if t.Kind() == reflect.Pointer {
		return target.Interface().(T), nil
	}
	return target.Elem().Interface().(T), nil
}

func inputBaseType(t reflect.Type) (reflect.Type, error) {
	if t == nil {
		return nil, fmt.Errorf("decode input: nil type")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("decode input: unsupported type %v", t)
	}
	return t, nil
}

func coerceInputValue(t reflect.Type, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}

	rv := reflect.ValueOf(raw)
	if rv.Type().AssignableTo(t) {
		return raw, nil
	}

	switch t.Kind() {
	case reflect.Pointer:
		return coerceInputValue(t.Elem(), raw)
	case reflect.Struct:
		return coerceStructValue(t, raw)
	case reflect.Slice:
		return coerceSliceValue(t, raw)
	default:
		return coerceScalarValue(t, rv, raw)
	}
}

func coerceStructValue(t reflect.Type, raw any) (any, error) {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected object for struct field, got %T", raw)
	}

	known := jsonFieldIndexes(t)
	coerced := make(map[string]any, len(rawMap))
	for key, value := range rawMap {
		idx, ok := known[key]
		if !ok {
			validFields := make([]string, 0, len(known))
			for name := range known {
				validFields = append(validFields, name)
			}
			sort.Strings(validFields)
			return nil, fmt.Errorf("decode input: unknown field %q; valid fields: %s", key, strings.Join(validFields, ", "))
		}

		field := t.Field(idx)
		next, err := coerceInputValue(field.Type, value)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", key, err)
		}
		coerced[key] = next
	}

	return coerced, nil
}

func coerceSliceValue(t reflect.Type, raw any) (any, error) {
	rawSlice, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("expected array for slice field, got %T", raw)
	}

	coerced := make([]any, 0, len(rawSlice))
	for i, item := range rawSlice {
		next, err := coerceInputValue(t.Elem(), item)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		coerced = append(coerced, next)
	}
	return coerced, nil
}

func jsonFieldIndexes(t reflect.Type) map[string]int {
	known := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := jsonFieldName(t.Field(i))
		if name != "" {
			known[name] = i
		}
	}
	return known
}

func jsonFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return ""
	}
	return name
}

func coerceScalarValue(t reflect.Type, rv reflect.Value, raw any) (any, error) {
	switch t.Kind() {
	case reflect.String:
		return coerceStringValue(rv, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return coerceIntValue(t, rv, raw)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return coerceUintValue(t, rv, raw)
	case reflect.Float32, reflect.Float64:
		return coerceFloatValue(t, rv, raw)
	case reflect.Bool:
		return coerceBoolValue(rv, raw)
	default:
		return nil, fmt.Errorf("cannot convert %T to %v", raw, t)
	}
}

func coerceStringValue(rv reflect.Value, raw any) (any, error) {
	switch rv.Kind() {
	case reflect.String:
		return rv.String(), nil
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'f', -1, 64), nil
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to string", raw)
	}
}

func coerceIntValue(t reflect.Type, rv reflect.Value, raw any) (any, error) {
	switch rv.Kind() {
	case reflect.String:
		n, err := strconv.ParseInt(rv.String(), 10, t.Bits())
		if err != nil {
			return nil, fmt.Errorf("parse int: %w", err)
		}
		return n, nil
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseInt(strconv.FormatFloat(rv.Float(), 'f', -1, 64), 10, t.Bits())
		if err != nil {
			return nil, fmt.Errorf("parse int: %w", err)
		}
		return n, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to %v", raw, t)
	}
}

func coerceUintValue(t reflect.Type, rv reflect.Value, raw any) (any, error) {
	switch rv.Kind() {
	case reflect.String:
		n, err := strconv.ParseUint(rv.String(), 10, t.Bits())
		if err != nil {
			return nil, fmt.Errorf("parse uint: %w", err)
		}
		return n, nil
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseUint(strconv.FormatFloat(rv.Float(), 'f', -1, 64), 10, t.Bits())
		if err != nil {
			return nil, fmt.Errorf("parse uint: %w", err)
		}
		return n, nil
	default:
		return nil, fmt.Errorf("cannot convert %T to %v", raw, t)
	}
}

func coerceFloatValue(t reflect.Type, rv reflect.Value, raw any) (any, error) {
	switch rv.Kind() {
	case reflect.String:
		n, err := strconv.ParseFloat(rv.String(), t.Bits())
		if err != nil {
			return nil, fmt.Errorf("parse float: %w", err)
		}
		return n, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), nil
	default:
		return nil, fmt.Errorf("cannot convert %T to %v", raw, t)
	}
}

func coerceBoolValue(rv reflect.Value, raw any) (any, error) {
	if rv.Kind() != reflect.String {
		return nil, fmt.Errorf("cannot convert %T to bool", raw)
	}
	b, err := strconv.ParseBool(rv.String())
	if err != nil {
		return nil, fmt.Errorf("parse bool: %w", err)
	}
	return b, nil
}
