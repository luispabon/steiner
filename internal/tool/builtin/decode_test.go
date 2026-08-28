package builtin

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestDecodeInputParity(t *testing.T) {
	type signedByteInput struct {
		Value int8 `json:"value"`
	}
	type unsignedByteInput struct {
		Value uint8 `json:"value"`
	}

	tests := []struct {
		name string
		run  func(*testing.T)
	}{
		{
			name: "ReadInput",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{
					"path":   "test.txt",
					"offset": 5,
					"limit":  100,
				})
			},
		},
		{
			name: "MutateOperation",
			run: func(t *testing.T) {
				compareDecodeParity[MutateOperation](t, map[string]any{
					"type":        "replace",
					"path":        "test.txt",
					"old_string":  "foo",
					"new_string":  "bar",
					"replace_all": true,
				})
			},
		},
		{
			name: "MutateInputNestedCoercions",
			run: func(t *testing.T) {
				compareDecodeParity[MutateInput](t, map[string]any{
					"operations": []any{
						map[string]any{
							"type":           "replace",
							"path":           "test.txt",
							"old_string":     "foo",
							"new_string":     float64(170),
							"replace_all":    true,
							"assert_present": []any{"alpha", float64(2)},
						},
						map[string]any{
							"type":          "write",
							"path":          "test.txt",
							"content":       float64(42),
							"file_hash":     "hash",
							"assert_absent": []any{"beta", float64(3)},
						},
					},
				})
			},
		},
		{
			name: "unknown fields",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{
					"path":          "test.txt",
					"unknown_field": "value",
				})
			},
		},
		{
			name: "unknown field name is reported",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{
					"path": "test.txt",
					"op":   "read",
				})
			},
		},
		{
			name: "invalid output_mode strings",
			run: func(t *testing.T) {
				compareDecodeParity[GrepInput](t, map[string]any{
					"pattern":     "foo",
					"output_mode": "invalid_value",
				})
			},
		},
		{
			name: "missing required fields",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{})
			},
		},
		{
			name: "defaults are not applied",
			run: func(t *testing.T) {
				compareDecodeParity[GlobInput](t, map[string]any{
					"pattern": "*.go",
				})
			},
		},
		{
			name: "BashInput",
			run: func(t *testing.T) {
				compareDecodeParity[BashInput](t, map[string]any{
					"command":          "echo hello",
					"cwd":              "/tmp",
					"timeout_seconds":  60,
					"max_output_chars": 50000,
				})
			},
		},
		{
			name: "float64 to string",
			run: func(t *testing.T) {
				compareDecodeParity[MutateOperation](t, map[string]any{
					"type":       "replace",
					"path":       "test.txt",
					"old_string": "foo",
					"new_string": float64(170),
				})
			},
		},
		{
			name: "float64 to int",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{
					"path":   "test.txt",
					"offset": float64(5),
					"limit":  float64(100),
				})
			},
		},
		{
			name: "signed int coercions",
			run: func(t *testing.T) {
				compareDecodeParity[signedByteInput](t, map[string]any{
					"value": float64(5),
				})
				compareDecodeParity[signedByteInput](t, map[string]any{
					"value": float64(3.9),
				})
				compareDecodeParity[signedByteInput](t, map[string]any{
					"value": float64(128),
				})
			},
		},
		{
			name: "unsigned int coercions",
			run: func(t *testing.T) {
				compareDecodeParity[unsignedByteInput](t, map[string]any{
					"value": float64(-1),
				})
				compareDecodeParity[unsignedByteInput](t, map[string]any{
					"value": float64(256),
				})
			},
		},
		{
			name: "string to int",
			run: func(t *testing.T) {
				compareDecodeParity[ReadInput](t, map[string]any{
					"path":   "test.txt",
					"offset": "5",
				})
			},
		},
		{
			name: "LSInput",
			run: func(t *testing.T) {
				compareDecodeParity[LSInput](t, map[string]any{
					"path":      ".",
					"recursive": true,
					"limit":     50,
					"offset":    10,
				})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

func compareDecodeParity[T any](t *testing.T, raw map[string]any) {
	t.Helper()

	legacy, legacyErr := decodeInputLegacy[T](raw)
	roundTrip, roundTripErr := decodeInput[T](raw)

	switch {
	case legacyErr == nil && roundTripErr == nil:
		if !reflect.DeepEqual(legacy, roundTrip) {
			t.Fatalf("legacy decode = %#v, round-trip decode = %#v", legacy, roundTrip)
		}
	case legacyErr == nil || roundTripErr == nil:
		t.Fatalf("legacy err = %v, round-trip err = %v", legacyErr, roundTripErr)
	default:
		if legacyErr.Error() != roundTripErr.Error() {
			t.Fatalf("legacy err = %q, round-trip err = %q", legacyErr.Error(), roundTripErr.Error())
		}
	}
}

func decodeInputLegacy[T any](raw map[string]any) (T, error) {
	var zero T

	v, err := decodeReflectLegacy(reflect.TypeFor[T](), raw)
	if err != nil {
		return zero, err
	}
	return v.Interface().(T), nil
}

func decodeReflectLegacy(t reflect.Type, raw map[string]any) (reflect.Value, error) {
	if t == nil {
		return reflect.Value{}, fmt.Errorf("decode input: nil type")
	}
	if t.Kind() == reflect.Pointer {
		decoded, err := decodeReflectLegacy(t.Elem(), raw)
		if err != nil {
			return reflect.Value{}, err
		}
		ptr := reflect.New(t.Elem())
		ptr.Elem().Set(decoded)
		return ptr, nil
	}
	if t.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("decode input: unsupported type %v", t)
	}

	v := reflect.New(t).Elem()

	known := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		name := jsonFieldName(t.Field(i))
		if name != "" {
			known[name] = i
		}
	}

	for jsonName, idx := range known {
		rv, ok := raw[jsonName]
		if !ok {
			continue
		}
		if err := setFieldLegacy(v.Field(idx), rv); err != nil {
			return v, fmt.Errorf("field %q: %w", jsonName, err)
		}
	}

	for key := range raw {
		if _, ok := known[key]; !ok {
			validFields := make([]string, 0, len(known))
			for name := range known {
				validFields = append(validFields, name)
			}
			sort.Strings(validFields)
			return v, fmt.Errorf("decode input: unknown field %q; valid fields: %s", key, strings.Join(validFields, ", "))
		}
	}

	return v, nil
}

