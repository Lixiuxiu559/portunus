package configtransfer

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"
)

// DecodeStrict checks encoding and the complete JSON shape before binding. Go's
// normal decoder accepts duplicate/case-insensitive keys and replaces bad Unicode.
func DecodeStrict(data []byte, target any) error {
	if !utf8.Valid(data) || !validJSONStrings(data) {
		return NewError("INVALID_INPUT", "validate")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := readJSONValue(d, 1)
	if err != nil {
		return NewError("INVALID_INPUT", "validate")
	}
	if _, err = d.Token(); err != io.EOF {
		return NewError("INVALID_INPUT", "validate")
	}
	t := reflect.TypeOf(target)
	if t == nil || t.Kind() != reflect.Pointer {
		return NewError("INVALID_INPUT", "validate")
	}
	if path, ok := matchesJSON(v, t.Elem(), ""); !ok {
		e := NewError("INVALID_INPUT", "validate")
		if path == "" {
			path = "configuration"
		}
		e.Field = path
		return e
	}
	v = nil // release the temporary shape tree before binding the typed payload
	if err := json.Unmarshal(data, target); err != nil {
		return NewError("INVALID_INPUT", "validate")
	}
	return nil
}

func readJSONValue(d *json.Decoder, depth int) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, composite := t.(json.Delim)
	if !composite {
		return t, nil
	}
	if depth > 16 {
		return nil, io.ErrUnexpectedEOF
	}
	switch delim {
	case '{':
		m := map[string]any{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, io.ErrUnexpectedEOF
			}
			if _, exists := m[key]; exists {
				return nil, io.ErrUnexpectedEOF
			}
			v, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = v
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, io.ErrUnexpectedEOF
		}
		return m, nil
	case '[':
		a := []any{}
		for d.More() {
			v, err := readJSONValue(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, io.ErrUnexpectedEOF
		}
		return a, nil
	}
	return nil, io.ErrUnexpectedEOF
}

func matchesJSON(v any, t reflect.Type, path string) (string, bool) {
	if t.Kind() == reflect.Pointer {
		if v == nil {
			return "", true
		}
		return matchesJSON(v, t.Elem(), path)
	}
	if v == nil {
		return path, false
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return path, false
		}
		fields := 0
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" {
				continue
			}
			key := strings.Split(f.Tag.Get("json"), ",")[0]
			if key == "-" {
				continue
			}
			if key == "" {
				key = f.Name
			}
			fields++
			fieldPath := key
			if path != "" {
				fieldPath = path + "." + key
			}
			value, exists := m[key]
			if !exists {
				return fieldPath, false
			}
			if mismatch, ok := matchesJSON(value, f.Type, fieldPath); !ok {
				return mismatch, false
			}
		}
		return path, len(m) == fields
	case reflect.Slice:
		a, ok := v.([]any)
		if !ok {
			return path, false
		}
		for i, item := range a {
			if mismatch, ok := matchesJSON(item, t.Elem(), path+"["+strconv.Itoa(i)+"]"); !ok {
				return mismatch, false
			}
		}
		return "", true
	case reflect.String:
		_, ok := v.(string)
		return path, ok
	case reflect.Bool:
		_, ok := v.(bool)
		return path, ok
	case reflect.Int, reflect.Int64, reflect.Int32:
		n, ok := v.(json.Number)
		if !ok {
			return path, false
		}
		_, err := strconv.ParseInt(string(n), 10, t.Bits())
		return path, err == nil
	}
	return path, false
}

// Every escaped UTF-16 surrogate must be a proper pair; no lossy U+FFFD rewrite.
func validJSONStrings(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '"' {
			continue
		}
		i++
		closed := false
		for i < len(data) {
			b := data[i]
			if b == '"' {
				closed = true
				break
			}
			if b < 0x20 {
				return false
			}
			if b != '\\' {
				i++
				continue
			}
			i++
			if i >= len(data) {
				return false
			}
			if data[i] != 'u' {
				i++
				continue
			}
			if i+4 >= len(data) {
				return false
			}
			u, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
			if err != nil {
				return false
			}
			i += 5
			if u >= 0xDC00 && u <= 0xDFFF {
				return false
			}
			if u >= 0xD800 && u <= 0xDBFF {
				if i+5 >= len(data) || data[i] != '\\' || data[i+1] != 'u' {
					return false
				}
				lo, err := strconv.ParseUint(string(data[i+2:i+6]), 16, 16)
				if err != nil || lo < 0xDC00 || lo > 0xDFFF {
					return false
				}
				i += 6
			}
		}
		if !closed {
			return false
		}
	}
	return true
}
