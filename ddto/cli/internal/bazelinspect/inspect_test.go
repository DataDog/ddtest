package bazelinspect

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symbolic links unavailable:", err)
	}
}

func paths(payloads []Payload) []string {
	var names []string
	for _, payload := range payloads {
		names = append(names, payload.Path)
	}
	slices.Sort(names)
	return names
}

// The listing finds what the doctor's Path.rglob calls found: links to files
// count, linked directories are not entered, and other names are skipped.
func TestListingMatchesTheDoctor(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "pkg", "target", "test.outputs")
	payloads := filepath.Join(output, "payloads")
	for _, name := range []string{"tests/a.json", "tests/nested/b.json", "coverage/c.msgpack", "x/d.msgpack.gz",
		"tests/notes.txt", "nested/" + metadataName} {
		write(t, filepath.Join(payloads, name), "{}")
	}
	if err := os.MkdirAll(filepath.Join(payloads, "tests", "directory.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(output, metadataName), `{"bazel.target":"//pkg:target"}`)
	outside := filepath.Join(root, "outside")
	write(t, filepath.Join(outside, "linked.json"), "{}")
	write(t, filepath.Join(outside, "inner", "hidden.json"), "{}")
	write(t, filepath.Join(outside, "inner", metadataName), "{}")
	symlink(t, filepath.Join(outside, "linked.json"), filepath.Join(payloads, "tests", "link.json"))
	symlink(t, filepath.Join(outside, "inner"), filepath.Join(payloads, "linked_dir"))
	symlink(t, filepath.Join(root, "missing.json"), filepath.Join(payloads, "tests", "dangling.json"))

	got := Inspect([]string{output}, 2).Outputs[0]
	// A sidecar under payloads is also a *.json payload there, as for rglob.
	want := []string{
		"payloads/nested/" + metadataName,
		"payloads/tests/a.json",
		"payloads/tests/link.json",
		"payloads/tests/nested/b.json",
	}
	if !slices.Equal(want, paths(got.JSONPayloads)) {
		t.Fatalf("json payloads: got %v, want %v", paths(got.JSONPayloads), want)
	}
	msgpack := slices.Sorted(slices.Values(got.MsgpackPayloads))
	if !slices.Equal([]string{"payloads/coverage/c.msgpack", "payloads/x/d.msgpack.gz"}, msgpack) {
		t.Fatalf("msgpack payloads: %v", msgpack)
	}
	var metadata []string
	for _, sidecar := range got.Metadata {
		metadata = append(metadata, sidecar.Path)
	}
	slices.Sort(metadata)
	if !slices.Equal([]string{metadataName, "payloads/nested/" + metadataName}, metadata) {
		t.Fatalf("metadata: %v", metadata)
	}
}

// A linked payloads directory is the root of the payload listing, as
// payload_root.rglob followed it.
func TestALinkedPayloadsDirectoryIsListed(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "test.outputs")
	write(t, filepath.Join(root, "real", "tests", "a.json"), "{}")
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(root, "real"), filepath.Join(output, "payloads"))
	got := Inspect([]string{output}, 1).Outputs[0]
	if want := []string{"payloads/tests/a.json"}; !slices.Equal(want, paths(got.JSONPayloads)) {
		t.Fatalf("got %v, want %v", paths(got.JSONPayloads), want)
	}
}

