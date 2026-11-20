package bazelupload

import (
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf16"
)

const inputPayload = `{
    "version": 1,
    "metadata": {"*": {"runtime-id":"process-a","language":"go","library_version":"fixture"}},
    "events": [{"type":"test","version":2,"content":{
        "trace_id":18446744073709551615,"span_id":9007199254740993,"test_session_id":3,"test_module_id":4,"test_suite_id":5,
        "name":"go.test","service":"original","resource":"fixture","type":"test","start":1000000000000000000,"duration":1,"error":0,
        "meta":{"test.name":"fixture","test.status":"pass","test.source.file":"pkg/thing_test.go"}
    }}]
}`

func testSource(id, file string) Source {
	return Source{ID: id, Path: file, Kind: Test, Context: "ctx", Metadata: Object{
		"bazel.test_optimization.service_name": "reporting",
		"bazel.test_optimization.runtime_name": "go",
		"bazel.test_optimization.repo_name":    "repo",
		"bazel.target":                         "//pkg:fixture.topt",
		"bazel.package":                        "pkg",
	}}
}

func testPlan(sources ...Source) Plan {
	return Plan{Version: 1, RulesVersion: "rule-version", RuntimeID: "fallback",
		Codeowners: "CODEOWNERS", Sources: sources, Contexts: map[string]Object{"ctx": {
			"service.name": "reporting", "runtime.name": "go", "topt.sync.repository_name": "repo",
			"git.commit.sha": "abcdef", "git.repository_url": "https://example.org/repo",
			"topt.api_key_fingerprint": "private",
		}},
	}
}

