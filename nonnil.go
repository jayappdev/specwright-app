package main

import (
	"encoding/json"
	"reflect"
)

// Go encodes a nil slice or map as JSON null, and the UI expects [] / {}:
// on a brand-new machine (no files, runs, rules or libraries yet) a null
// list froze screens that call .length or .map on it. Everything the app
// returns to the UI goes through these helpers, which replace nil slices
// and maps with empty ones, all the way down.

// ok fills in empty lists for a value with an error.
func ok[T any](v T, err error) (T, error) {
	fill(reflect.ValueOf(&v).Elem(), 0)
	return v, err
}

// val fills in empty lists for a value.
func val[T any](v T) T {
	fill(reflect.ValueOf(&v).Elem(), 0)
	return v
}

var rawMessage = reflect.TypeOf(json.RawMessage(nil))

func fill(v reflect.Value, depth int) {
	if depth > 16 || !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			fill(v.Elem(), depth+1)
		}
	case reflect.Interface:
		if !v.IsNil() && v.Elem().Kind() == reflect.Pointer {
			fill(v.Elem(), depth+1)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanSet() {
				fill(f, depth+1)
			}
		}
	case reflect.Slice:
		// []byte and json.RawMessage encode as strings / raw JSON; leave them
		if v.Type() == rawMessage || v.Type().Elem().Kind() == reflect.Uint8 {
			return
		}
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			fill(v.Index(i), depth+1)
		}
	case reflect.Map:
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.MakeMap(v.Type()))
		}
	}
}
