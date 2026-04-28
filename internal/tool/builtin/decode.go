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
	v, err := decodeReflect(reflect.TypeOf(zero), raw)
	if err != nil {
		return zero, err
	}
	return v.Interface().(T), nil
}

func decodeReflect(t reflect.Type, raw map[string]any) (reflect.Value, error) {
	v := reflect.New(t).Elem()

	known := make(map[string]int, t.NumField())
	for i := range t.NumField() {
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

	if fv.Kind() == reflect.Ptr {
		elem := reflect.New(fv.Type().Elem())
		if err := setField(elem.Elem(), raw); err != nil {
			return err
		}
		fv.Set(elem)
		return nil
	}

	if rv.Type() == fv.Type() {
		fv.Set(rv)
		return nil
	}

	if fv.Kind() == reflect.Struct {
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

	switch fv.Kind() {
	case reflect.String:
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
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch rv.Kind() {
		case reflect.String:
			n, err := strconv.ParseInt(rv.String(), 10, fv.Type().Bits())
			if err != nil {
				return fmt.Errorf("parse int: %w", err)
			}
			fv.SetInt(n)
			return nil
		case reflect.Float32, reflect.Float64:
			fv.SetInt(int64(rv.Float()))
			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch rv.Kind() {
		case reflect.String:
			n, err := strconv.ParseUint(rv.String(), 10, fv.Type().Bits())
			if err != nil {
				return fmt.Errorf("parse uint: %w", err)
			}
			fv.SetUint(n)
			return nil
		case reflect.Float32, reflect.Float64:
			fv.SetUint(uint64(rv.Float()))
			return nil
		}
	case reflect.Float32, reflect.Float64:
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
		}
	case reflect.Bool:
		switch rv.Kind() {
		case reflect.String:
			b, err := strconv.ParseBool(rv.String())
			if err != nil {
				return fmt.Errorf("parse bool: %w", err)
			}
			fv.SetBool(b)
			return nil
		}
	}

	return fmt.Errorf("cannot convert %T to %v", raw, fv.Type())
}
