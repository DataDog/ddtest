package bazelupload

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/jsonindex"
)

// jdoc is an indexed payload or written event.
type jdoc struct {
	jsonindex.Doc
}

// The streaming preparation of a test source writes the same request bodies
// as readTest, without decoding the events into maps and encoding them again.
// The payload is parsed once into an index of byte ranges; each event is
// written in encoding/json's form, sorted keys and all, with the enrichment
// applied as it is written; and the written event is indexed again to check
// the schema, the test facts and the expected tags. Options.Streaming turns it
// on; a batch upload keeps the decoding path.

// writeString writes a string as encoding/json does without HTML escaping.
func writeString(out []byte, text string) []byte {
	if plainString(text) {
		out = append(out, '"')
		out = append(out, text...)
		return append(out, '"')
	}
	encoded, _ := encodeJSON(text)
	return append(out, encoded...)
}

// plainString reports whether encoding/json writes text unchanged between
// quotes: no quote, backslash, control character, U+2028, U+2029 or invalid
// UTF-8.
func plainString(text string) bool {
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c < 0x20 || c == '"' || c == '\\' || c >= 0x80 && c == 0xe2 && i+2 < len(text) && text[i+1] == 0x80 &&
			(text[i+2] == 0xa8 || text[i+2] == 0xa9) {
			return false
		}
	}
	return utf8.ValidString(text)
}

// write writes a value as encoding/json writes its decoded form.
func (doc *jdoc) write(out []byte, index int) []byte {
	node := &doc.Nodes[index]
	switch node.Kind {
	case '{':
		keys, values := doc.MemberList(index)
		out = append(out, '{')
		for i, key := range keys {
			if i > 0 {
				out = append(out, ',')
			}
			out = writeString(out, key)
			out = append(out, ':')
			out = doc.write(out, values[i])
		}
		return append(out, '}')
	case '[':
		out = append(out, '[')
		for child, i := node.First, 0; child >= 0; child, i = doc.Nodes[child].Next, i+1 {
			if i > 0 {
				out = append(out, ',')
			}
			out = doc.write(out, child)
		}
		return append(out, ']')
	case '"':
		if !node.Escaped {
			text := doc.Raw[node.Start+1 : node.End-1]
			if !bytes.Contains(text, []byte("\u2028")) && !bytes.Contains(text, []byte("\u2029")) {
				return append(out, doc.Raw[node.Start:node.End]...)
			}
		}
		return writeString(out, doc.Text(node.Start, node.End, node.Escaped))
	}
	return append(out, doc.Raw[node.Start:node.End]...)
}

// writeValue writes a decoded value that enrichment adds.
func writeValue(out []byte, value any) []byte {
	switch typed := value.(type) {
	case string:
		return writeString(out, typed)
	case json.Number:
		return append(out, typed...)
	}
	encoded, _ := encodeJSON(value)
	return append(out, encoded...)
}

// field is a member that enrichment writes: a key and its decoded value.
type field struct {
	key   string
	value any
}

func sortedFields(values map[string]any) []field {
	fields := make([]field, 0, len(values))
	for key, value := range values {
		fields = append(fields, field{key, value})
	}
	slices.SortFunc(fields, func(a, b field) int { return strings.Compare(a.key, b.key) })
	return fields
}

// writeFields writes an object's members with fields, sorted by key,
// replacing or adding members; object may be -1 for a new object. A nested
// writer writes the value of a field whose value is nil.
func (doc *jdoc) writeFields(out []byte, object int, fields []field, nested func([]byte, string) []byte) []byte {
	var keys []string
	var values []int
	if object >= 0 {
		keys, values = doc.MemberList(object)
	}
	out = append(out, '{')
	i, j, written := 0, 0, 0
	for i < len(keys) || j < len(fields) {
		if written > 0 {
			out = append(out, ',')
		}
		written++
		switch {
		case j >= len(fields) || i < len(keys) && keys[i] < fields[j].key:
			out = writeString(out, keys[i])
			out = append(out, ':')
			out = doc.write(out, values[i])
			i++
		default:
			if i < len(keys) && keys[i] == fields[j].key {
				i++
			}
			out = writeString(out, fields[j].key)
			out = append(out, ':')
			if fields[j].value == nil {
				out = nested(out, fields[j].key)
			} else {
				out = writeValue(out, fields[j].value)
			}
			j++
		}
	}
	return append(out, '}')
}

// streamEnrichment is what enrich adds to every event of a source.
type streamEnrichment struct {
	service       string
	meta, metrics []field
	workspace     string
	owners        *ownerLookup
	// scratch holds the meta fields of an event that gains its owners.
	scratch []field
}

var (
	contentField = []field{{key: "content"}}
	contentParts = []field{{key: "meta"}, {key: "metrics"}}
)