func setFieldLegacy(fv reflect.Value, raw any) error {
	if raw == nil {
		return nil
	}

	rv := reflect.ValueOf(raw)

	if fv.Kind() == reflect.Pointer {
		return setPointerFieldLegacy(fv, raw)
	}

	if rv.Type().AssignableTo(fv.Type()) {
		fv.Set(rv)
		return nil
	}

	if fv.Kind() == reflect.Struct {
		return setStructFieldLegacy(fv, raw)
	}

	if fv.Kind() == reflect.Slice {
		return setSliceFieldLegacy(fv, raw)
	}

	return setScalarFieldLegacy(fv, rv, raw)
}

func setPointerFieldLegacy(fv reflect.Value, raw any) error {
	elem := reflect.New(fv.Type().Elem())
	if err := setFieldLegacy(elem.Elem(), raw); err != nil {
		return err
	}
	fv.Set(elem)
	return nil
}

func setStructFieldLegacy(fv reflect.Value, raw any) error {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("expected object for struct field, got %T", raw)
	}
	nested, err := decodeReflectLegacy(fv.Type(), rawMap)
	if err != nil {
		return err
	}
	fv.Set(nested)
	return nil
}

func setSliceFieldLegacy(fv reflect.Value, raw any) error {
	rawSlice, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("expected array for slice field, got %T", raw)
	}
	elemType := fv.Type().Elem()
	slice := reflect.MakeSlice(fv.Type(), 0, len(rawSlice))
	for i, item := range rawSlice {
		elem := reflect.New(elemType).Elem()
		if elemType.Kind() == reflect.Struct {
			itemMap, ok := item.(map[string]any)
			if !ok {
				return fmt.Errorf("expected object for slice element %d, got %T", i, item)
			}
			nested, err := decodeReflectLegacy(elemType, itemMap)
			if err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
			elem = nested
		} else {
			if err := setFieldLegacy(elem, item); err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
		}
		slice = reflect.Append(slice, elem)
	}
	fv.Set(slice)
	return nil
}

func setScalarFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	switch fv.Kind() {
	case reflect.String:
		return setStringFieldLegacy(fv, rv, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return setIntFieldLegacy(fv, rv, raw)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return setUintFieldLegacy(fv, rv, raw)
	case reflect.Float32, reflect.Float64:
		return setFloatFieldLegacy(fv, rv, raw)
	case reflect.Bool:
		return setBoolFieldLegacy(fv, rv, raw)
	default:
		return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
	}
}

func setStringFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	switch rv.Kind() {
	case reflect.Float32, reflect.Float64:
		fv.SetString(strconv.FormatFloat(rv.Float(), 'f', -1, 64))
		return nil
	case reflect.Bool:
		fv.SetString(strconv.FormatBool(rv.Bool()))
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fv.SetString(strconv.FormatInt(rv.Int(), 10))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fv.SetString(strconv.FormatUint(rv.Uint(), 10))
		return nil
	default:
		return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
	}
}

func setIntFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	n, err := parseScalarValueLegacy(
		rv,
		raw,
		func(s string) (int64, error) {
			return strconv.ParseInt(s, 10, fv.Type().Bits())
		},
		func(f float64) (int64, error) {
			return strconv.ParseInt(strconv.FormatFloat(f, 'f', -1, 64), 10, fv.Type().Bits())
		},
	)
	if err != nil {
		return fmt.Errorf("parse int: %w", err)
	}
	fv.SetInt(n)
	return nil
}

func setUintFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	n, err := parseScalarValueLegacy(
		rv,
		raw,
		func(s string) (uint64, error) {
			return strconv.ParseUint(s, 10, fv.Type().Bits())
		},
		func(f float64) (uint64, error) {
			return strconv.ParseUint(strconv.FormatFloat(f, 'f', -1, 64), 10, fv.Type().Bits())
		},
	)
	if err != nil {
		return fmt.Errorf("parse uint: %w", err)
	}
	fv.SetUint(n)
	return nil
}

func setFloatFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	switch rv.Kind() {
	case reflect.String:
		n, err := strconv.ParseFloat(rv.String(), fv.Type().Bits())
		if err != nil {
			return fmt.Errorf("parse float: %w", err)
		}
		fv.SetFloat(n)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		fv.SetFloat(float64(rv.Int()))
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		fv.SetFloat(float64(rv.Uint()))
		return nil
	default:
		return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
	}
}

func setBoolFieldLegacy(fv reflect.Value, rv reflect.Value, raw any) error {
	if rv.Kind() != reflect.String {
		return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
	}
	b, err := strconv.ParseBool(rv.String())
	if err != nil {
		return fmt.Errorf("parse bool: %w", err)
	}
	fv.SetBool(b)
	return nil
}

func parseScalarValueLegacy[T any](rv reflect.Value, raw any, fromString func(string) (T, error), fromFloat func(float64) (T, error)) (T, error) {
	switch rv.Kind() {
	case reflect.String:
		return fromString(rv.String())
	case reflect.Float32, reflect.Float64:
		return fromFloat(rv.Float())
	default:
		var zero T
		return zero, fmt.Errorf("cannot convert %T", raw)
	}
}

