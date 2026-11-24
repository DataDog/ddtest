// Package pyformat writes values as Python does, for tools that replace
// Python implementations and must keep their output: repr() for messages,
// json.dumps for reports, and repr(float) for numbers.
package pyformat

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Float is repr(float): the shortest round-tripping digits, in fixed
// notation with at least one decimal for exponents from -4 to 15, and in
// exponent notation with a signed exponent of at least two digits otherwise.
func Float(value float64) string {
	switch {
	case math.IsNaN(value):
		return "nan"
	case math.IsInf(value, 1):
		return "inf"
	case math.IsInf(value, -1):
		return "-inf"
	}
	formatted := strconv.FormatFloat(value, 'e', -1, 64)
	sign := ""
	if strings.HasPrefix(formatted, "-") {
		sign, formatted = "-", formatted[1:]
	}
	mantissa, exponentText, _ := strings.Cut(formatted, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exponent, _ := strconv.Atoi(exponentText)
	if exponent >= -4 && exponent < 16 {
		if exponent < 0 {
			return sign + "0." + strings.Repeat("0", -exponent-1) + digits
		}
		if len(digits) <= exponent+1 {
			return sign + digits + strings.Repeat("0", exponent+1-len(digits)) + ".0"
		}
		return sign + digits[:exponent+1] + "." + digits[exponent+1:]
	}
	if len(digits) > 1 {
		digits = digits[:1] + "." + digits[1:]
	}
	exponentSign := "+"
	if exponent < 0 {
		exponentSign, exponent = "-", -exponent
	}
	return sign + digits + "e" + exponentSign + fmt.Sprintf("%02d", exponent)
}

// Tuple is a Python tuple, which repr writes with parentheses.
type Tuple []any

// Repr is Python's repr() of a value made of strings, integers, floats,
// booleans, nil, slices, Tuples and string-keyed maps. Maps keep the order
// of Keys when given as an *Ordered, and sort keys otherwise.
func Repr(value any) string {
	var builder strings.Builder
	writeRepr(&builder, value)
	return builder.String()
}

// Ordered is a dict in insertion order.
type Ordered struct {
	Keys   []string
	Values map[string]any
}

func writeRepr(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("None")
	case bool:
		if typed {
			builder.WriteString("True")
		} else {
			builder.WriteString("False")
		}
	case string:
		builder.WriteString(String(typed))
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case float64:
		builder.WriteString(Float(typed))
	case []string:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = item
		}
		writeSequence(builder, "[", "]", items)
	case []any:
		writeSequence(builder, "[", "]", typed)
	case Tuple:
		if len(typed) == 1 {
			builder.WriteByte('(')
			writeRepr(builder, typed[0])
			builder.WriteString(",)")
			return
		}
		writeSequence(builder, "(", ")", typed)
	case *Ordered:
		builder.WriteByte('{')
		for i, key := range typed.Keys {
			if i > 0 {
				builder.WriteString(", ")
			}
			builder.WriteString(String(key))
			builder.WriteString(": ")
			writeRepr(builder, typed.Values[key])
		}
		builder.WriteByte('}')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		writeRepr(builder, &Ordered{Keys: keys, Values: typed})
	default:
		fmt.Fprintf(builder, "%v", typed)
	}
}

func writeSequence(builder *strings.Builder, open, close string, items []any) {
	builder.WriteString(open)
	for i, item := range items {
		if i > 0 {
			builder.WriteString(", ")
		}
		writeRepr(builder, item)
	}
	builder.WriteString(close)
}

