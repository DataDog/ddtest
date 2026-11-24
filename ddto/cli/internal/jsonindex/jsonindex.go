// Package jsonindex indexes a JSON document by the byte ranges of its values,
// without decoding them, for hot paths that read a few fields of large
// payloads or write them again unchanged. It accepts exactly the documents
// that encoding/json accepts, and its accessors read them as a decoded
// map[string]any would: the last of duplicate keys wins.
package jsonindex

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unsafe"
)

// MaxDepth is encoding/json's nesting limit.
const MaxDepth = 10000

// ErrSyntax is invalid JSON.
var ErrSyntax = errors.New("invalid JSON")

// Node is a JSON value in a Doc: its kind ('{', '[', '"', '0' for a number,
// 't', 'f' or 'n'), its byte range, and for an object member its key's range.
// Children link through first and next.
type Node struct {
	Kind                 byte
	Escaped, KeyEscaped  bool
	Start, End           int
	KeyStart, KeyEnd     int
	First, Next, Members int
}

// Doc indexes one JSON document by byte ranges, without decoding it.
type Doc struct {
	Raw   []byte
	Nodes []Node
}

// parse indexes raw, which must hold one JSON value and whitespace.
func (doc *Doc) Parse(raw []byte) (int, error) {
	doc.Raw, doc.Nodes = raw, doc.Nodes[:0]
	pos := skipSpace(raw, 0)
	root, pos, err := doc.value(pos, 0)
	if err != nil {
		return -1, err
	}
	if skipSpace(raw, pos) != len(raw) {
		return root, ErrTrailing
	}
	return root, nil
}

// ErrTrailing is data after the value.
var ErrTrailing = errors.New("trailing data")

func skipSpace(raw []byte, pos int) int {
	for pos < len(raw) && (raw[pos] == ' ' || raw[pos] == '\t' || raw[pos] == '\n' || raw[pos] == '\r') {
		pos++
	}
	return pos
}

func (doc *Doc) add(node Node) int {
	node.First, node.Next, node.KeyStart, node.KeyEnd = -1, -1, -1, -1
	doc.Nodes = append(doc.Nodes, node)
	return len(doc.Nodes) - 1
}

func (doc *Doc) value(pos, depth int) (int, int, error) {
	raw := doc.Raw
	if pos >= len(raw) || depth > MaxDepth {
		return -1, pos, ErrSyntax
	}
	switch c := raw[pos]; {
	case c == '{' || c == '[':
		index := doc.add(Node{Kind: c, Start: pos})
		closing := byte('}')
		if c == '[' {
			closing = ']'
		}
		pos = skipSpace(raw, pos+1)
		if pos < len(raw) && raw[pos] == closing {
			doc.Nodes[index].End = pos + 1
			return index, pos + 1, nil
		}
		last := -1
		for {
			keyStart, keyEnd, keyEscaped := -1, -1, false
			if c == '{' {
				if pos >= len(raw) || raw[pos] != '"' {
					return -1, pos, ErrSyntax
				}
				var err error
				keyStart = pos
				if keyEnd, keyEscaped, err = scanString(raw, pos); err != nil {
					return -1, pos, err
				}
				pos = skipSpace(raw, keyEnd)
				if pos >= len(raw) || raw[pos] != ':' {
					return -1, pos, ErrSyntax
				}
				pos = skipSpace(raw, pos+1)
			}
			child, next, err := doc.value(pos, depth+1)
			if err != nil {
				return -1, next, err
			}
			doc.Nodes[child].KeyStart, doc.Nodes[child].KeyEnd, doc.Nodes[child].KeyEscaped = keyStart, keyEnd, keyEscaped
			if last < 0 {
				doc.Nodes[index].First = child
			} else {
				doc.Nodes[last].Next = child
			}
			last = child
			doc.Nodes[index].Members++
			pos = skipSpace(raw, next)
			if pos >= len(raw) {
				return -1, pos, ErrSyntax
			}
			if raw[pos] == closing {
				doc.Nodes[index].End = pos + 1
				return index, pos + 1, nil
			}
			if raw[pos] != ',' {
				return -1, pos, ErrSyntax
			}
			pos = skipSpace(raw, pos+1)
		}
	case c == '"':
		end, escaped, err := scanString(raw, pos)
		if err != nil {
			return -1, pos, err
		}
		return doc.add(Node{Kind: '"', Start: pos, End: end, Escaped: escaped}), end, nil
	case c == '-' || c >= '0' && c <= '9':
		end, err := scanNumber(raw, pos)
		if err != nil {
			return -1, pos, err
		}
		return doc.add(Node{Kind: '0', Start: pos, End: end}), end, nil
	case c == 't' && bytes.HasPrefix(raw[pos:], []byte("true")):
		return doc.add(Node{Kind: 't', Start: pos, End: pos + 4}), pos + 4, nil
	case c == 'f' && bytes.HasPrefix(raw[pos:], []byte("false")):
		return doc.add(Node{Kind: 'f', Start: pos, End: pos + 5}), pos + 5, nil
	case c == 'n' && bytes.HasPrefix(raw[pos:], []byte("null")):
		return doc.add(Node{Kind: 'n', Start: pos, End: pos + 4}), pos + 4, nil
	}
	return -1, pos, ErrSyntax
}