func writeFixture(t testing.TB, root, name, value string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runFixture(t testing.TB, directory string, plan Plan, options Options) Report {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	report, err := upload(context.Background(), root, plan, options)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestUploadParity(t *testing.T) {
	directory := t.TempDir()
	golden, err := os.ReadFile("testdata/enriched.json")
	if err != nil {
		t.Fatal(err)
	}
	want, err := decodeObject(golden)
	if err != nil {
		t.Fatal(err)
	}

	writeFixture(t, directory, "CODEOWNERS", "*.go @base\n/pkg/** @test-team @test-team\n")
	writeFixture(t, directory, "test.json", inputPayload)
	writeFixture(t, directory, "coverage.json", `[{"covered":"pkg/thing.go"}]`)
	writeFixture(t, directory, "telemetry.json", `{"api_version":"v2","request_type":"generate-metrics","application":{"service_name":"original","language_name":"go"},"payload":{"series":[{"tags":["provider:bazel"]}]}}`)
	seen := make(map[string]bool)
	var seenMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		seenMu.Lock()
		defer seenMu.Unlock()
		seen[request.URL.Path] = true
		if request.Header.Get("DD-API-KEY") != "local-test" {
			t.Error("expected the explicit agentless test credential")
		}
		switch request.URL.Path {
		case "/api/v2/citestcycle":
			reader, err := gzip.NewReader(request.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			raw, err := io.ReadAll(reader)
			_ = reader.Close()
			got, parseErr := decodeObject(raw)
			if err != nil || parseErr != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("test payload differs from Python fixture: %s", raw)
			}
		case "/api/v2/citestcov":
			reader, err := request.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			for _, spec := range []struct{ name, filename, value string }{
				{"event", "fileevent.json", `{"dummy":true}`},
				{"coveragex", "filecoveragex.json", `[{"covered":"pkg/thing.go"}]`},
			} {
				part, err := reader.NextPart()
				if err != nil {
					t.Error(err)
					break
				}
				checkPart(t, part, spec.name, spec.filename, spec.value)
			}
		case "/api/v2/apmtelemetry":
			raw, _ := io.ReadAll(request.Body)
			payload, err := decodeObject(raw)
			if err != nil || request.Header.Get("DD-Telemetry-API-Version") != "v2" ||
				stringValue(object(payload["application"]), "service_name") != "reporting" ||
				stringValue(object(payload["application"]), "env") != "ci" ||
				!strings.Contains(string(raw), "provider:bazel/go") {
				t.Errorf("unexpected telemetry: %s", raw)
			}
		default:
			t.Errorf("unexpected request %s", request.URL.Path)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	telemetry := testSource("telemetry", "telemetry.json")
	telemetry.Kind = Telemetry
	telemetry.Telemetry = &TelemetryDirective{Environment: "ci", ProviderSuffix: "go"}
	plan := testPlan(testSource("test", "test.json"),
		Source{ID: "coverage", Path: "coverage.json", Kind: Coverage}, telemetry)
	options := Options{Gzip: true, Workers: 3, AgentlessURL: server.URL, APIKey: "local-test",
		ExpectedTags: []string{"bazel.target", "git.commit.sha"}}
	report := runFixture(t, directory, plan, options)
	if report.ExitCode != 0 || report.Files.Deleted != 3 || report.Requests.Succeeded != 3 || len(report.Warnings) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	seenMu.Lock()
	defer seenMu.Unlock()
	if len(seen) != 3 {
		t.Fatalf("expected three intake endpoints, got %v", seen)
	}
	for _, source := range plan.Sources {
		if _, err := os.Stat(filepath.Join(directory, source.Path)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("acknowledged source is still present: %s", source.Path)
		}
	}
}

func checkPart(t *testing.T, part *multipart.Part, name, filename, value string) {
	t.Helper()
	body, err := io.ReadAll(part)
	if err != nil || part.FormName() != name || part.FileName() != filename || string(body) != value {
		t.Errorf("unexpected coverage part %s/%s %s, %v", part.FormName(), part.FileName(), body, err)
	}
}

func TestBatchRejectionRetainsOneSource(t *testing.T) {
	for _, rejectBatch := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[rejectBatch], func(t *testing.T) {
			directory := t.TempDir()
			writeFixture(t, directory, "CODEOWNERS", "")
			writeFixture(t, directory, "first.json", inputPayload)
			writeFixture(t, directory, "second.json", strings.ReplaceAll(inputPayload, "process-a", "process-b"))
			var calls []int
			var received []string
			var mu sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				raw, _ := io.ReadAll(request.Body)
				payload, err := decodeObject(raw)
				if err != nil {
					t.Error(err)
					return
				}
				events, _ := payload["events"].([]any)
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, len(events))
				if len(events) > 1 && rejectBatch {
					w.WriteHeader(http.StatusRequestEntityTooLarge)
					return
				}
				global := object(object(payload["metadata"])["*"])
				for _, event := range events {
					meta := object(object(object(event)["content"])["meta"])
					received = append(received, firstString(stringValue(meta, "runtime-id"), stringValue(global, "runtime-id")))
				}
				if len(events) == 1 && global["runtime-id"] == "process-b" {
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			plan := testPlan(testSource("a", "first.json"), testSource("b", "second.json"))
			report := runFixture(t, directory, plan, Options{Batch: true, AgentlessURL: server.URL, APIKey: "local-test"})
			mu.Lock()
			defer mu.Unlock()
			sort.Strings(received)
			if !reflect.DeepEqual(received, []string{"process-a", "process-b"}) {
				t.Fatalf("lost per-event runtime ID: %v", received)
			}
			if rejectBatch {
				if !reflect.DeepEqual(calls, []int{2, 1, 1}) || report.Result != "partial_failure" ||
					report.Files.Deleted != 1 || report.Files.Retained != 1 || report.Requests.Attempted != 3 {
					t.Fatalf("unexpected fallback: %v, %+v", calls, report)
				}
				got, err := os.ReadFile(filepath.Join(directory, "second.json"))
				if err != nil || string(got) != strings.ReplaceAll(inputPayload, "process-a", "process-b") {
					t.Fatalf("rejected source was changed: %s %v", got, err)
				}
			} else if !reflect.DeepEqual(calls, []int{2}) || report.Files.Deleted != 2 ||
				report.Requests.Attempted != 1 || report.SharedRequests != 1 {
				t.Fatalf("unexpected shared batch: %v, %+v", calls, report)
			}
		})
	}
}

func TestDryRunAndEnrichmentFailures(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	writeFixture(t, directory, "valid.json", inputPayload)
	writeFixture(t, directory, "wrong-service.json", inputPayload)
	writeFixture(t, directory, "malformed.json", strings.ReplaceAll(inputPayload, "\"duration\":1", "\"duration\":\"invalid\""))
	mismatch := testSource("wrong", "wrong-service.json")
	mismatch.Metadata["bazel.test_optimization.service_name"] = "other-service"
	plan := testPlan(testSource("valid", "valid.json"), mismatch, testSource("invalid", "malformed.json"))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		t.Error("dry-run attempted an HTTP request")
		w.WriteHeader(500)
	}))
	defer server.Close()
	report := runFixture(t, directory, plan, Options{DryRun: true, AgentlessURL: server.URL})
	if report.Result != "partial_failure" || report.Requests.Planned != 2 || report.Requests.Attempted != 0 ||
		report.Files.Deleted != 0 || report.Sources[1].Failure != "reporting_service_identity_mismatch" ||
		report.Warnings["schema_validation_failed"] != 1 {
		t.Fatalf("unexpected dry-run report: %+v", report)
	}
	for _, source := range plan.Sources {
		if _, err := os.Stat(filepath.Join(directory, source.Path)); err != nil {
			t.Errorf("dry run touched %s: %v", source.Path, err)
		}
	}
}

