package bazelupload

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

const testRequestLimit = 4_718_592

//go:embed agentless-schema.json
var testSchemaJSON []byte

type preparedTest struct {
	envelope Object
	events   []any
	// chunks are the source's own requests. A source that can share a request
	// builds them only if it is sent alone.
	chunks  []testChunk
	headers http.Header
	// estimate bounds the source's events in a shared request, before shared
	// metadata moves to the envelope.
	estimate int
}

type testChunk struct {
	body       []byte
	first, end int
}

func prepareChunks(envelope Object, events []any, limit, sizeHint int, encoding *jsonEncoder) ([]testChunk, error) {
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
	for index, event := range events {
		raw, err := encoding.encode(event)
		if err != nil {
			return nil, err
		}
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

func coverageBody(raw []byte, msgpack bool) ([]byte, string, error) {
	var buf bytes.Buffer
	buf.Grow(len(raw) + 500)
	writer := multipart.NewWriter(&buf)
	filename, contentType := "filecoveragex.json", "application/json"
	if msgpack {
		filename, contentType = "filecoveragex.msgpack", "application/msgpack"
	}
	for _, part := range []struct {
		name, filename, mime string
		data                 []byte
	}{
		{"event", "fileevent.json", "application/json", []byte(`{"dummy":true}`)},
		{"coveragex", filename, contentType, raw},
	} {
		headers := make(textproto.MIMEHeader)
		headers.Set("Content-Type", part.mime)
		headers.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, part.name, part.filename))
		dest, err := writer.CreatePart(headers)
		if err != nil {
			return nil, "", err
		}
		if _, err := dest.Write(part.data); err != nil {
			return nil, "", err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), writer.FormDataContentType(), nil
}

// A replay envelope contains the prepared request that is still pending.
const telemetryReplayKey = "_ddtest_bazel_telemetry_replay_v1"

type telemetryReplay struct {
	Body      json.RawMessage `json:"body"`
	SessionID string          `json:"session_id"`
}

func telemetryBody(raw []byte, payload Object, plan Plan, source Source, urls endpoints, apiKey string) ([]outbound, error) {
	service, err := reportingService(plan.Contexts[source.Context], source.Metadata)
	if err != nil {
		return nil, err
	}
	if retained, ok := payload[telemetryReplayKey]; ok && len(payload) == 1 {
		replay := object(retained)
		prepared := object(replay["body"])
		sessionID := stringValue(replay, "session_id")
		if prepared == nil || sessionID == "" {
			return nil, fmt.Errorf("invalid retained telemetry request")
		}
		raw, err := encodeJSON(prepared)
		if err != nil {
			return nil, err
		}
		request, err := telemetryRequest(prepared, append(raw, '\n'), sessionID, urls.telemetry, apiKey)
		if err != nil {
			return nil, err
		}
		return []outbound{request}, nil
	}

	directive := source.Telemetry
	changed := false
	if app := object(payload["application"]); app != nil {
		if service != "" && stringValue(app, "service_name") != service {
			app["service_name"] = service
			changed = true
		}
		if directive != nil && directive.Environment != "" {
			app["env"] = directive.Environment
			changed = true
		}
	}
	if directive != nil {
		if messages, ok := payload["payload"].([]any); ok && len(directive.AppendMessages) != 0 {
			payload["payload"] = append(messages, directive.AppendMessages...)
			changed = true
		}
		if directive.ProviderSuffix != "" {
			rewriteProvider(payload, directive.ProviderSuffix)
			changed = true
		}
	}
	if changed {
		raw, err = encodeJSON(payload)
		if err != nil {
			return nil, err
		}
		raw = append(raw, '\n')
	}
	primary, err := telemetryRequest(payload, raw, plan.RuntimeID, urls.telemetry, apiKey)
	if err != nil {
		return nil, err
	}
	requests := []outbound{primary}
	if directive != nil && directive.Synthetic != nil {
		// The caller supplies the already selected and resolved replay envelope.
		synthetic := directive.Synthetic
		if directive.ProviderSuffix != "" {
			rewriteProvider(synthetic, directive.ProviderSuffix)
		}
		body, err := encodeJSON(synthetic)
		if err != nil {
			return nil, err
		}
		request, err := telemetryRequest(synthetic, append(body, '\n'), plan.RuntimeID, urls.telemetry, apiKey)
		if err != nil {
			return nil, err
		}
		requests = append(requests, request)
	}
	return requests, nil
}

func telemetryRequest(payload Object, body []byte, runtimeID, endpoint, apiKey string) (outbound, error) {
	version, kind := stringValue(payload, "api_version"), stringValue(payload, "request_type")
	if version == "" || kind == "" {
		return outbound{}, fmt.Errorf("telemetry requires api_version and request_type")
	}
	application := object(payload["application"])
	headers := http.Header{"Content-Type": {"application/json"}}
	headers.Set("DD-Telemetry-API-Version", version)
	headers.Set("DD-Telemetry-Request-Type", kind)
	headers.Set("DD-Session-ID", firstString(stringValue(payload, "runtime_id"), runtimeID))
	if lang := stringValue(application, "language_name"); lang != "" {
		headers.Set("DD-Client-Library-Language", lang)
	}
	if version := stringValue(application, "tracer_version"); version != "" {
		headers.Set("DD-Client-Library-Version", version)
	}
	if apiKey != "" {
		headers.Set("DD-API-KEY", apiKey)
	}
	return outbound{body: bytes.TrimPrefix(body, []byte(utf8BOM)), url: endpoint, headers: headers}, nil
}

func rewriteProvider(payload Object, suffix string) {
	kind := stringValue(payload, "request_type")
	if kind == "message-batch" {
		messages, _ := payload["payload"].([]any)
		for _, message := range messages {
			if fields := object(message); fields != nil {
				rewriteProvider(fields, suffix)
			}
		}
	}
	if kind != "generate-metrics" && kind != "distributions" {
		return
	}
	series, _ := object(payload["payload"])["series"].([]any)
	for _, value := range series {
		tags, _ := object(value)["tags"].([]any)
		for i, tag := range tags {
			text, ok := tag.(string)
			if ok && text == "provider:bazel" {
				tags[i] = text + "/" + suffix
			}
		}
	}
}

func withoutEvents(payload Object) Object {
	envelope := maps.Clone(payload)
	delete(envelope, "events")
	return envelope
}