// A requested path keeps its meaning on disk: link/.. leaves the link's
// target, as the doctor's os.scandir does, while lexical cleaning would read
// a sibling target's output instead.
func TestPathsAreNotCleanedBeforeTheFilesystemResolvesThem(t *testing.T) {
	root := t.TempDir()
	requested := filepath.Join(root, "real", "test.outputs")
	write(t, filepath.Join(requested, "payloads", "tests", "a.json"), `{"events":[{"type":"test","content":{}}]}`)
	write(t, filepath.Join(requested, metadataName), `{"bazel.target":"//real:a"}`)
	sibling := filepath.Join(root, "pkg", "test.outputs")
	write(t, filepath.Join(sibling, "payloads", "tests", "b.json"),
		`{"events":[{"type":"test","content":{}},{"type":"test","content":{}}]}`)
	write(t, filepath.Join(sibling, metadataName), `{"bazel.target":"//pkg:b"}`)
	if err := os.MkdirAll(filepath.Join(root, "real", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(root, "real", "deep"), filepath.Join(root, "pkg", "link"))

	path := filepath.Join(root, "pkg", "link") + string(os.PathSeparator) + ".." + string(os.PathSeparator) + "test.outputs"
	got := Inspect([]string{path}, 1).Outputs[0]
	if got.Path != path || len(got.JSONPayloads) != 1 || got.JSONPayloads[0].Path != "payloads/tests/a.json" ||
		got.JSONPayloads[0].TestEvents != 1 || len(got.Metadata) != 1 || string(got.Metadata[0].Document) != `{"bazel.target":"//real:a"}` {
		t.Fatalf("the link's target was not inspected: %+v", got)
	}
}

// testFacts agrees with test_result_event_count and
// completed_empty_test_session in topt_runtime/bep_execution.py; each case
// records what that Python returns.
func TestTestFactsMatchThePythonChecks(t *testing.T) {
	session := func(fields string) string {
		return `{"events":[{"type":"test_session_end","content":{` + fields + `}}]}`
	}
	const empty = `"test_session_id":7,"error":0,"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"},"metrics":{"test.exit_code":0}`
	for _, test := range []struct {
		name, payload string
		events        int
		empty         bool
	}{
		{"typed test", `{"events":[{"type":"test","content":{}}]}`, 1, false},
		{"legacy span", `{"events":[{"type":"span","content":{"type":"test"}}]}`, 1, false},
		{"untyped span", `{"events":[{"content":{"type":"test"}}]}`, 1, false},
		{"null type", `{"events":[{"type":null,"content":{"type":"test"}}]}`, 1, false},
		{"escaped type", `{"events":[{"type":"test","content":{}}]}`, 1, false},
		{"case differs", `{"events":[{"Type":"test","content":{}}]}`, 0, false},
		{"content is not an object", `{"events":[{"type":"test","content":[]}]}`, 0, false},
		{"suite end", `{"events":[{"type":"test_suite_end","content":{"type":"test"}}]}`, 0, false},
		{"duplicate key keeps the last", `{"events":[{"type":"test","type":"span","content":{}}]}`, 0, false},
		{"events is not a list", `{"events":{}}`, 0, false},
		{"no events", `{}`, 0, false},
		{"empty session", session(empty), 0, true},
		{"float session id", session(strings.Replace(empty, `"test_session_id":7`, `"test_session_id":7.0`, 1)), 0, false},
		{"zero session id", session(strings.Replace(empty, `"test_session_id":7`, `"test_session_id":0`, 1)), 0, false},
		{"big session id", session(strings.Replace(empty, `"test_session_id":7`, `"test_session_id":123456789012345678901234567890`, 1)), 0, true},
		{"float exit code", session(strings.Replace(empty, `"test.exit_code":0`, `"test.exit_code":0.0`, 1)), 0, true},
		{"underflowing exit code", session(strings.Replace(empty, `"test.exit_code":0`, `"test.exit_code":1e-400`, 1)), 0, true},
		{"false exit code", session(strings.Replace(empty, `"test.exit_code":0`, `"test.exit_code":false`, 1)), 0, false},
		{"false error", session(strings.Replace(empty, `"error":0`, `"error":false`, 1)), 0, true},
		{"true error", session(strings.Replace(empty, `"error":0`, `"error":true`, 1)), 0, false},
		{"missing error", session(strings.Replace(empty, `"error":0,`, ``, 1)), 0, false},
		{"null meta", session(strings.Replace(empty, `"meta":{"test.session.empty_reason":"zero_tests","test.status":"skip"}`, `"meta":null`, 1)), 0, false},
		{"other reason", session(strings.Replace(empty, `zero_tests`, `filtered`, 1)), 0, false},
		{"a test hides the session", `{"events":[{"type":"test","content":{}},{"type":"test_session_end","content":{` + empty + `}}]}`, 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload.json")
			write(t, path, test.payload)
			got := decodePayload(path)
			if !got.Decoded || got.TestEvents != test.events || got.EmptySession != test.empty {
				t.Fatalf("got %+v, want %d events, empty session %v", got, test.events, test.empty)
			}
		})
	}
}

