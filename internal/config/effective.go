package config

import (
	"encoding"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Effective returns the configuration as a tree keyed by the config file's
// key names, for display. Secrets are redacted and durations are written the
// way the config file accepts them. Unset optional values (nil pointers,
// slices and maps) are left out.
//
// It walks the struct instead of keeping koanf's raw map, because the raw map
// holds secrets in clear text and lacks the defaults applied in resolve.
func (c *Config) Effective() map[string]any {
	m, _ := effectiveValue(reflect.ValueOf(*c)).(map[string]any)

	return m
}

func effectiveValue(v reflect.Value) any {
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}

	if v.CanInterface() {
		switch x := v.Interface().(type) {
		case Secret:
			return x.String()
		case time.Duration:
			return x.String()
		case encoding.TextMarshaler:
			text, err := x.MarshalText()
			if err != nil {
				return fmt.Sprintf("<%v>", err)
			}

			return string(text)
		}
	}

	switch v.Kind() { //nolint:exhaustive // every other kind is a scalar
	case reflect.Pointer, reflect.Interface:
		return effectiveValue(v.Elem())
	case reflect.Struct:
		return effectiveStruct(v)
	case reflect.Slice:
		// A nil slice is unset; an empty one can be meaningful, e.g.
		// "topics: []" clears the topics.
		if v.IsNil() {
			return nil
		}
		out := make([]any, v.Len())
		for i := range v.Len() {
			out[i] = effectiveValue(v.Index(i))
		}

		return out
	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		for iter := v.MapRange(); iter.Next(); {
			out[fmt.Sprint(iter.Key().Interface())] = effectiveValue(iter.Value())
		}

		return out
	case reflect.Func, reflect.Chan:
		return nil
	default:
		return v.Interface()
	}
}

func effectiveStruct(v reflect.Value) map[string]any {
	out := make(map[string]any)
	for i := range v.NumField() {
		field := v.Type().Field(i)
		name, _, _ := strings.Cut(field.Tag.Get("koanf"), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}
		if value := effectiveValue(v.Field(i)); value != nil {
			out[name] = value
		}
	}

	return out
}