// writeEvent writes an event of the source as enrich and encoding/json write
// it.
func (doc *jdoc) writeEvent(out []byte, event int, enrichment *streamEnrichment) []byte {
	if doc.Nodes[event].Kind != '{' {
		return doc.write(out, event)
	}
	kind, _ := doc.String(doc.Member(event, "type"))
	content := doc.Member(event, "content")
	if content >= 0 && doc.Nodes[content].Kind != '{' {
		content = -1
	}
	parts := contentParts
	if enrichment.service != "" {
		contentType, _ := doc.String(doc.Member(content, "type"))
		if testEvent(kind) || kind == "test" || (kind == "span" || kind == "") && contentType == "test" {
			parts = []field{{key: "meta"}, {key: "metrics"}, {key: "service", value: enrichment.service}}
		}
	}
	meta, metrics := doc.Member(content, "meta"), doc.Member(content, "metrics")
	if meta >= 0 && doc.Nodes[meta].Kind != '{' {
		meta = -1
	}
	if metrics >= 0 && doc.Nodes[metrics].Kind != '{' {
		metrics = -1
	}
	metaFields := enrichment.meta
	if kind != "span" {
		if owners := doc.codeowners(content, meta, enrichment); owners != "" {
			at, _ := slices.BinarySearchFunc(enrichment.meta, "test.codeowners", func(f field, key string) int {
				return strings.Compare(f.key, key)
			})
			metaFields = append(append(append(enrichment.scratch[:0], enrichment.meta[:at]...),
				field{"test.codeowners", owners}), enrichment.meta[at:]...)
			enrichment.scratch = metaFields
		}
	}
	return doc.writeFields(out, event, contentField, func(out []byte, _ string) []byte {
		return doc.writeFields(out, content, parts, func(out []byte, key string) []byte {
			if key == "meta" {
				return doc.writeFields(out, meta, metaFields, nil)
			}
			return doc.writeFields(out, metrics, enrichment.metrics, nil)
		})
	})
}

// codeowners is ownerLookup.enrich over the merged meta: the owners to add,
// or "".
func (doc *jdoc) codeowners(content, meta int, enrichment *streamEnrichment) string {
	lookup := enrichment.owners
	if lookup == nil || lookup.rules == nil {
		return ""
	}
	fieldValue := func(key string) (any, bool) {
		at, found := slices.BinarySearchFunc(enrichment.meta, key, func(f field, key string) int { return strings.Compare(f.key, key) })
		if !found {
			return nil, false
		}
		return enrichment.meta[at].value, true
	}
	if _, exists := fieldValue("test.codeowners"); exists || doc.Member(meta, "test.codeowners") >= 0 {
		return ""
	}
	metaString := func(key string) string {
		if value, ok := fieldValue(key); ok {
			text, _ := value.(string)
			return text
		}
		text, _ := doc.String(doc.Member(meta, key))
		return text
	}
	source := doc.Member(content, "source")
	file, _ := doc.String(doc.Member(source, "file"))
	path, _ := doc.String(doc.Member(source, "path"))
	sourceFile := firstString(metaString("test.source.file"), metaString("test.source.path"), metaString("source.file"),
		metaString("source.path"), file, path)
	if sourceFile == "" {
		return ""
	}
	key := [2]string{sourceFile, enrichment.workspace}
	result, cached := lookup.cache[key]
	if !cached {
		result = lookup.match(sourceFile, enrichment.workspace)
		if len(lookup.cache) < 4096 {
			// The key may share the payload's bytes.
			lookup.cache[[2]string{strings.Clone(sourceFile), enrichment.workspace}] = result
		}
	}
	return result
}

// streamFacts reads the test facts of a written event, as readTest counts
// them.
func (doc *jdoc) streamFacts(event int) (test, emptySession bool) {
	if doc.Nodes[event].Kind != '{' {
		return false, false
	}
	kind, _ := doc.String(doc.Member(event, "type"))
	content := doc.Member(event, "content")
	contentType, _ := doc.String(doc.Member(content, "type"))
	test = kind == "test" || (kind == "span" || kind == "") && contentType == "test"
	if kind != "test_session_end" {
		return test, false
	}
	meta, metrics := doc.Member(content, "meta"), doc.Member(content, "metrics")
	reason, _ := doc.String(doc.Member(meta, "test.session.empty_reason"))
	status, _ := doc.String(doc.Member(meta, "test.status"))
	session, _ := doc.Number(doc.Member(content, "test_session_id"))
	id, err := strconv.ParseUint(session.String(), 10, 64)
	exit, _ := doc.Number(doc.Member(metrics, "test.exit_code"))
	failed, _ := doc.Number(doc.Member(content, "error"))
	return test, reason == "zero_tests" && status == "skip" && err == nil && id > 0 && exit == "0" && failed == "0"
}