func TestSplitRetainsOnlyRejectedEvents(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	payload, err := decodeObject([]byte(inputPayload))
	if err != nil {
		t.Fatal(err)
	}
	event := object(payload["events"].([]any)[0])
	meta := object(object(event["content"])["meta"])
	meta["test.name"] = "accepted"
	meta["blob"] = strings.Repeat("x", testRequestLimit/2+100)
	copyEvent := objectField(event, "content")
	otherContent := make(Object)
	for key, value := range copyEvent {
		otherContent[key] = value
	}
	otherContent["meta"] = Object{"test.name": "rejected", "blob": meta["blob"]}
	payload["events"] = []any{event, Object{"type": "test", "version": json.Number("2"), "content": otherContent}}
	raw, err := encodeJSON(payload)
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, directory, "split.json", string(raw))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		if len(raw) > testRequestLimit {
			t.Error("request exceeded the size limit")
		}
		if strings.Contains(string(raw), `"test.name":"rejected"`) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	report := runFixture(t, directory, testPlan(testSource("split", "split.json")),
		Options{AgentlessURL: server.URL, APIKey: "local-test"})
	result := report.Sources[0]
	if report.Result != "partial_failure" || result.ChunksCreated != 2 || result.ChunksUploaded != 1 || result.ChunksFailed != 1 {
		t.Fatalf("unexpected split report: %+v", report)
	}
	retained, err := os.ReadFile(filepath.Join(directory, "split.json"))
	if err != nil || strings.Contains(string(retained), `"test.name":"accepted"`) ||
		!strings.Contains(string(retained), `"test.name":"rejected"`) {
		t.Fatalf("replay did not preserve exactly the rejected events: %v", err)
	}
}

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestRetriesDoNotSkipTheSource(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		attempts := 0
		start := time.Now()
		wantDelays := []time.Duration{0, 3 * time.Second, 63 * time.Second}
		send := sender{attempts: maxAttempts, client: &http.Client{Transport: testTransport(func(request *http.Request) (*http.Response, error) {
			if attempts >= len(wantDelays) || time.Since(start) != wantDelays[attempts] {
				t.Errorf("retry %d started at %v", attempts, time.Since(start))
			}
			attempts++
			defer func() { _ = request.Body.Close() }()
			body, _ := io.ReadAll(request.Body)
			if string(body) != "fixture" {
				t.Errorf("different retry body: %s", body)
			}
			status := http.StatusTooManyRequests
			header := make(http.Header)
			switch attempts {
			case 1:
				header.Set("Retry-After", "3")
			case 2:
				header.Set("Retry-After", time.Now().Add(65*time.Second).UTC().Format(http.TimeFormat))
			case 3:
				status = http.StatusAccepted
			}
			return &http.Response{StatusCode: status, Header: header, Body: http.NoBody}, nil
		})}}
		result := send.post(context.Background(), outbound{url: "http://localhost", body: []byte("fixture")}, nil)
		if result.Attempts != 3 || !result.Success {
			t.Fatalf("unexpected retry result: %+v", result)
		}
	})
}