func TestDecodeInput(t *testing.T) {
	type signedByteInput struct {
		Value int8 `json:"value"`
	}
	type unsignedByteInput struct {
		Value uint8 `json:"value"`
	}

	t.Run("valid ReadInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": 5,
			"limit":  100,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "test.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "test.txt")
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
		if result.Limit != 100 {
			t.Errorf("Limit = %d, want %d", result.Limit, 100)
		}
	})

	t.Run("valid MutateOperation decodes correctly", func(t *testing.T) {
		result, err := decodeInput[MutateOperation](map[string]any{
			"type":        "replace",
			"path":        "test.txt",
			"old_string":  "foo",
			"new_string":  "bar",
			"replace_all": true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "test.txt" {
			t.Errorf("Path = %q, want %q", result.Path, "test.txt")
		}
		if result.OldString != "foo" {
			t.Errorf("OldString = %q, want %q", result.OldString, "foo")
		}
		if result.NewString != "bar" {
			t.Errorf("NewString = %q, want %q", result.NewString, "bar")
		}
		if !result.ReplaceAll {
			t.Error("ReplaceAll should be true")
		}
	})

	t.Run("unknown fields are rejected", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path":          "test.txt",
			"unknown_field": "value",
		})
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
	})

	t.Run("unknown_field_error_contains_field_name", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "test.txt",
			"op":   "read",
		})
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
		if !strings.Contains(err.Error(), "op") {
			t.Fatalf("error = %q, want to contain %q", err.Error(), "op")
		}
	})

	t.Run("unknown_field_error_lists_valid_fields", func(t *testing.T) {
		_, err := decodeInput[ReadInput](map[string]any{
			"path": "test.txt",
			"op":   "read",
		})
		if err == nil {
			t.Fatal("expected error for unknown field")
		}
		if !strings.Contains(err.Error(), "valid fields:") {
			t.Fatalf("error = %q, want to contain %q", err.Error(), "valid fields:")
		}
		if !strings.Contains(err.Error(), "path") {
			t.Fatalf("error = %q, want to contain %q", err.Error(), "path")
		}
	})

	t.Run("invalid output_mode strings accepted at decode level", func(t *testing.T) {
		result, err := decodeInput[GrepInput](map[string]any{
			"pattern":     "foo",
			"output_mode": "invalid_value",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.OutputMode != "invalid_value" {
			t.Errorf("OutputMode = %q, want %q", result.OutputMode, "invalid_value")
		}
	})

	t.Run("missing required fields are accepted at decode level", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "" {
			t.Errorf("Path = %q, want empty string", result.Path)
		}
	})

	t.Run("defaults are not applied by decode", func(t *testing.T) {
		result, err := decodeInput[GlobInput](map[string]any{
			"pattern": "*.go",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Limit != 0 {
			t.Errorf("Limit = %d, want 0 (default not applied)", result.Limit)
		}
		if result.Offset != 0 {
			t.Errorf("Offset = %d, want 0 (default not applied)", result.Offset)
		}
	})

	t.Run("valid BashInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[BashInput](map[string]any{
			"command":          "echo hello",
			"cwd":              "/tmp",
			"timeout_seconds":  60,
			"max_output_chars": 50000,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Command != "echo hello" {
			t.Errorf("Command = %q, want %q", result.Command, "echo hello")
		}
		if result.CWD != "/tmp" {
			t.Errorf("CWD = %q, want %q", result.CWD, "/tmp")
		}
	})

	t.Run("float64 coerced to string", func(t *testing.T) {
		result, err := decodeInput[MutateOperation](map[string]any{
			"type":       "replace",
			"path":       "test.txt",
			"old_string": "foo",
			"new_string": float64(170),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.NewString != "170" {
			t.Errorf("NewString = %q, want %q", result.NewString, "170")
		}
	})

	t.Run("float64 coerced to int", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": float64(5),
			"limit":  float64(100),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
		if result.Limit != 100 {
			t.Errorf("Limit = %d, want %d", result.Limit, 100)
		}
	})

	t.Run("integral float accepted for signed ints", func(t *testing.T) {
		result, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(5),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Value != 5 {
			t.Errorf("Value = %d, want %d", result.Value, 5)
		}
	})

	t.Run("fractional float rejected for signed ints", func(t *testing.T) {
		_, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(3.9),
		})
		if err == nil {
			t.Fatal("expected error for fractional float")
		}
	})

	t.Run("negative float rejected for unsigned ints", func(t *testing.T) {
		_, err := decodeInput[unsignedByteInput](map[string]any{
			"value": float64(-1),
		})
		if err == nil {
			t.Fatal("expected error for negative float")
		}
	})

	t.Run("out of range float rejected for signed ints", func(t *testing.T) {
		_, err := decodeInput[signedByteInput](map[string]any{
			"value": float64(128),
		})
		if err == nil {
			t.Fatal("expected error for out of range float")
		}
	})

	t.Run("out of range float rejected for unsigned ints", func(t *testing.T) {
		_, err := decodeInput[unsignedByteInput](map[string]any{
			"value": float64(256),
		})
		if err == nil {
			t.Fatal("expected error for out of range float")
		}
	})

	t.Run("string coerced to int", func(t *testing.T) {
		result, err := decodeInput[ReadInput](map[string]any{
			"path":   "test.txt",
			"offset": "5",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Offset != 5 {
			t.Errorf("Offset = %d, want %d", result.Offset, 5)
		}
	})

	t.Run("valid LSInput decodes correctly", func(t *testing.T) {
		result, err := decodeInput[LSInput](map[string]any{
			"path":      ".",
			"recursive": true,
			"limit":     50,
			"offset":    10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Path != "." {
			t.Errorf("Path = %q, want %q", result.Path, ".")
		}
		if !result.Recursive {
			t.Error("Recursive should be true")
		}
		if result.Limit != 50 {
			t.Errorf("Limit = %d, want %d", result.Limit, 50)
		}
		if result.Offset != 10 {
			t.Errorf("Offset = %d, want %d", result.Offset, 10)
		}
	})
}
