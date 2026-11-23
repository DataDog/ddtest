package bazelinspect

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// errUnsupported marks input that the BEP evaluation leaves to the Python
// implementation, which decides what it means.
var errUnsupported = errors.New("unsupported BEP input")

// jsonNumber is a JSON number literal, kept as written.
type jsonNumber string

// jsonObject is a JSON object in document order. Objects with duplicate keys
// are unsupported: Python keeps the first position and the last value.
type jsonObject struct {
	keys   []string
	values []any
}

func (object *jsonObject) get(key string) (any, bool) {
	for i, name := range object.keys {
		if name == key {
			return object.values[i], true
		}
	}
	return nil, false
}

// lookup is dict.get(key, fallback).
func (object *jsonObject) lookup(key string, fallback any) any {
	if value, ok := object.get(key); ok {
		return value
	}
	return fallback
}

// coalesced is the doctor's _coalesced_field: the camelCase field if present,
// else the snake_case one, else fallback; fallback for a non-object.
func coalesced(value any, camel, snake string, fallback any) any {
	object, ok := value.(*jsonObject)
	if !ok {
		return fallback
	}
	if found, ok := object.get(camel); ok {
		return found
	}
	if found, ok := object.get(snake); ok {
		return found
	}
	return fallback
}

const maxJSONDepth = 200

// parseJSON parses one JSON document as Python's json.loads would, or returns
// errUnsupported when Python might read it differently or reject it: invalid
// JSON, NaN and Infinity, lone surrogates, duplicate keys and deep nesting.
func parseJSON(data []byte) (any, error) {
	parser := jsonParser{data: data}
	parser.space()
	value, err := parser.value(0)
	if err != nil {
		return nil, err
	}
	parser.space()
	if parser.pos != len(parser.data) {
		return nil, errUnsupported
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
	if depth > maxJSONDepth || parser.pos >= len(parser.data) {
		return nil, errUnsupported
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
	return nil, errUnsupported
}

func (parser *jsonParser) literal(word string) error {
	if !strings.HasPrefix(string(parser.data[parser.pos:min(len(parser.data), parser.pos+len(word))]), word) {
		return errUnsupported
	}
	parser.pos += len(word)
	return nil
}

func (parser *jsonParser) object(depth int) (any, error) {
	parser.pos++
	object := &jsonObject{}
	parser.space()
	if parser.pos < len(parser.data) && parser.data[parser.pos] == '}' {
		parser.pos++
		return object, nil
	}
	for {
		parser.space()
		if parser.pos >= len(parser.data) || parser.data[parser.pos] != '"' {
			return nil, errUnsupported
		}
		key, err := parser.string()
		if err != nil {
			return nil, err
		}
		if _, duplicate := object.get(key); duplicate {
			return nil, errUnsupported
		}
		parser.space()
		if parser.pos >= len(parser.data) || parser.data[parser.pos] != ':' {
			return nil, errUnsupported
		}
		parser.pos++
		parser.space()
		value, err := parser.value(depth + 1)
		if err != nil {
			return nil, err
		}
		object.keys = append(object.keys, key)
		object.values = append(object.values, value)
		parser.space()
		if parser.pos >= len(parser.data) {
			return nil, errUnsupported
		}
		switch parser.data[parser.pos] {
		case ',':
			parser.pos++
		case '}':
			parser.pos++
			return object, nil
		default:
			return nil, errUnsupported
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
			return nil, errUnsupported
		}
		switch parser.data[parser.pos] {
		case ',':
			parser.pos++
		case ']':
			parser.pos++
			return values, nil
		default:
			return nil, errUnsupported
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
			return "", errUnsupported
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
			return "", errUnsupported
		case c != '\\':
			builder.WriteByte(c)
			parser.pos++
			continue
		}
		if parser.pos+1 >= len(parser.data) {
			return "", errUnsupported
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
				return "", errUnsupported
			}
			if utf16.IsSurrogate(rune(code)) {
				if code >= 0xDC00 || parser.pos+1 >= len(parser.data) || parser.data[parser.pos] != '\\' ||
					parser.data[parser.pos+1] != 'u' {
					return "", errUnsupported
				}
				parser.pos += 2
				low, ok := parser.hex4()
				if !ok || low < 0xDC00 || low > 0xDFFF {
					return "", errUnsupported
				}
				builder.WriteRune(utf16.DecodeRune(rune(code), rune(low)))
				continue
			}
			builder.WriteRune(rune(code))
		default:
			return "", errUnsupported
		}
	}
	return "", errUnsupported
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
		return nil, errUnsupported
	}
	if parser.data[parser.pos] == '0' {
		parser.pos++
	} else if digits() == 0 {
		return nil, errUnsupported
	}
	if parser.pos < len(parser.data) && parser.data[parser.pos] == '.' {
		parser.pos++
		if digits() == 0 {
			return nil, errUnsupported
		}
	}
	if parser.pos < len(parser.data) && (parser.data[parser.pos] == 'e' || parser.data[parser.pos] == 'E') {
		parser.pos++
		if parser.pos < len(parser.data) && (parser.data[parser.pos] == '+' || parser.data[parser.pos] == '-') {
			parser.pos++
		}
		if digits() == 0 {
			return nil, errUnsupported
		}
	}
	return jsonNumber(parser.data[start:parser.pos]), nil
}