// String is repr(str): single quotes unless the text holds a single quote
// and no double quote, with non-printable characters escaped.
func String(text string) string {
	quote := byte('\'')
	if strings.Contains(text, "'") && !strings.Contains(text, `"`) {
		quote = '"'
	}
	var builder strings.Builder
	builder.WriteByte(quote)
	for _, r := range text {
		switch {
		case r == rune(quote) || r == '\\':
			builder.WriteByte('\\')
			builder.WriteRune(r)
		case r == '\t':
			builder.WriteString(`\t`)
		case r == '\n':
			builder.WriteString(`\n`)
		case r == '\r':
			builder.WriteString(`\r`)
		case r == utf8.RuneError:
			builder.WriteString(`�`)
		case printable(r):
			builder.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&builder, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&builder, `\u%04x`, r)
		default:
			fmt.Fprintf(&builder, `\U%08x`, r)
		}
	}
	builder.WriteByte(quote)
	return builder.String()
}

// printable is str.isprintable for one character: everything except the
// Other and Separator categories, apart from the space.
func printable(r rune) bool {
	if r == ' ' {
		return true
	}
	if unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp, unicode.Zs) {
		return false
	}
	// Unassigned code points are in no category.
	return unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.P, unicode.S)
}

// DumpsIndented is json.dumps(value, indent=2, sort_keys=True): ASCII only,
// two-space indentation, and empty containers written as [] and {}. An
// *Ordered keeps its order, as json.dumps(value, indent=2) does.
func DumpsIndented(value any) string {
	var builder strings.Builder
	writeJSON(&builder, value, 0)
	return builder.String()
}

func writeJSON(builder *strings.Builder, value any, depth int) {
	indent := func(level int) {
		builder.WriteByte('\n')
		builder.WriteString(strings.Repeat("  ", level))
	}
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case string:
		writeJSONString(builder, typed)
	case int:
		builder.WriteString(strconv.Itoa(typed))
	case int64:
		builder.WriteString(strconv.FormatInt(typed, 10))
	case float64:
		switch {
		case math.IsNaN(typed):
			builder.WriteString("NaN")
		case math.IsInf(typed, 1):
			builder.WriteString("Infinity")
		case math.IsInf(typed, -1):
			builder.WriteString("-Infinity")
		default:
			builder.WriteString(Float(typed))
		}
	case []string:
		items := make([]any, len(typed))
		for i, item := range typed {
			items[i] = item
		}
		writeJSON(builder, items, depth)
	case []any:
		if len(typed) == 0 {
			builder.WriteString("[]")
			return
		}
		builder.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				builder.WriteByte(',')
			}
			indent(depth + 1)
			writeJSON(builder, item, depth+1)
		}
		indent(depth)
		builder.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		writeJSONObject(builder, keys, typed, depth)
	case *Ordered:
		writeJSONObject(builder, typed.Keys, typed.Values, depth)
	default:
		writeJSONString(builder, fmt.Sprint(typed))
	}
}

func writeJSONObject(builder *strings.Builder, keys []string, values map[string]any, depth int) {
	if len(keys) == 0 {
		builder.WriteString("{}")
		return
	}
	builder.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteByte('\n')
		builder.WriteString(strings.Repeat("  ", depth+1))
		writeJSONString(builder, key)
		builder.WriteString(": ")
		writeJSON(builder, values[key], depth+1)
	}
	builder.WriteByte('\n')
	builder.WriteString(strings.Repeat("  ", depth))
	builder.WriteByte('}')
}

// writeJSONString is json.dumps's ensure_ascii string encoding.
func writeJSONString(builder *strings.Builder, text string) {
	builder.WriteByte('"')
	for _, r := range text {
		switch {
		case r == '"':
			builder.WriteString(`\"`)
		case r == '\\':
			builder.WriteString(`\\`)
		case r == '\n':
			builder.WriteString(`\n`)
		case r == '\r':
			builder.WriteString(`\r`)
		case r == '\t':
			builder.WriteString(`\t`)
		case r == '\b':
			builder.WriteString(`\b`)
		case r == '\f':
			builder.WriteString(`\f`)
		case r < 0x20 || r > 0x7e && r < 0x10000:
			fmt.Fprintf(builder, `\u%04x`, r)
		case r >= 0x10000:
			r -= 0x10000
			fmt.Fprintf(builder, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			builder.WriteRune(r)
		}
	}
	builder.WriteByte('"')
}
