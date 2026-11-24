package bazelinspect

import (
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

type (
	jsonNumber = pyjson.Number
	jsonObject = pyjson.Object
)

var (
	errUnsupported = pyjson.ErrUnsupported
	parseJSON      = pyjson.Parse
	pythonInt      = pyjson.Int
	coalesced      = pyjson.Coalesced
)

const maxJSONDepth = pyjson.MaxDepth

// normalizedEventID is _normalized_event_id in bep_execution.py: object keys
// in snake_case, recursively. Non-ASCII keys and keys that collide once
// normalized are unsupported.
func normalizedEventID(value any) (any, error) {
	switch typed := value.(type) {
	case *jsonObject:
		normalized := &jsonObject{}
		for i, key := range typed.Keys {
			name, err := snakeKey(key)
			if err != nil {
				return nil, err
			}
			if _, collision := normalized.Get(name); collision {
				return nil, errUnsupported
			}
			item, err := normalizedEventID(typed.Values[i])
			if err != nil {
				return nil, err
			}
			normalized.Keys = append(normalized.Keys, name)
			normalized.Values = append(normalized.Values, item)
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
		order := make([]int, len(typed.Keys))
		for i := range order {
			order[i] = i
		}
		sort.Slice(order, func(a, b int) bool { return typed.Keys[order[a]] < typed.Keys[order[b]] })
		builder.WriteByte('{')
		for n, i := range order {
			if n > 0 {
				builder.WriteByte(',')
			}
			if err := writeCanonicalString(builder, typed.Keys[i]); err != nil {
				return err
			}
			builder.WriteByte(':')
			if err := writeCanonical(builder, typed.Values[i]); err != nil {
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