// scanString returns the end of the string at pos, which starts with a quote.
func scanString(raw []byte, pos int) (int, bool, error) {
	escaped := false
	for pos++; pos < len(raw); pos++ {
		switch c := raw[pos]; {
		case c == '"':
			return pos + 1, escaped, nil
		case c == '\\':
			escaped = true
			pos++
			if pos >= len(raw) {
				return 0, false, ErrSyntax
			}
			switch raw[pos] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if pos+4 >= len(raw) {
					return 0, false, ErrSyntax
				}
				for _, h := range raw[pos+1 : pos+5] {
					if !(h >= '0' && h <= '9' || h >= 'a' && h <= 'f' || h >= 'A' && h <= 'F') {
						return 0, false, ErrSyntax
					}
				}
				pos += 4
			default:
				return 0, false, ErrSyntax
			}
		case c < 0x20:
			return 0, false, ErrSyntax
		}
	}
	return 0, false, ErrSyntax
}

func scanNumber(raw []byte, pos int) (int, error) {
	digits := func() int {
		start := pos
		for pos < len(raw) && raw[pos] >= '0' && raw[pos] <= '9' {
			pos++
		}
		return pos - start
	}
	if raw[pos] == '-' {
		pos++
	}
	if pos < len(raw) && raw[pos] == '0' {
		pos++
	} else if digits() == 0 {
		return 0, ErrSyntax
	}
	if pos < len(raw) && raw[pos] == '.' {
		pos++
		if digits() == 0 {
			return 0, ErrSyntax
		}
	}
	if pos < len(raw) && (raw[pos] == 'e' || raw[pos] == 'E') {
		pos++
		if pos < len(raw) && (raw[pos] == '+' || raw[pos] == '-') {
			pos++
		}
		if digits() == 0 {
			return 0, ErrSyntax
		}
	}
	return pos, nil
}

// text is a string node's or key's value. An unescaped one shares the
// document's bytes, so it is valid only while they are: callers that keep
// it clone it.
func (doc *Doc) Text(start, end int, escaped bool) string {
	if !escaped {
		if end-start == 2 {
			return ""
		}
		return unsafe.String(&doc.Raw[start+1], end-start-2)
	}
	var value string
	_ = json.Unmarshal(doc.Raw[start:end], &value)
	return value
}

func (doc *Doc) Key(index int) string {
	node := &doc.Nodes[index]
	return doc.Text(node.KeyStart, node.KeyEnd, node.KeyEscaped)
}

// member is an object's member by key, the last one as in a decoded map, or
// -1.
func (doc *Doc) Member(object int, key string) int {
	if object < 0 || doc.Nodes[object].Kind != '{' {
		return -1
	}
	found := -1
	for child := doc.Nodes[object].First; child >= 0; child = doc.Nodes[child].Next {
		node := &doc.Nodes[child]
		if node.KeyEnd-node.KeyStart-2 == len(key) && !node.KeyEscaped {
			if string(doc.Raw[node.KeyStart+1:node.KeyEnd-1]) == key {
				found = child
			}
		} else if node.KeyEscaped && doc.Key(child) == key {
			found = child
		}
	}
	return found
}

func (doc *Doc) String(index int) (string, bool) {
	if index < 0 || doc.Nodes[index].Kind != '"' {
		return "", false
	}
	node := &doc.Nodes[index]
	return doc.Text(node.Start, node.End, node.Escaped), true
}

func (doc *Doc) Number(index int) (json.Number, bool) {
	if index < 0 || doc.Nodes[index].Kind != '0' {
		return "", false
	}
	return json.Number(doc.Raw[doc.Nodes[index].Start:doc.Nodes[index].End]), true
}

// members lists an object's members as a decoded map holds them: the last
// value of each key, in encoding/json's key order.
func (doc *Doc) MemberList(object int) ([]string, []int) {
	type member struct {
		key   string
		index int
	}
	list := make([]member, 0, doc.Nodes[object].Members)
	for child := doc.Nodes[object].First; child >= 0; child = doc.Nodes[child].Next {
		list = append(list, member{doc.Key(child), child})
	}
	slices.SortStableFunc(list, func(a, b member) int { return strings.Compare(a.key, b.key) })
	keys, values := make([]string, 0, len(list)), make([]int, 0, len(list))
	for i, item := range list {
		if i+1 < len(list) && list[i+1].key == item.key {
			continue
		}
		keys, values = append(keys, item.key), append(values, item.index)
	}
	return keys, values
}