// The doctor reads a file itself when the inspector cannot decode it, so
// that its errors and its NaN support stay Python's.
func TestUndecodableFilesAreLeftToTheDoctor(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		decoded       bool
	}{
		{"byte order mark", "\xef\xbb\xbf" + `{"events":[]}`, true},
		{"array", `[1,2]`, true},
		{"invalid", `{"events":`, false},
		{"trailing data", `{} {}`, false},
		{"nan", `{"value":NaN}`, false},
		{"invalid utf-8", "{\"value\":\"\xff\"}", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "payload.json")
			write(t, path, test.payload)
			if got := decodePayload(path); got.Decoded != test.decoded || got.TestEvents != 0 || got.EmptySession {
				t.Fatalf("got %+v, want decoded %v", got, test.decoded)
			}
		})
	}
	if got := decodePayload(filepath.Join(t.TempDir(), "missing.json")); got.Decoded {
		t.Fatalf("a missing file decoded: %+v", got)
	}
}

func TestSidecarsKeepTheirDocument(t *testing.T) {
	directory := t.TempDir()
	for name, value := range map[string]string{"valid": "\xef\xbb\xbf" + `{"bazel.target": "//pkg:a", "n": 1.50}`,
		"null": "null", "invalid": "{", "nan": `{"n": NaN}`} {
		write(t, filepath.Join(directory, name), value)
	}
	if got := decodeMetadata(filepath.Join(directory, "valid")); string(got.Document) != `{"bazel.target": "//pkg:a", "n": 1.50}` {
		t.Fatalf("valid: %s", got.Document)
	}
	if got := decodeMetadata(filepath.Join(directory, "null")); string(got.Document) != "null" {
		t.Fatalf("null: %s", got.Document)
	}
	for _, name := range []string{"invalid", "nan"} {
		if got := decodeMetadata(filepath.Join(directory, name)); got.Document != nil {
			t.Fatalf("%s: %s", name, got.Document)
		}
	}
}

func TestCommandReadsARequestAndWritesAReport(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "test.outputs")
	write(t, filepath.Join(output, "payloads", "tests", "a.json"), `{"events":[{"type":"test","content":{}}]}`)
	write(t, filepath.Join(output, metadataName), `{"bazel.target":"//pkg:a"}`)
	request, _ := json.Marshal(Request{Version: 1, Outputs: []string{output, filepath.Join(root, "missing")}})
	var stdout, stderr bytes.Buffer
	if status := Command("ddtest", nil, bytes.NewReader(request), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	var report Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Outputs) != 2 || report.Outputs[0].JSONPayloads[0].TestEvents != 1 ||
		string(report.Outputs[0].Metadata[0].Document) != `{"bazel.target":"//pkg:a"}` ||
		report.Outputs[1].Path != filepath.Join(root, "missing") || len(report.Outputs[1].JSONPayloads) != 0 {
		t.Fatalf("unexpected report: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"json_payloads":[]`) {
		t.Fatalf("an output without payloads must list none: %s", stdout.String())
	}

	for _, test := range []struct {
		args   []string
		input  string
		status int
	}{
		{[]string{"extra"}, `{"schema_version":1,"outputs":[]}`, 2},
		{nil, `{"schema_version":2,"outputs":[]}`, 1},
		{nil, `{"schema_version":1,"outputs":[],"other":1}`, 1},
	} {
		stderr.Reset()
		if status := Command("ddtest", test.args, strings.NewReader(test.input), &stdout, &stderr); status != test.status {
			t.Fatalf("%v %s: status %d, want %d: %s", test.args, test.input, status, test.status, stderr.String())
		}
	}
}
