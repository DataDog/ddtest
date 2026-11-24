package bazelupload

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// streamWorker is a worker over root, as upload builds one.
func streamWorker(t testing.TB, root string, plan Plan, options Options) *worker {
	t.Helper()
	dir, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	if err := checkPlan(&plan); err != nil {
		t.Fatal(err)
	}
	urls, err := resolveEndpoints(options)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compileSchema()
	if err != nil {
		t.Fatal(err)
	}
	rules, err := readOwners(dir, plan.Codeowners, plan.CodeownersDialect)
	if err != nil {
		t.Fatal(err)
	}
	contexts := map[string]flatMetadata{}
	for name, values := range plan.Contexts {
		contexts[name] = flatten(values, true)
	}
	results := make([]FileResult, len(plan.Sources))
	for i, source := range plan.Sources {
		results[i] = FileResult{ID: source.ID, Path: source.Path, Kind: source.Kind}
	}
	inv := &invocation{root: dir, plan: plan, contexts: contexts, options: options, urls: urls, schema: schema,
		results: results, files: make([]os.FileInfo, len(results)), directories: newDirectories(dir),
		held: make([]*directory, len(results))}
	return &worker{invocation: inv, owners: ownerLookup{rules: rules, workspace: dir.Name(), cache: map[[2]string]string{}},
		compressor: gzip.NewWriter(io.Discard), encoder: newJSONEncoder()}
}

type preparedView struct {
	ok      bool
	result  FileResult
	bodies  []string
	ranges  [][2]int
	headers map[string][]string
}

func prepareWith(t testing.TB, root string, plan Plan, options Options, streaming bool) preparedView {
	t.Helper()
	work := streamWorker(t, root, plan, options)
	var test preparedTest
	var ok bool
	if streaming {
		test, ok = work.streamTest(0)
	} else {
		test, ok = work.readTest(0)
		if ok && test.chunks == nil {
			ok = work.chunk(0, &test)
		}
	}
	work.closeDirectory(0)
	view := preparedView{ok: ok, result: work.results[0], headers: test.headers}
	for _, chunk := range test.chunks {
		view.bodies = append(view.bodies, string(chunk.body))
		view.ranges = append(view.ranges, [2]int{chunk.first, chunk.end})
	}
	return view
}

// compareStreaming requires both preparations of payload to be identical.
func compareStreaming(t testing.TB, payload string, options Options) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "CODEOWNERS", "*.go @team/go\npkg/ @team/pkg\n")
	writeFixture(t, root, "events.json", payload)
	source := testSource("one", "events.json")
	plan := testPlan(source)
	plan.Contexts["ctx"]["ci.workspace_path"] = root
	plan.Contexts["ctx"]["custom.number"] = json.Number("7")
	plan.Contexts["ctx"]["custom.flag"] = true
	old := prepareWith(t, root, plan, options, false)
	streamed := prepareWith(t, root, plan, options, true)
	if !reflect.DeepEqual(old, streamed) {
		t.Fatalf("payload %q\nold      %+v\nstreamed %+v", truncateText(payload), old, streamed)
	}
	lastView = old
}

// lastView is the last comparison's preparation, for tests that check that
// a comparison prepared requests.
var lastView preparedView

func truncateText(text string) string {
	if len(text) > 300 {
		return text[:300] + "..."
	}
	return text
}

func streamEvent(content string) string {
	return `{"type":"test","version":2,"content":` + content + `}`
}

const streamContent = `{"trace_id":18446744073709551615,"span_id":9007199254740993,"test_session_id":3,` +
	`"test_module_id":4,"test_suite_id":5,"name":"go.test","service":"original","resource":"fixture","type":"test",` +
	`"start":1000000000000000000,"duration":1,"error":0,` +
	`"meta":{"test.name":"fixture","test.status":"pass","test.source.file":"pkg/thing_test.go"},"metrics":{"x":1.50}}`

func streamPayload(events ...string) string {
	return `{"version":1,"metadata":{"*":{"runtime-id":"process-a","language":"go","library_version":"fixture"},` +
		`"test":{"k":"v"}},"events":[` + strings.Join(events, ",") + `]}`
}

