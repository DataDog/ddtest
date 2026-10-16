package minitracer

import "github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"

// A span's metadata and metrics are sets of unique keys. Up to linearEntries
// entries live in a slice, searched linearly: a test event's 22 metadata
// entries take one allocation instead of a map's four, and iterate faster.
// A larger set is a map, so that many tags never cost quadratic time. Order
// does not matter; both encode as msgpack maps.
const linearEntries = 32

type entry[V any] struct {
	key   string
	value V
}

type entrySet[V any] struct {
	list []entry[V]   // While the set has up to linearEntries entries.
	big  map[string]V // Instead of list, beyond that.
}

// newEntrySet reserves capacity entries, in the form that they need.
func newEntrySet[V any](capacity int) entrySet[V] {
	if capacity > linearEntries {
		return entrySet[V]{big: make(map[string]V, capacity)}
	}
	return entrySet[V]{list: make([]entry[V], 0, capacity)}
}

func (s *entrySet[V]) len() int {
	if s.big != nil {
		return len(s.big)
	}
	return len(s.list)
}

func (s *entrySet[V]) get(key string) (V, bool) {
	if s.big != nil {
		v, ok := s.big[key]
		return v, ok
	}
	for i := range s.list {
		if s.list[i].key == key {
			return s.list[i].value, true
		}
	}
	var zero V
	return zero, false
}

func (s *entrySet[V]) set(key string, value V) {
	if s.big != nil {
		s.big[key] = value
		return
	}
	for i := range s.list {
		if s.list[i].key == key {
			s.list[i].value = value
			return
		}
	}
	if len(s.list) < linearEntries {
		if s.list == nil {
			s.list = make([]entry[V], 0, 4)
		}
		s.list = append(s.list, entry[V]{key, value})
		return
	}
	s.big = make(map[string]V, 2*linearEntries)
	for _, e := range s.list {
		s.big[e.key] = e.value
	}
	s.big[key] = value
	s.list = nil
}

// deleteIf removes the entries whose key matches.
func (s *entrySet[V]) deleteIf(match func(string) bool) {
	if s.big != nil {
		for key := range s.big {
			if match(key) {
				delete(s.big, key)
			}
		}
		return
	}
	kept := s.list[:0]
	for _, e := range s.list {
		if !match(e.key) {
			kept = append(kept, e)
		}
	}
	clear(s.list[len(kept):])
	s.list = kept
}

func (s *entrySet[V]) delete(key string) {
	if s.big != nil {
		delete(s.big, key)
		return
	}
	for i := range s.list {
		if s.list[i].key == key {
			last := len(s.list) - 1
			s.list[i], s.list[last] = s.list[last], entry[V]{}
			s.list = s.list[:last]
			return
		}
	}
}

// all ranges over the entries.
func (s *entrySet[V]) all(yield func(string, V) bool) {
	if s.big != nil {
		for k, v := range s.big {
			if !yield(k, v) {
				return
			}
		}
		return
	}
	for _, e := range s.list {
		if !yield(e.key, e.value) {
			return
		}
	}
}

// clone copies the set with room for extra more entries.
func (s *entrySet[V]) clone(extra int) entrySet[V] {
	c := newEntrySet[V](s.len() + extra)
	for k, v := range s.all {
		c.set(k, v)
	}
	return c
}

// IsZero omits an absent set from the wire, as a nil map was.
func (s *entrySet[V]) IsZero() bool { return s.list == nil && s.big == nil }

type metaSet struct{ entrySet[string] }
type metricSet struct{ entrySet[float64] }

func (m *metaSet) EncodeMsg(en *msgp.Writer) error {
	if err := en.WriteMapHeader(uint32(m.len())); err != nil {
		return err
	}
	for k, v := range m.all {
		if err := en.WriteString(k); err != nil {
			return err
		}
		if err := en.WriteString(v); err != nil {
			return err
		}
	}
	return nil
}

func (m *metaSet) Msgsize() int {
	s := msgp.MapHeaderSize
	for k, v := range m.all {
		s += msgp.StringPrefixSize + len(k) + msgp.StringPrefixSize + len(v)
	}
	return s
}

func (m *metaSet) DecodeMsg(dc *msgp.Reader) error {
	n, err := dc.ReadMapHeader()
	if err != nil {
		return err
	}
	m.entrySet = newEntrySet[string](int(n))
	for range n {
		key, err := dc.ReadString()
		if err != nil {
			return err
		}
		value, err := dc.ReadString()
		if err != nil {
			return err
		}
		m.set(key, value)
	}
	return nil
}

func (m *metricSet) EncodeMsg(en *msgp.Writer) error {
	if err := en.WriteMapHeader(uint32(m.len())); err != nil {
		return err
	}
	for k, v := range m.all {
		if err := en.WriteString(k); err != nil {
			return err
		}
		if err := en.WriteFloat64(v); err != nil {
			return err
		}
	}
	return nil
}

func (m *metricSet) Msgsize() int {
	s := msgp.MapHeaderSize
	for k := range m.all {
		s += msgp.StringPrefixSize + len(k) + msgp.Float64Size
	}
	return s
}

func (m *metricSet) DecodeMsg(dc *msgp.Reader) error {
	n, err := dc.ReadMapHeader()
	if err != nil {
		return err
	}
	m.entrySet = newEntrySet[float64](int(n))
	for range n {
		key, err := dc.ReadString()
		if err != nil {
			return err
		}
		value, err := dc.ReadFloat64()
		if err != nil {
			return err
		}
		m.set(key, value)
	}
	return nil
}