// accepts is schemaNode.accepts over an indexed value.
func (node *schemaNode) acceptsNode(doc *jdoc, index int) bool {
	for _, child := range node.all {
		if !child.acceptsNode(doc, index) {
			return false
		}
	}
	if len(node.any) != 0 && !slices.ContainsFunc(node.any, func(child *schemaNode) bool { return child.acceptsNode(doc, index) }) {
		return false
	}
	if node.condition != nil {
		child := node.other
		if node.condition.acceptsNode(doc, index) {
			child = node.then
		}
		if child != nil && !child.acceptsNode(doc, index) {
			return false
		}
	}
	value := &doc.Nodes[index]
	kind := "null"
	switch value.Kind {
	case '{':
		kind = "object"
	case '[':
		kind = "array"
	case '"':
		kind = "string"
	case 't', 'f':
		kind = "boolean"
	case '0':
		kind = "number"
		number, _ := doc.Number(index)
		if !strings.ContainsAny(number.String(), ".eE") && slices.Contains(node.types, "integer") {
			kind = "integer"
		}
		if node.minimum != "" && compareNumber(number, node.minimum) < 0 ||
			node.maximum != "" && compareNumber(number, node.maximum) > 0 {
			return false
		}
	}
	if len(node.types) != 0 && !slices.Contains(node.types, kind) {
		return false
	}
	if len(node.allowed) != 0 && !slices.ContainsFunc(node.allowed, func(candidate any) bool {
		switch typed := candidate.(type) {
		case json.Number:
			number, numeric := doc.Number(index)
			return numeric && compareNumber(number, typed) == 0
		case string:
			text, ok := doc.String(index)
			return ok && text == typed
		case bool:
			return value.Kind == 't' && typed || value.Kind == 'f' && !typed
		case nil:
			return value.Kind == 'n'
		}
		return false
	}) {
		return false
	}
	switch value.Kind {
	case '{':
		keys, values := doc.MemberList(index)
		for _, key := range node.required {
			if _, found := slices.BinarySearch(keys, key); !found {
				return false
			}
		}
		for i, key := range keys {
			child, found := node.properties[key]
			if found && !child.acceptsNode(doc, values[i]) {
				return false
			}
			for _, pattern := range node.patterns {
				if pattern.everything || pattern.expression.MatchString(key) {
					found = true
					if !pattern.node.acceptsNode(doc, values[i]) {
						return false
					}
				}
			}
			if !found && !node.additional {
				return false
			}
		}
	case '[':
		if node.items != nil {
			for child := value.First; child >= 0; child = doc.Nodes[child].Next {
				if !node.items.acceptsNode(doc, child) {
					return false
				}
			}
		}
	}
	return true
}

// streamable reports whether the schema checks events one at a time: the
// payload's events property constrains nothing but its type and items.
func (node *schemaNode) streamable() bool {
	events := node.properties["events"]
	return events != nil && events.items != nil && len(events.all) == 0 && len(events.any) == 0 &&
		events.condition == nil && len(events.allowed) == 0 && events.minimum == "" && events.maximum == ""
}

