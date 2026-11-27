// Package pyjson parses JSON as Python's json module reads it, for tools that
// replace Python implementations: it accepts only documents that Python reads
// the same way, and keeps objects in document order.
package pyjson

import (
	"errors"
	"strconv"
	"strings"
	"unicode/utf16"
)

// ErrUnsupported marks input that a caller leaves to the Python
// implementation, which decides what it means.
var ErrUnsupported = errors.New("unsupported input")

// Number is a JSON number literal, kept as written.
type Number string

// Object is a JSON object in document order. Objects with duplicate keys
// are unsupported: Python keeps the first position and the last value.
type Object struct {
	Keys   []string
	Values []any
}

func (object *Object) Get(key string) (any, bool) {
	for i, name := range object.Keys {
		if name == key {
			return object.Values[i], true
		}
	}
	return nil, false
}

// Lookup is dict.get(key, fallback).
func (object *Object) Lookup(key string, fallback any) any {
	if value, ok := object.Get(key); ok {
		return value
	}
	return fallback
}

// Coalesced is the doctor's _coalesced_field: the camelCase field if present,
// else the snake_case one, else fallback; fallback for a non-object.
func Coalesced(value any, camel, snake string, fallback any) any {
	object, ok := value.(*Object)
	if !ok {
		return fallback
	}
	if found, ok := object.Get(camel); ok {
		return found
	}
	if found, ok := object.Get(snake); ok {
		return found
	}
	return fallback
}

const MaxDepth = 200

// Parse parses one JSON document as Python's json.loads would, or returns
// ErrUnsupported when Python might read it differently or reject it: invalid
// JSON, NaN and Infinity, lone surrogates, duplicate keys and deep nesting.
func Parse(data []byte) (any, error) {
	parser := jsonParser{data: data}
	parser.space()
	value, err := parser.value(0)
	if err != nil {
		return nil, err
	}
	parser.space()
	if parser.pos != len(parser.data) {
		return nil, ErrUnsupported
	}
	return value, nil
}

type jsonParser struct {
	data []byte
	pos  int
}

func (parser *jsonParser) space() {
	for parser.pos < len(parser.data) {
		switch parser.data[parser.pos] {
		case ' ', '\t', '\n', '\r':
			parser.pos++
		default:
			return
		}
	}
}

func (parser *jsonParser) value(depth int) (any, error) {
	if depth > MaxDepth || parser.pos >= len(parser.data) {
		return nil, ErrUnsupported
	}
	switch c := parser.data[parser.pos]; {
	case c == '{':
		return parser.object(depth)
	case c == '[':
		return parser.array(depth)
	case c == '"':
		return parser.string()
	case c == 't':
		return true, parser.literal("true")
	case c == 'f':
		return false, parser.literal("false")
	case c == 'n':
		return nil, parser.literal("null")
	case c == '-' || c >= '0' && c <= '9':
		return parser.number()
	}
	return nil, ErrUnsupported
}

func (parser *jsonParser) literal(word string) error {
	if !strings.HasPrefix(string(parser.data[parser.pos:min(len(parser.data), parser.pos+len(word))]), word) {
		return ErrUnsupported
	}
	parser.pos += len(word)
	return nil
}

func (parser *jsonParser) object(depth int) (any, error) {
	parser.pos++
	object := &Object{}
	parser.space()
	if parser.pos < len(parser.data) && parser.data[parser.pos] == '}' {
		parser.pos++
		return object, nil
	}
	for {
		parser.space()
		if parser.pos >= len(parser.data) || parser.data[parser.pos] != '"' {
			return nil, ErrUnsupported
		}
		key, err := parser.string()
		if err != nil {
			return nil, err
		}
		if _, duplicate := object.Get(key); duplicate {
			return nil, ErrUnsupported
		}
		parser.space()
		if parser.pos >= len(parser.data) || parser.data[parser.pos] != ':' {
			return nil, ErrUnsupported
		}
		parser.pos++
		parser.space()
		value, err := parser.value(depth + 1)
		if err != nil {
			return nil, err
		}
		object.Keys = append(object.Keys, key)
		object.Values = append(object.Values, value)
		parser.space()
		if parser.pos >= len(parser.data) {
			return nil, ErrUnsupported
		}
		switch parser.data[parser.pos] {
		case ',':
			parser.pos++
		case '}':
			parser.pos++
			return object, nil
		default:
			return nil, ErrUnsupported
		}
	}
}