// pythonInt is the value of a JSON number that Python reads as an int, small
// enough to compare; ok is false for floats. A huge int is unsupported.
func pythonInt(value any) (int64, bool, error) {
	number, ok := value.(jsonNumber)
	if !ok || strings.ContainsAny(string(number), ".eE") {
		return 0, false, nil
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || parsed > 1<<40 || parsed < -(1<<40) {
		return 0, false, errUnsupported
	}
	return parsed, true, nil
}

// truthy is Python's bool() of a JSON value.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case jsonNumber:
		// An underflow reads as 0.0, an overflow as infinity, as in Python.
		parsed, _ := strconv.ParseFloat(string(typed), 64)
		return parsed != 0
	case []any:
		return len(typed) > 0
	case *jsonObject:
		return len(typed.keys) > 0
	}
	return true
}

// normalizedEventID is _normalized_event_id in bep_execution.py: object keys
// in snake_case, recursively. Non-ASCII keys and keys that collide once
// normalized are unsupported.
func normalizedEventID(value any) (any, error) {
	switch typed := value.(type) {
	case *jsonObject:
		normalized := &jsonObject{}
		for i, key := range typed.keys {
			name, err := snakeKey(key)
			if err != nil {
				return nil, err
			}
			if _, collision := normalized.get(name); collision {
				return nil, errUnsupported
			}
			item, err := normalizedEventID(typed.values[i])
			if err != nil {
				return nil, err
			}
			normalized.keys = append(normalized.keys, name)
			normalized.values = append(normalized.values, item)
		}
		return normalized, nil
	case []any:
		items := make([]any, len(typed))
		for i, item := range typed {
			normalized, err := normalizedEventID(item)
			if err != nil {
				return nil, err
			}
			items[i] = normalized
		}
		return items, nil
	}
	return value, nil
}

// snakeKey is re.sub(r"(?<!^)(?=[A-Z])", "_", key).lower() for ASCII keys.
func snakeKey(key string) (string, error) {
	var builder strings.Builder
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= utf8.RuneSelf {
			return "", errUnsupported
		}
		if c >= 'A' && c <= 'Z' {
			if i > 0 {
				builder.WriteByte('_')
			}
			c += 'a' - 'A'
		}
		builder.WriteByte(c)
	}
	return builder.String(), nil
}

// canonicalJSON is json.dumps(value, sort_keys=True, separators=(",", ":"))
// for values without floats or non-ASCII text, which are unsupported.
func canonicalJSON(value any) (string, error) {
	var builder strings.Builder
	if err := writeCanonical(&builder, value); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func writeCanonical(builder *strings.Builder, value any) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case jsonNumber:
		integer, ok, err := pythonInt(typed)
		if err != nil || !ok {
			return errUnsupported
		}
		builder.WriteString(strconv.FormatInt(integer, 10))
	case string:
		return writeCanonicalString(builder, typed)
	case []any:
		builder.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				builder.WriteByte(',')
			}
			if err := writeCanonical(builder, item); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
	case *jsonObject:
		order := make([]int, len(typed.keys))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return typed.keys[order[a]] < typed.keys[order[b]] })
		builder.WriteByte('{')
		for n, i := range order {
			if n > 0 {
				builder.WriteByte(',')
			}
			if err := writeCanonicalString(builder, typed.keys[i]); err != nil {
				return err
			}
			builder.WriteByte(':')
			if err := writeCanonical(builder, typed.values[i]); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	default:
		return errUnsupported
	}
	return nil
}

func writeCanonicalString(builder *strings.Builder, text string) error {
	builder.WriteByte('"')
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= utf8.RuneSelf:
			return errUnsupported
		case c == '"':
			builder.WriteString(`\"`)
		case c == '\\':
			builder.WriteString(`\\`)
		case c == '\n':
			builder.WriteString(`\n`)
		case c == '\r':
			builder.WriteString(`\r`)
		case c == '\t':
			builder.WriteString(`\t`)
		case c == '\b':
			builder.WriteString(`\b`)
		case c == '\f':
			builder.WriteString(`\f`)
		case c < 0x20:
			builder.WriteString(`\u00`)
			builder.WriteByte("0123456789abcdef"[c>>4])
			builder.WriteByte("0123456789abcdef"[c&15])
		default:
			builder.WriteByte(c)
		}
	}
	builder.WriteByte('"')
	return nil
}