// streamTest prepares a test source as readTest does.
func (work *worker) streamTest(index int) (preparedTest, bool) {
	if !work.schema.streamable() {
		return work.readTest(index)
	}
	source, result := work.plan.Sources[index], &work.results[index]
	if !checkSuffix(source, ".json") {
		result.fail("unsupported_test_payload_format", fmt.Errorf("test payloads must be JSON"))
		return preparedTest{}, false
	}
	raw, err := work.read(index)
	if err != nil {
		result.fail("test_payload_read_failed", err)
		return preparedTest{}, false
	}
	if !utf8.Valid(raw) {
		result.fail("invalid_test_payload", fmt.Errorf("payload must be UTF-8"))
		return preparedTest{}, false
	}
	size := len(raw)
	raw = bytes.TrimPrefix(raw, []byte(utf8BOM))
	doc := &work.stream
	root, err := doc.Parse(raw)
	switch {
	case errors.Is(err, jsonindex.ErrTrailing) && doc.Nodes[root].Kind == '{':
		result.fail("invalid_test_payload", fmt.Errorf("payload must contain a single JSON object"))
		return preparedTest{}, false
	case err != nil || doc.Nodes[root].Kind != '{':
		result.fail("invalid_test_payload", fmt.Errorf("payload must be a JSON object"))
		return preparedTest{}, false
	}
	keys, values := doc.MemberList(root)
	envelope := make(Object, len(keys))
	events := -1
	for i, key := range keys {
		if key == "events" {
			events = values[i]
			continue
		}
		var value any
		decoder := json.NewDecoder(bytes.NewReader(doc.Raw[doc.Nodes[values[i]].Start:doc.Nodes[values[i]].End]))
		decoder.UseNumber()
		_ = decoder.Decode(&value)
		envelope[key] = value
	}
	if events < 0 || doc.Nodes[events].Kind != '[' || doc.Nodes[events].Members == 0 {
		result.Status = statusSkipped
		result.warn("test_payload_without_events")
		return preparedTest{}, false
	}
	context := work.plan.Contexts[source.Context]
	service, err := enrichScopes(envelope, source, work.plan)
	if err != nil {
		result.fail("reporting_service_invalid", err)
		return preparedTest{}, false
	}
	contextFields, bazelFields := work.contexts[source.Context], flatten(source.Metadata, false)
	meta, metrics := map[string]any{}, map[string]any{}
	for _, set := range []flatMetadata{contextFields, bazelFields} {
		for key, value := range set.meta {
			meta[key] = value
		}
		for key, value := range set.metrics {
			metrics[key] = value
		}
	}
	enrichment := &streamEnrichment{service: service, meta: sortedFields(meta), metrics: sortedFields(metrics),
		workspace: stringValue(context, "ci.workspace_path"), owners: &work.owners}
	encoded := make([][]byte, 0, doc.Nodes[events].Members)
	for event := doc.Nodes[events].First; event >= 0; event = doc.Nodes[event].Next {
		encoded = append(encoded, doc.writeEvent(nil, event, enrichment))
	}
	result.Events, result.TestEvents = len(encoded), 0
	empty, accepted := false, work.schema.accepts(withEmptyEvents(envelope))
	written := &work.written
	tags := map[string]bool{}
	for _, event := range encoded {
		root, err := written.Parse(event)
		if err != nil {
			return work.readTest(index)
		}
		test, session := written.streamFacts(root)
		if test {
			result.TestEvents++
		}
		empty = empty || session
		accepted = accepted && work.schema.properties["events"].items.acceptsNode(written, root)
		content := written.Member(root, "content")
		for _, tag := range work.options.ExpectedTags {
			if tag != "" && !tags[tag] && (written.Member(written.Member(content, "meta"), tag) >= 0 ||
				written.Member(written.Member(content, "metrics"), tag) >= 0) {
				tags[tag] = true
			}
		}
	}
	result.EmptyTestSession = result.TestEvents == 0 && empty
	if !accepted {
		result.warn("schema_validation_failed")
	}
	for _, tag := range work.options.ExpectedTags {
		if tag != "" && !tags[tag] {
			result.fail("enrichment_tags_missing", fmt.Errorf("missing enriched tag %q", tag))
			return preparedTest{}, false
		}
	}
	evp := ""
	if !work.urls.agentless {
		evp = "citestcycle-intake"
	}
	test := preparedTest{envelope: envelope,
		headers: requestHeaders(work.plan, object(object(envelope["metadata"])["*"]), work.apiKey, evp)}
	chunks, err := prepareEncodedChunks(envelope, encoded, testRequestLimit, size)
	if err != nil {
		result.fail("single_event_exceeds_payload_limit", err)
		return preparedTest{}, false
	}
	test.chunks = chunks
	return test, true
}

func withEmptyEvents(envelope Object) Object {
	payload := make(Object, len(envelope)+1)
	for key, value := range envelope {
		payload[key] = value
	}
	payload["events"] = []any{}
	return payload
}

// prepareEncodedChunks is prepareChunks over events already encoded.
func prepareEncodedChunks(envelope Object, events [][]byte, limit, sizeHint int) ([]testChunk, error) {
	header, err := encodeJSON(envelope)
	if err != nil {
		return nil, err
	}
	header = header[:len(header)-1]
	if len(header) > 1 {
		header = append(header, ',')
	}
	header = append(header, `"events":[`...)
	if len(header)+2 > limit {
		return nil, fmt.Errorf("test payload envelope exceeds %d bytes", limit)
	}
	var chunks []testChunk
	newBody := func() []byte {
		body := make([]byte, 0, min(limit, len(header)+sizeHint+2))
		return append(body, header...)
	}
	body := newBody()
	first := 0
	for index, raw := range events {
		if len(header)+len(raw)+2 > limit {
			return nil, fmt.Errorf("event %d exceeds %d bytes with its envelope", index, limit)
		}
		if len(body)+len(raw)+3 > limit && index > first {
			chunks = append(chunks, testChunk{body: append(body, ']', '}'), first: first, end: index})
			first = index
			body = newBody()
		}
		if index > first {
			body = append(body, ',')
		}
		body = append(body, raw...)
	}
	return append(chunks, testChunk{body: append(body, ']', '}'), first: first, end: len(events)}), nil
}