func (parser *jsonParser) array(depth int) (any, error) {
	parser.pos++
	values := []any{}
	parser.space()
	if parser.pos < len(parser.data) && parser.data[parser.pos] == ']' {
		parser.pos++
		return values, nil
	}
	for {
		parser.space()
		value, err := parser.value(depth + 1)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		parser.space()
		if parser.pos >= len(parser.data) {
			return nil, ErrUnsupported
		}
		switch parser.data[parser.pos] {
		case ',':
			parser.pos++
		case ']':
			parser.pos++
			return values, nil
		default:
			return nil, ErrUnsupported
		}
	}
}

func (parser *jsonParser) string() (string, error) {
	parser.pos++
	start := parser.pos
	// Fast path: no escapes.
	for parser.pos < len(parser.data) {
		c := parser.data[parser.pos]
		if c == '"' {
			text := string(parser.data[start:parser.pos])
			parser.pos++
			return text, nil
		}
		if c == '\\' {
			break
		}
		if c < 0x20 {
			return "", ErrUnsupported
		}
		parser.pos++
	}
	var builder strings.Builder
	builder.Write(parser.data[start:parser.pos])
	for parser.pos < len(parser.data) {
		c := parser.data[parser.pos]
		switch {
		case c == '"':
			parser.pos++
			return builder.String(), nil
		case c < 0x20:
			return "", ErrUnsupported
		case c != '\\':
			builder.WriteByte(c)
			parser.pos++
			continue
		}
		if parser.pos+1 >= len(parser.data) {
			return "", ErrUnsupported
		}
		escape := parser.data[parser.pos+1]
		parser.pos += 2
		switch escape {
		case '"', '\\', '/':
			builder.WriteByte(escape)
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case 'u':
			code, ok := parser.hex4()
			if !ok {
				return "", ErrUnsupported
			}
			if utf16.IsSurrogate(rune(code)) {
				if code >= 0xDC00 || parser.pos+1 >= len(parser.data) || parser.data[parser.pos] != '\\' ||
					parser.data[parser.pos+1] != 'u' {
					return "", ErrUnsupported
				}
				parser.pos += 2
				low, ok := parser.hex4()
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return "", ErrUnsupported
				}
				builder.WriteRune(utf16.DecodeRune(rune(code), rune(low)))
				continue
			}
			builder.WriteRune(rune(code))
		default:
			return "", ErrUnsupported
		}
	}
	return "", ErrUnsupported
}

func (parser *jsonParser) hex4() (int, bool) {
	if parser.pos+4 > len(parser.data) {
		return 0, false
	}
	code, err := strconv.ParseUint(string(parser.data[parser.pos:parser.pos+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	parser.pos += 4
	return int(code), true
}

func (parser *jsonParser) number() (any, error) {
	start := parser.pos
	if parser.data[parser.pos] == '-' {
		parser.pos++
	}
	digits := func() int {
		count := 0
		for parser.pos < len(parser.data) && parser.data[parser.pos] >= '0' && parser.data[parser.pos] <= '9' {
			parser.pos++
			count++
		}
		return count
	}
	if parser.pos >= len(parser.data) {
		return nil, ErrUnsupported
	}
	if parser.data[parser.pos] == '0' {
		parser.pos++
	} else if digits() == 0 {
		return nil, ErrUnsupported
	}
	if parser.pos < len(parser.data) && parser.data[parser.pos] == '.' {
		parser.pos++
		if digits() == 0 {
			return nil, ErrUnsupported
		}
	}
	if parser.pos < len(parser.data) && (parser.data[parser.pos] == 'e' || parser.data[parser.pos] == 'E') {
		parser.pos++
		if parser.pos < len(parser.data) && (parser.data[parser.pos] == '+' || parser.data[parser.pos] == '-') {
			parser.pos++
		}
		if digits() == 0 {
			return nil, ErrUnsupported
		}
	}
	return Number(parser.data[start:parser.pos]), nil
}

// Int is the value of a JSON number that Python reads as an int, small
// enough to compare; ok is false for floats. A huge int is unsupported.
func Int(value any) (int64, bool, error) {
	number, ok := value.(Number)
	if !ok || strings.ContainsAny(string(number), ".eE") {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || parsed > 1<<40 || parsed < -(1<<40) {
		return 0, false, ErrUnsupported
	}
	return parsed, true, nil
}

// Truthy is Python's bool() of a JSON value.
func Truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case Number:
		// An underflow reads as 0.0, an overflow as infinity, as in Python.
		parsed, _ := strconv.ParseFloat(string(typed), 64)
		return parsed != 0
	case []any:
		return len(typed) > 0
	case *Object:
		return len(typed.Keys) > 0
	}
	return true
}