func TestReadPlanRejectsUnsafePathsAndDuplicates(t *testing.T) {
	const template = `{"schema_version":1,"rules_version":"x","runtime_id":"x","sources":SOURCES}`
	for _, sources := range []string{
		`[{"id":"1","path":"../outside","kind":"test"}]`,
		`[{"id":"1","path":"payload.json","kind":"test"},{"id":"2","path":"./payload.json","kind":"test"}]`,
		`[{"id":"1","path":"payload.json","kind":"test","context":"unknown"}]`,
	} {
		if _, err := readPlan(strings.NewReader(strings.Replace(template, "SOURCES", sources, 1))); err == nil {
			t.Errorf("accepted unsafe or ambiguous plan %s", sources)
		}
	}
}

func TestDifferentContextsRemainSeparate(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	writeFixture(t, directory, "first.json", inputPayload)
	writeFixture(t, directory, "second.json", inputPayload)
	plan := testPlan(testSource("a", "first.json"), testSource("b", "second.json"))
	plan.Contexts["separate"] = plan.Contexts["ctx"]
	plan.Sources[1].Context = "separate"
	report := runFixture(t, directory, plan, Options{DryRun: true, Batch: true})
	if report.ExitCode != 0 || report.Requests.Planned != 2 || report.SharedRequests != 0 {
		t.Fatalf("sources from separate contexts were combined: %+v", report)
	}
}

