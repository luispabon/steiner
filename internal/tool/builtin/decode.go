package builtin

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// decodeInput decodes a raw map into a typed input struct T using strict
// decoding that rejects unknown fields.
//
// We do not use json.Unmarshal here because the raw map may contain float64
// values for fields that are strings in the struct (e.g., an LLM sends
// new_string: 170 instead of "170"). A reflection-based converter lets us
// coerce these mismatches instead of failing.
func decodeInput[T any](raw map[string]any) (T, error) {
	var zero T
	v, err := decodeReflect(reflect.TypeFor[T](), raw)
	if err != nil {
		return zero, err
	}
	return v.Interface().(T), nil
}

func decodeReflect(t reflect.Type, raw map[string]any) (reflect.Value, error) {
	if t == nil {
		return reflect.Value{}, fmt.Errorf("decode input: nil type")
	}
	if t.Kind() == 22 {
		decoded, err := decodeReflect(t.Elem(), raw)
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
		if err := setField(v.Field(idx), rv); err != nil {
			return v, fmt.Errorf("field %q: %w", jsonName, err)
		}
	}

	for key := range raw {
		if _, ok := known[key]; !ok {
			return v, fmt.Errorf("decode input: unknown field %q", key)
		}
	}

	return v, nil
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

func setField(fv reflect.Value, raw any) error {
	if raw == nil {
		return nil
	}

	rv := reflect.ValueOf(raw)

	if fv.Kind() == 22 {
		return setPointerField(fv, raw)
	}

	if rv.Type().AssignableTo(fv.Type()) {
		fv.Set(rv)
		return nil
	}

	if fv.Kind() == reflect.Struct {
		return setStructField(fv, raw)
	}

	if fv.Kind() == reflect.Slice {
		return setSliceField(fv, raw)
	}

	return setScalarField(fv, rv, raw)
}

func setPointerField(fv reflect.Value, raw any) error {
	elem := reflect.New(fv.Type().Elem())
	if err := setField(elem.Elem(), raw); err != nil {
		return err
	}
	fv.Set(elem)
	return nil
}

func setStructField(fv reflect.Value, raw any) error {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("expected object for struct field, got %T", raw)
	}
	nested, err := decodeReflect(fv.Type(), rawMap)
	if err != nil {
		return err
	}
	fv.Set(nested)
	return nil
}

func setSliceField(fv reflect.Value, raw any) error {
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
			nested, err := decodeReflect(elemType, itemMap)
			if err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
			elem = nested
		} else {
			if err := setField(elem, item); err != nil {
				return fmt.Errorf("element %d: %w", i, err)
			}
		}
		slice = reflect.Append(slice, elem)
	}
	fv.Set(slice)
	return nil
}

func setScalarField(fv reflect.Value, rv reflect.Value, raw any) error {
	switch fv.Kind() {
	case reflect.String:
		return setStringField(fv, rv, raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return setIntField(fv, rv, raw)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return setUintField(fv, rv, raw)
	case reflect.Float32, reflect.Float64:
		return setFloatField(fv, rv, raw)
	case reflect.Bool:
		return setBoolField(fv, rv, raw)
	default:
		return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
	}
}

func setStringField(fv reflect.Value, rv reflect.Value, raw any) error {
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

func setIntField(fv reflect.Value, rv reflect.Value, raw any) error {
	n, err := parseScalarValue(
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

func setUintField(fv reflect.Value, rv reflect.Value, raw any) error {
	n, err := parseScalarValue(
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

func setFloatField(fv reflect.Value, rv reflect.Value, raw any) error {
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

func setBoolField(fv reflect.Value, rv reflect.Value, raw any) error {
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

func parseScalarValue[T any](rv reflect.Value, raw any, fromString func(string) (T, error), fromFloat func(float64) (T, error)) (T, error) {
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
