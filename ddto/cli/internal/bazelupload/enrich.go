package bazelupload

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"unicode/utf8"
)

const utf8BOM = "\xef\xbb\xbf"

// testEventTypes are the event types that have a metadata scope of their own.
var testEventTypes = [...]string{"test", "test_suite_end", "test_module_end", "test_session_end"}

type flatMetadata struct {
	meta, metrics Object
}

func flatten(values Object, isContext bool) flatMetadata {
	out := flatMetadata{meta: make(Object, len(values)), metrics: make(Object)}
	for key, raw := range values {
		if isContext && key == "topt.api_key_fingerprint" {
			continue
		}
		switch value := raw.(type) {
		case string:
			out.meta[key] = value
		case bool:
			out.meta[key] = "false"
			if value {
				out.meta[key] = "true"
			}
		case json.Number:
			out.metrics[key] = value
		default:
			// Values originate from the strictly decoded plan.
			encoded, _ := encodeJSON(value)
			out.meta[key] = string(encoded)
		}
	}
	return out
}

// jsonEncoder encodes into one buffer that it reuses, for a worker's hot
// paths. A result stays valid until the next call.
type jsonEncoder struct {
	buffer  bytes.Buffer
	encoder *json.Encoder
}

func newJSONEncoder() *jsonEncoder {
	encoding := &jsonEncoder{}
	encoding.encoder = json.NewEncoder(&encoding.buffer)
	encoding.encoder.SetEscapeHTML(false)
	return encoding
}

func (encoding *jsonEncoder) encode(value any) ([]byte, error) {
	encoding.buffer.Reset()
	if err := encoding.encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(encoding.buffer.Bytes(), []byte{'\n'}), nil
}

// enrichmentBytes bounds what enrichment adds to an event's encoding: each
// field's key, value, quotes, colon and comma.
func enrichmentBytes(fields ...flatMetadata) int {
	size := 0
	for _, set := range fields {
		for key, value := range set.meta {
			text, _ := value.(string)
			size += len(key) + len(text) + 6
		}
		for key, value := range set.metrics {
			number, _ := value.(json.Number)
			size += len(key) + len(number) + 4
		}
	}
	return size
}

func encodeJSON(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte{'\n'}), nil
}

func decodeObject(raw []byte) (Object, error) {
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("payload must be UTF-8")
	}
	raw = bytes.TrimPrefix(raw, []byte(utf8BOM))
	var doc Object
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return nil, fmt.Errorf("payload must be a JSON object")
	}
	if err := decoder.Decode(new(json.RawMessage)); err != io.EOF {
		return nil, fmt.Errorf("payload must contain a single JSON object")
	}
	return doc, nil
}

func reportingService(context, metadata Object) (string, error) {
	_, declared := metadata["bazel.test_optimization.service_name"]
	if !declared {
		return "", nil
	}
	for _, identity := range [][2]string{
		{"bazel.test_optimization.repo_name", "topt.sync.repository_name"},
		{"bazel.test_optimization.service_name", "service.name"},
		{"bazel.test_optimization.runtime_name", "runtime.name"},
	} {
		target := stringValue(metadata, identity[0])
		if target == "" || target != stringValue(context, identity[1]) {
			return "", fmt.Errorf("reporting service identity mismatch: %s", identity[0])
		}
	}
	return stringValue(context, "service.name"), nil
}

func objectField(parent Object, key string) Object {
	existing := object(parent[key])
	if existing != nil {
		return existing
	}
	fields := make(Object)
	parent[key] = fields
	return fields
}

func enrich(payload Object, source Source, plan Plan, contextFields flatMetadata, owners *ownerLookup) error {
	context := plan.Contexts[source.Context]
	service, err := reportingService(context, source.Metadata)
	if err != nil {
		return err
	}
	metadata := object(payload["metadata"])
	global := object(metadata["*"])
	defaults := Object{
		"runtime-id": firstString(stringValue(global, "runtime-id"), stringValue(context, "runtime-id"),
			stringValue(context, "runtime.id"), stringValue(context, "runtime_id"), plan.RuntimeID),
		"language": firstString(stringValue(global, "language"), stringValue(context, "language"),
			stringValue(context, "runtime.name"), stringValue(context, "runtime_name"), "bazel"),
		"library_version": firstString(stringValue(global, "library_version"), plan.RulesVersion),
	}
	if env := firstString(stringValue(global, "env"), stringValue(context, "env")); env != "" {
		defaults["env"] = env
	}
	globalFields := make(Object, len(global)+len(defaults))
	maps.Copy(globalFields, global)
	delete(globalFields, "env")
	maps.Copy(globalFields, defaults)
	scopes := Object{"*": globalFields}
	for _, key := range testEventTypes {
		if metadata[key] != nil {
			scopes[key] = metadata[key]
		}
	}
	payload["metadata"] = scopes

	bazelFields := flatten(source.Metadata, false)
	events, _ := payload["events"].([]any)
	for _, value := range events {
		event := object(value)
		if event == nil {
			continue
		}
		content := objectField(event, "content")
		kind := stringValue(event, "type")
		if service != "" && (testEvent(kind) || testSpan(kind, content)) {
			content["service"] = service
		}
		meta := objectField(content, "meta")
		metrics := objectField(content, "metrics")
		maps.Copy(meta, contextFields.meta)
		maps.Copy(metrics, contextFields.metrics)
		maps.Copy(meta, bazelFields.meta)
		maps.Copy(metrics, bazelFields.metrics)
		if kind != "span" {
			owners.enrich(content, meta, stringValue(context, "ci.workspace_path"))
		}
	}
	return nil
}

func testEvent(kind string) bool {
	return slices.Contains(testEventTypes[:], kind)
}

// testSpan reports an event that describes one test, also in span form.
func testSpan(kind string, content Object) bool {
	return kind == "test" || (kind == "span" || kind == "") && content["type"] == "test"
}

func validateEnrichment(events []any, expected []string) error {
	for _, tag := range expected {
		if tag == "" {
			continue
		}
		found := false
		for _, value := range events {
			content := object(object(value)["content"])
			_, meta := object(content["meta"])[tag]
			_, metric := object(content["metrics"])[tag]
			if meta || metric {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("missing enriched tag %q", tag)
		}
	}
	return nil
}