func TestSchemaValidatesExactIDs(t *testing.T) {
	schema, err := compileSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  string
		want bool
	}{
		{"valid uint64 maximum", inputPayload, true},
		{"uint64 overflow", strings.Replace(inputPayload, "18446744073709551615", "18446744073709551616", 1), false},
		{"wrong envelope version", strings.Replace(inputPayload, `"version": 1`, `"version": 2`, 1), false},
		{"valid nested metrics", strings.Replace(inputPayload, `"meta":{`, `"metrics":{"memory":{"rss":42}},"meta":{`, 1), true},
		{"invalid nested metrics", strings.Replace(inputPayload, `"meta":{`, `"metrics":{"memory":{"rss":"invalid"}},"meta":{`, 1), false},
		{"missing runtime ID", strings.Replace(inputPayload, `"runtime-id":"process-a",`, "", 1), false},
		{"unknown event", strings.Replace(inputPayload, `"type":"test"`, `"type":"unknown"`, 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := decodeObject([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if got := schema.accepts(payload); got != tc.want {
				t.Errorf("schema accepted = %t, want %t", got, tc.want)
			}
		})
	}
}

func TestMessagePackCoverageThroughEVP(t *testing.T) {
	directory := t.TempDir()
	const messagePack = "\x81\xa5files\x90"
	writeFixture(t, directory, "coverage.msgpack", messagePack)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/evp_proxy/v2/api/v2/citestcov" ||
			request.Header.Get("X-Datadog-EVP-Subdomain") != "citestcov-intake" ||
			request.Header.Get("DD-API-KEY") != "" {
			t.Error("unexpected coverage routing or credentials")
		}
		multipart, err := request.MultipartReader()
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		event, err := multipart.NextPart()
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		checkPart(t, event, "event", "fileevent.json", `{"dummy":true}`)
		data, err := multipart.NextPart()
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		checkPart(t, data, "coveragex", "filecoveragex.msgpack", messagePack)
		if data.Header.Get("Content-Type") != "application/msgpack" {
			t.Error("expected MessagePack part")
		}
		response.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	plan := testPlan(Source{ID: "coverage", Path: "coverage.msgpack", Kind: Coverage})
	plan.Codeowners = ""
	report := runFixture(t, directory, plan, Options{AgentURL: server.URL, APIKey: "unused"})
	if report.Files.Deleted != 1 || report.Requests.Succeeded != 1 {
		t.Fatalf("unexpected EVP upload report: %+v", report)
	}
}

func TestCodeownersFromStagedGitLabFile(t *testing.T) {
	directory := t.TempDir()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	const source = "[Backend] @backend\r\n/pkg/**\r\n[Review]\r\n/pkg/** @review\r\n"
	data := []byte{0xff, 0xfe}
	for _, codeUnit := range utf16.Encode([]rune(source)) {
		data = binary.LittleEndian.AppendUint16(data, codeUnit)
	}
	if err := root.WriteFile("staged-owners", data, 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := readOwners(root, "staged-owners", "gitlab")
	if err != nil {
		t.Fatal(err)
	}
	lookup := ownerLookup{rules: rules, workspace: directory, cache: make(map[[2]string]string)}
	meta := Object{"test.source.file": "/tmp/execroot/example/_main/pkg/test.go"}
	lookup.enrich(nil, meta, "")
	if got := meta["test.codeowners"]; got != `["@backend","@review"]` {
		t.Fatalf("test.codeowners = %s", got)
	}
}

func TestProducerGlobalMetadataSurvivesEnrichment(t *testing.T) {
	payload, err := decodeObject([]byte(inputPayload))
	if err != nil {
		t.Fatal(err)
	}
	global := object(object(payload["metadata"])["*"])
	global["language_version"] = "1.27"
	global["language_interpreter"] = "go-arm64"
	global["library_name"] = "ddtest"
	global["git.repository_url"] = "https://example.org/producer"
	plan := testPlan()
	if err := enrich(payload, Source{}, plan, flatMetadata{}, &ownerLookup{}); err != nil {
		t.Fatal(err)
	}
	enriched := object(object(payload["metadata"])["*"])
	if !reflect.DeepEqual(enriched, global) {
		t.Fatalf("producer metadata did not survive enrichment: %v", enriched)
	}
	headers := requestHeaders(plan, enriched, "", "")
	if headers.Get("Datadog-Meta-Lang-Version") != "1.27" ||
		headers.Get("Datadog-Meta-Lang-Interpreter") != "go-arm64" {
		t.Fatalf("missing producer headers: %v", headers)
	}
}

func TestPartialTelemetryReplayKeepsOnlyRejectedRequest(t *testing.T) {
	for _, tc := range []struct {
		name, rejectedKind string
		bom                bool
	}{
		{"primary rejected", "message-batch", false},
		{"synthetic rejected", "generate-metrics", false},
		{"BOM-prefixed primary rejected", "message-batch", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directory := t.TempDir()
			input := `{"api_version":"v2","request_type":"message-batch","application":{},"payload":[{"request_type":"app-started","payload":{}}]}`
			directive := &TelemetryDirective{
				Synthetic: Object{"api_version": "v2", "request_type": "generate-metrics", "payload": Object{}},
			}
			if tc.bom {
				input = utf8BOM + input
			} else {
				directive.Environment, directive.ProviderSuffix = "ci", "go"
				directive.AppendMessages = []any{Object{"request_type": "app-closing", "payload": Object{}}}
			}
			writeFixture(t, directory, "telemetry.json", input)
			source := Source{ID: "telemetry", Path: "telemetry.json", Kind: Telemetry, Telemetry: directive}
			plan := testPlan(source)
			plan.Codeowners = ""
			type received struct {
				kind, session string
				payload       Object
			}
			var requests []received
			var lock sync.Mutex
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				data, _ := io.ReadAll(request.Body)
				payload, err := decodeObject(data)
				if err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				kind := stringValue(payload, "request_type")
				lock.Lock()
				defer lock.Unlock()
				requests = append(requests, received{kind, request.Header.Get("DD-Session-ID"), payload})
				if len(requests) <= 2 && kind == tc.rejectedKind {
					w.WriteHeader(http.StatusUnprocessableEntity)
					return
				}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			options := Options{AgentlessURL: server.URL, APIKey: "local-test"}
			first := runFixture(t, directory, plan, options)
			if first.Result != "partial_failure" || first.Requests.Succeeded != 1 || first.Files.Deleted != 0 {
				t.Fatalf("unexpected first upload: %+v", first)
			}
			plan.RuntimeID = "new-invocation"
			second := runFixture(t, directory, plan, options)
			if second.Result != "success" || second.Requests.Attempted != 1 || second.Files.Deleted != 1 {
				t.Fatalf("unexpected replay: %+v", second)
			}
			lock.Lock()
			defer lock.Unlock()
			if len(requests) != 3 || requests[2].kind != tc.rejectedKind {
				t.Fatalf("unexpected delivered sequence: %+v", requests)
			}
			original := 0
			if tc.rejectedKind == "generate-metrics" {
				original = 1
			}
			if !reflect.DeepEqual(requests[original], requests[2]) {
				t.Fatalf("replay did not preserve the prepared request: %+v", requests)
			}
		})
	}
}
