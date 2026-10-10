package minitracer

import (
	"maps"
	"strings"

	"github.com/tonyredondo/dd-ci-testing-poc/internal/thirdparty/msgp/msgp"
)

// CommonTags owns an immutable CI tag snapshot. Spans keep their own overrides;
// getters and wire projection resolve those overrides before these defaults.
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
	return func(s *Span) {
		if s.common != nil {
			// Multiple common options are uncommon, but retain earlier keys that
			// the new option does not replace, just as individual Tag options do.
			for key, value := range s.common.values {
				if _, text := s.content.Meta[key]; !text {
					if _, numeric := s.content.Metrics[key]; !numeric {
						s.content.Meta[key] = value
					}
				}
			}
		}
		for key := range s.content.Meta {
			if _, replace := c.values[key]; replace {
				delete(s.content.Meta, key)
			}
		}
		for key := range s.content.Metrics {
			if _, replace := c.values[key]; replace {
				delete(s.content.Metrics, key)
			}
		}
		s.common = c
	}
}

// Account for effective event strings and the potential envelope overhead.
// Replaced defaults consume no bytes: projection puts such keys on peer events
// instead of adding an unused (possibly large) value to the envelope.
func eventSize(event *ciEvent) int {
	size := event.Msgsize()
	if event.common != nil {
		// A new event-kind metadata entry also needs its key and map headers.
		size += event.common.bytes + msgp.StringPrefixSize + len(event.Type) + 2*msgp.MapHeaderSize
		for key := range event.Content.Meta {
			if value, replaced := event.common.values[key]; replaced {
				size -= 2*msgp.StringPrefixSize + len(key) + len(value)
			}
		}
		for key := range event.Content.Metrics {
			if _, text := event.Content.Meta[key]; text {
				continue
			}
			if value, replaced := event.common.values[key]; replaced {
				size -= 2*msgp.StringPrefixSize + len(key) + len(value)
			}
		}
	}
	return size
}

// prepareCommonMetadata leaves sealed spans untouched. A homogeneous event kind
// shares its snapshot in the envelope. Mixed snapshots (including an event with
// no defaults) fall back to per-event strings for that kind in this batch.
// An override removes only that new default from the envelope; peers get the
// string on their event. This prevents numeric inheritance and unused strings
// escaping the effective-event byte budget when every event overrides a key.
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
		for key := range event.Content.Meta {
			delete(defaults[event.Type], key)
		}
		for key := range event.Content.Metrics {
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
		var meta map[string]string
		for key, value := range event.common.values {
			if _, local := event.Content.Meta[key]; local {
				continue
			}
			if _, numeric := event.Content.Metrics[key]; numeric {
				continue
			}
			if shared, present := defaults[event.Type][key]; present && shared == value {
				continue
			}
			if meta == nil {
				meta = maps.Clone(event.Content.Meta)
				if meta == nil {
					meta = make(map[string]string, len(event.common.values))
				}
			}
			meta[key] = value
		}
		if meta != nil {
			if projected == nil {
				projected = append(ciEvents(nil), events...)
			}
			copy := *event
			copy.Content.Meta = meta
			projected[i] = &copy
		}
	}
	if projected == nil {
		projected = events
	}
	return metadata, projected
}

func isCIEventKind(kind string) bool {
	switch kind {
	case "test", "test_session_end", "test_module_end", "test_suite_end":
		return true
	}
	return false
}
