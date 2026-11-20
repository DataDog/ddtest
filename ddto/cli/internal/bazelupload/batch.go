package bazelupload

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
)

const batchSources = 32

type batchEntry struct {
	index     int
	test      preparedTest
	candidate batchCandidate
}

type batchCandidate struct {
	key      string
	envelope Object
	events   []any
	size     int
}

func prepareBatch(test preparedTest) batchCandidate {
	if test.estimate == 0 || test.estimate > testRequestLimit || test.envelope["version"] != json.Number("1") {
		return batchCandidate{}
	}
	metadata := object(test.envelope["metadata"])
	scopes := make(Object, len(metadata))
	for scope, value := range metadata {
		fields := object(value)
		if fields == nil {
			return batchCandidate{}
		}
		scopes[scope] = maps.Clone(fields)
	}
	global := object(metadata["*"])
	globalID := stringValue(global, "runtime-id")
	if globalID == "" {
		return batchCandidate{}
	}
	var events []any
	size := test.estimate
	for _, value := range test.events {
		event := object(value)
		kind := stringValue(event, "type")
		content := object(event["content"])
		meta := object(content["meta"])
		if content == nil || meta == nil || kind == "" {
			return batchCandidate{}
		}
		id := globalID
		if local, ok := meta["runtime-id"]; ok {
			id, _ = local.(string)
		}
		if testEvent(kind) || kind == "span" {
			if fixed, ok := object(metadata[kind])["runtime-id"]; ok {
				id, _ = fixed.(string)
			}
		}
		if id == "" {
			return batchCandidate{}
		}
		if id != stringValue(meta, "runtime-id") {
			// Bound runtime ID expansion before allocating duplicate event maps.
			size += len(id)*6 + len(`"runtime-id":""`) + 1
			if size > testRequestLimit {
				return batchCandidate{}
			}
		}
		eventCopy, contentCopy, metaCopy := maps.Clone(event), maps.Clone(content), maps.Clone(meta)
		metaCopy["runtime-id"] = id
		contentCopy["meta"] = metaCopy
		eventCopy["content"] = contentCopy
		events = append(events, eventCopy)
	}
	for _, scope := range testEventTypes {
		delete(object(scopes[scope]), "runtime-id")
	}
	delete(object(scopes["span"]), "runtime-id")
	delete(object(scopes["*"]), "runtime-id")
	envelope := maps.Clone(test.envelope)
	envelope["metadata"] = scopes
	key, err := encodeJSON(envelope)
	if err != nil {
		return batchCandidate{}
	}
	object(scopes["*"])["runtime-id"] = globalID
	return batchCandidate{key: string(key), envelope: envelope, events: events, size: size}
}

func compactSharedMetadata(envelope Object, events []any) {
	scopes := object(envelope["metadata"])
	global := object(scopes["*"])
	meta := make([]Object, 0, len(events))
	for _, value := range events {
		event := object(value)
		kind := stringValue(event, "type")
		if !testEvent(kind) && kind != "span" {
			return
		}
		meta = append(meta, object(object(event["content"])["meta"]))
	}
	for key, value := range meta[0] {
		text, ok := value.(string)
		if !ok || !sharesMetadataKey(key) {
			continue
		}
		if existing, exists := global[key]; exists && existing != text {
			continue
		}
		// Fixed-type scope has different precedence from event metadata.
		shadowed := false
		for scope, fields := range scopes {
			if _, exists := object(fields)[key]; scope != "*" && exists {
				shadowed = true
				break
			}
		}
		if shadowed || slices.ContainsFunc(meta, func(fields Object) bool { return fields[key] != text }) {
			continue
		}
		global[key] = text
		for _, fields := range meta {
			delete(fields, key)
		}
	}
}

func sharesMetadataKey(key string) bool {
	return strings.HasPrefix(key, "bazel.") || strings.HasPrefix(key, "git.") ||
		strings.HasPrefix(key, "ci.") || strings.HasPrefix(key, "os.") || strings.HasPrefix(key, "runtime.")
}

// batchBody encodes the batch as one request. It reports false when the
// request would exceed its limit or the schema rejects it.
func (work *worker) batchBody(batch []batchEntry) ([]byte, bool) {
	envelope := batch[0].candidate.envelope
	var events []any
	for _, source := range batch {
		events = append(events, source.candidate.events...)
	}
	compactSharedMetadata(envelope, events)
	doc := maps.Clone(envelope)
	doc["events"] = events
	body, err := encodeJSON(doc)
	return body, err == nil && len(body) <= testRequestLimit && work.schema.accepts(doc)
}

func (work *worker) flushBatch(ctx context.Context, batch []batchEntry, encoded func()) {
	if len(batch) == 0 {
		return
	}
	if len(batch) == 1 {
		work.uploadTest(ctx, batch[0].index, &batch[0].test, encoded)
		return
	}
	body, ok := work.batchBody(batch)
	if !ok {
		// Each source drops its own data as it is sent.
		for i := range batch {
			work.uploadTest(ctx, batch[i].index, &batch[i].test, func() {})
		}
		return
	}
	// The body holds the batch now. The sources stay on disk until they are
	// delivered, so the decoded data can go while the request waits.
	headers := batch[0].test.headers
	for i := range batch {
		batch[i].test, batch[i].candidate = preparedTest{}, batchCandidate{}
	}
	encoded()
	delivery := work.send.post(ctx, work.testRequest(body, headers), work.compressor)
	first := &work.results[batch[0].index]
	first.record(delivery)
	first.SharedRequests++
	// Only a definitive batch rejection permits replay at a source boundary.
	if delivery.Status == 400 || delivery.Status == 413 || delivery.Status == 422 {
		for _, entry := range batch {
			work.results[entry.index].warn("batch_rejected_then_source_fallback")
			if test, ok := work.readTest(entry.index); ok {
				work.uploadTest(ctx, entry.index, &test, func() {})
			}
		}
		return
	}
	for _, entry := range batch {
		result := &work.results[entry.index]
		result.ChunksCreated = 1
		if work.options.DryRun && delivery.Err == nil {
			result.Status = statusSucceeded
		} else if delivery.Success {
			result.ChunksUploaded = 1
			work.finish(entry.index)
		} else {
			work.failDelivery(result, delivery)
			result.ChunksFailed = 1
		}
	}
}