func TestStreamingMatchesDecoding(t *testing.T) {
	cases := map[string]string{
		"ordinary": streamPayload(streamEvent(streamContent)),
		"several":  streamPayload(streamEvent(streamContent), streamEvent(streamContent), `{"type":"span","content":{"type":"test"}}`),
		"unsorted and spaced keys": "\ufeff" + ` { "events" : [ { "content" : { "z":1, "a" : "b", "meta" : { "b":"1","a":"2" } },` +
			` "version":2, "type":"test" } ], "version" : 1, "metadata":{"*":{"language":"go","runtime-id":"r"}} } `,
		"duplicate keys":      `{"version":1,"events":[{"type":"x","type":"test","content":{"meta":{"a":"1","a":"2"},"meta":{"b":"3"}}}],"events":[{"type":"test","content":{}}]}`,
		"escapes":             streamPayload(streamEvent(`{"meta":{"q\"uote":"a\\b\/c\u00e9\u2028\n","\u0041":"x"},"name":"\ud83d\ude00"}`)),
		"line separator":      streamPayload(streamEvent("{\"meta\":{\"k\":\"a\u2028b\u2029c\"}}")),
		"non-ASCII keys":      streamPayload(streamEvent("{\"meta\":{\"\uffff\":\"1\",\"\U0001F600\":\"2\",\"\ue000\":\"3\",\"\u00e9\":\"4\"}}")),
		"missing content":     streamPayload(`{"type":"test","version":2}`),
		"content not object":  streamPayload(`{"type":"test","content":[1,2]}`, `{"type":"test","content":{"meta":"x","metrics":null}}`),
		"non-object events":   streamPayload(`1`, `"x"`, `null`, `[{"b":1,"a":2}]`, streamEvent(streamContent)),
		"numbers":             streamPayload(streamEvent(`{"metrics":{"a":-0,"b":1e10,"c":1.0E-5,"d":123456789012345678901234567890},"error":0}`)),
		"nested":              streamPayload(streamEvent(`{"meta":{"a":"b"},"nested":{"z":[{"y":1,"x":[true,false,null]}],"a":{}}}`)),
		"existing codeowners": streamPayload(streamEvent(`{"meta":{"test.codeowners":"@x","test.source.file":"pkg/a.go"}}`)),
		"source object":       streamPayload(`{"type":"test_suite_end","content":{"source":{"file":"pkg/b.go"}}}`),
		"span keeps owners":   streamPayload(`{"type":"span","content":{"type":"test","meta":{"test.source.file":"pkg/a.go"}}}`),
		"empty session": streamPayload(`{"type":"test_session_end","content":{"test_session_id":7,"error":0,` +
			`"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":0}}}`),
		"session not empty": streamPayload(`{"type":"test_session_end","content":{"test_session_id":7,"error":0,` +
			`"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":0.0}}}`),
		"no events":       `{"version":1,"events":[]}`,
		"events missing":  `{"version":1}`,
		"events not list": `{"version":1,"events":{"a":1}}`,
		"not an object":   `[1,2]`,
		"null":            `null`,
		"trailing":        `{"events":[]} {}`,
		"truncated":       `{"events":[{"type":"test"`,
		"invalid escape":  `{"events":[{"type":"te\qst"}]}`,
		"control char":    "{\"events\":[{\"type\":\"te\x01st\"}]}",
		"leading zero":    `{"events":[{"n":01}]}`,
		"bare minus":      `{"events":[{"n":-}]}`,
		"invalid UTF-8":   "{\"events\":[{\"type\":\"\xff\"}]}",
		"schema metadata": `{"version":1,"metadata":{"*":{"runtime-id":7},"extra":{}},"events":[` + streamEvent(streamContent) + `]}`,
	}
	prepares := map[string]bool{"ordinary": true, "several": true, "unsorted and spaced keys": true, "duplicate keys": true,
		"escapes": true, "line separator": true, "non-ASCII keys": true, "missing content": true, "numbers": true,
		"empty session": true, "non-object events": true}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			compareStreaming(t, payload, Options{DryRun: true})
			if prepares[name] && (!lastView.ok || len(lastView.bodies) == 0 || lastView.result.Events == 0) {
				t.Fatalf("no requests prepared: %+v", lastView)
			}
			compareStreaming(t, payload, Options{DryRun: true, ExpectedTags: []string{"git.commit.sha", "bazel.target", "missing.tag"}})
			compareStreaming(t, payload, Options{DryRun: true, ExpectedTags: defaultExpectedTags})
		})
	}
}

// Large sources split into the same requests.
func TestStreamingMatchesDecodingChunks(t *testing.T) {
	big := strings.Repeat("x", 600_000)
	var events []string
	for range 20 {
		events = append(events, streamEvent(`{"meta":{"payload":"`+big+`"}}`))
	}
	compareStreaming(t, streamPayload(events...), Options{DryRun: true})
	compareStreaming(t, streamPayload(streamEvent(`{"meta":{"payload":"`+strings.Repeat("y", 5_000_000)+`"}}`)), Options{DryRun: true})
}

func FuzzStreamingMatchesDecoding(f *testing.F) {
	for _, seed := range []string{streamPayload(streamEvent(streamContent)), `{"events":[{"type":"span","content":{"type":"test"}}]}`,
		`{"events":[1,{"content":{"meta":{"a":"\u2028"}}}]}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, payload string) {
		compareStreaming(t, payload, Options{DryRun: true, ExpectedTags: []string{"bazel.target"}})
	})
}

func TestStreamableSchema(t *testing.T) {
	schema, err := compileSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !schema.streamable() {
		t.Fatal("the embedded schema checks its events one at a time")
	}
	if strings.Contains(string(bytes.TrimSpace(testSchemaJSON)), "minItems") {
		t.Fatal("events constraints beyond items need the decoding path")
	}
	_ = filepath.Join
}
