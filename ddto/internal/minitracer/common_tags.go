package minitracer

import (
	"maps"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// CommonTags owns an immutable CI tag snapshot. Spans keep their own overrides;
// getters and wire projection resolve those overrides before these defaults.
// values holds only keys that IsSharedCITag selects, so other keys never need
// a lookup.
type CommonTags struct {
	values map[string]string
	bytes  int
}

// IsSharedCITag restricts automatic lifting to the CI/Git/system string tags.
// Capabilities, test attributes, IDs and metrics keep their existing wire fields.
func IsSharedCITag(key string) bool {
	return strings.HasPrefix(key, "ci.") || strings.HasPrefix(key, "git.") ||
		strings.HasPrefix(key, "os.") || strings.HasPrefix(key, "runtime.") || key == "_dd.ci.env_vars"
}

// NewCommonTags copies the CI/Git/system values selected by IsSharedCITag and
// ignores other keys: those keep their own tag semantics, and delivery would
// otherwise lift them into event-kind metadata. Callers may reuse or mutate
// their input after construction.
func NewCommonTags(values map[string]string) *CommonTags {
	common := &CommonTags{values: make(map[string]string, len(values))}
	for key, value := range values {
		if !IsSharedCITag(key) {
			continue
		}
		common.values[key] = value
		common.bytes += 2*msgp.StringPrefixSize + len(key) + len(value)
	}
	return common
}

// Option applies defaults at this point in option order. Later string/numeric
// tags win; an earlier tag of the same name is replaced, as with ordinary Tag.
func (c *CommonTags) Option() StartSpanOption {
	return StartSpanOption{apply: func(s *Span) {
		if s.event.common != nil {
			// Multiple common options are uncommon, but retain earlier keys that
			// the new option does not replace, just as individual Tag options do.
			for key, value := range s.event.common.values {
				if _, text := s.event.Content.Meta.get(key); !text {
					if _, numeric := s.event.Content.Metrics.get(key); !numeric {
						s.event.Content.Meta.set(key, value)
					}
				}
			}
		}
		replaced := func(key string) bool {
			if !IsSharedCITag(key) {
				return false
			}
			_, replace := c.values[key]
			return replace
		}
		s.event.Content.Meta.deleteIf(replaced)
		s.event.Content.Metrics.deleteIf(replaced)
		s.event.common = c
	}}
}

// Account for effective event strings and the potential envelope overhead.
// Replaced defaults consume no bytes: projection puts such keys on peer events
// instead of adding an unused (possibly large) value to the envelope. This
// worst case charges the whole snapshot to the event; overrides reports that
// the event replaces one of its defaults.
func eventSize(event *ciEvent) (size int, overrides bool) {
	size = event.Msgsize()
	if event.common != nil {
		size += event.common.entrySize(event.Type)
		for key := range event.Content.Meta.all {
			if !IsSharedCITag(key) {
				continue
			}
			if value, replaced := event.common.values[key]; replaced {
				size -= 2*msgp.StringPrefixSize + len(key) + len(value)
				overrides = true
			}
		}
		for key := range event.Content.Metrics.all {
			if !IsSharedCITag(key) {
				continue
			}
			if _, text := event.Content.Meta.get(key); text {
				continue
			}
			if value, replaced := event.common.values[key]; replaced {
				size -= 2*msgp.StringPrefixSize + len(key) + len(value)
				overrides = true
			}
		}
	}
	return size, overrides
}

// entrySize bounds the snapshot as an event-kind metadata entry: its strings,
// and the entry's key and map headers. Projected onto an event, the strings
// take no more.
func (c *CommonTags) entrySize(kind string) int {
	return c.bytes + msgp.StringPrefixSize + len(kind) + 2*msgp.MapHeaderSize
}

// prepareCommonMetadata leaves sealed spans untouched. A homogeneous event kind
// shares its snapshot in the envelope. Mixed snapshots (including an event with
// no defaults) fall back to per-event strings for that kind in this batch.
// An override removes only that new default from the envelope; peers get the
// string on their event. This prevents numeric inheritance and unused strings
// escaping the effective-event byte budget when every event overrides a key.
// Only events whose overrides flag Client.add set, from eventSize, can remove
// a default; the others are not searched.
func prepareCommonMetadata(base map[string]map[string]string, events ciEvents) (map[string]map[string]string, ciEvents) {
	groups := make(map[string]*CommonTags)
	for _, event := range events {
		common, seen := groups[event.Type]
		if !seen {
			groups[event.Type] = event.common
		} else if common != event.common {
			groups[event.Type] = nil
		}
	}
	defaults := make(map[string]map[string]string)
	metadata := base
	for kind, common := range groups {
		if isCIEventKind(kind) && common != nil && len(common.values) != 0 {
			defaults[kind] = maps.Clone(common.values)
		}
	}
	for _, event := range events {
		if !event.overrides {
			continue
		}
		for key := range event.Content.Meta.all {
			delete(defaults[event.Type], key)
		}
		for key := range event.Content.Metrics.all {
			delete(defaults[event.Type], key)
		}
	}
	if len(defaults) != 0 {
		metadata = maps.Clone(base)
		if metadata == nil {
			metadata = make(map[string]map[string]string, len(defaults))
		}
		for kind, values := range defaults {
			merged := maps.Clone(base[kind])
			if merged == nil {
				merged = make(map[string]string, len(values))
			}
			maps.Copy(merged, values)
			metadata[kind] = merged
		}
	}
	var projected ciEvents
	for i, event := range events {
		if event.common == nil {
			continue
		}
		if groups[event.Type] == event.common && len(defaults[event.Type]) == len(event.common.values) {
			continue // Ordinary batch: the whole base is already in the envelope.
		}
		var meta *metaSet
		for key, value := range event.common.values {
			if _, local := event.Content.Meta.get(key); local {
				continue
			}
			if _, numeric := event.Content.Metrics.get(key); numeric {
				continue
			}
			if shared, present := defaults[event.Type][key]; present && shared == value {
				continue
			}
			if meta == nil {
				meta = &metaSet{event.Content.Meta.clone(len(event.common.values))}
			}
			meta.set(key, value)
		}
		if meta != nil {
			if projected == nil {
				projected = append(ciEvents(nil), events...)
			}
			copy := *event
			copy.Content.Meta = *meta
			projected[i] = &copy
		}
	}
	if projected == nil {
		projected = events
	}
	return metadata, projected
}

func isCIEventKind(kind string) bool {
	return ciEventKindIndex(kind) >= 0
}

// ciEventKindIndex numbers the test-event kinds, or returns -1.
func ciEventKindIndex(kind string) int {
	switch kind {
	case "test":
		return 0
	case "test_session_end":
		return 1
	case "test_module_end":
		return 2
	case "test_suite_end":
		return 3
	}
	return -1
}
