package bazelinspect

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The expected values below are what the doctor's Python helpers return.

func TestBepTestOutputKeyFollowsTheDoctor(t *testing.T) {
	for _, test := range []struct{ path, name, key string }{
		{"file:///e/bazel-out/k8/testlogs/pkg/t/test.log", "", "pkg/t/test.outputs"},
		{"file:///e/bazel-out/k8/testlogs/pkg/t/test.xml", "", "pkg/t/test.outputs"},
		{"file:///e/testlogs/pkg/t/test_attempts/attempt_2.log", "", "pkg/t/test_attempts/attempt_2.outputs/test.outputs"},
		{"file:///e/testlogs/pkg/t/test.outputs/payloads/tests/a.json", "test.outputs/payloads/tests/a.json", "pkg/t/test.outputs"},
		{"file:///e/testlogs/pkg/t/test_attempts/attempt_1.outputs/x.json", "", "pkg/t/test_attempts/attempt_1.outputs/test.outputs"},
		{"file:///e/testlogs/pkg/t/test_attempts/attempt_1.outputs", "", "pkg/t/test_attempts/attempt_1.outputs/test.outputs"},
		{"file:///e/testlogs/pkg/t/outputs.zip", "", "pkg/t/test.outputs"},
		{"file:///e/testlogs/pkg/t/test.outputs", "", "pkg/t/test.outputs"},
		{"file://host/e/bazel-testlogs/pkg/t/test.outputs/a%20b.json", "", "pkg/t/test.outputs"},
		{"FILE://localhost/e/testlogs/pkg/t/test.log?x=1#f", "", "pkg/t/test.outputs"},
		{"bazel-out/k8/testlogs/pkg/t/test.outputs/c.json", "test.outputs/c.json", "pkg/t/test.outputs"},
		{"./pkg/t/test.outputs/d.json", "", "pkg/t/test.outputs"},
		{`pkg\t\test.log`, "", "pkg/t/test.outputs"},
		{"file:///e/other/x", "", ""},
		{"file:///e/testlogs/pkg/test.outputs/inner/test.outputs/k.json", "test.outputs/k.json", "pkg/test.outputs/inner/test.outputs"},
		{"file:///e/testlogs/a//b/./test.outputs", "", "a//b/./test.outputs"},
		{"", "", ""},
	} {
		if key, err := bepTestOutputKey(test.path, test.name); err != nil || key != test.key {
			t.Errorf("%q, %q: got %q, %v; want %q", test.path, test.name, key, err, test.key)
		}
	}
}

func TestBepReferenceHelpersFollowTheDoctor(t *testing.T) {
	for uri, want := range map[string]string{
		"file:///a/b": "/a/b", "file://localhost/a": "/a", "file://host/a%2Fb?q#f": "//host/a/b", "FiLe:///x%41": "/xA",
		"http://x/y": "http://x/y", "file:///a\tb": "/ab", "relative/path": "relative/path", "file://": "",
	} {
		if got, err := stripFileURI(uri); err != nil || got != want {
			t.Errorf("stripFileURI(%q) = %q, %v; want %q", uri, got, err, want)
		}
	}
	for reference, want := range map[string]bool{
		"bytestream://h/blobs/a/1": true, "BLOBS/x": true, "0123456789abcdef0123456789abcdef/12": true, "file:///x": false,
		"a/b": false, "": false, "s3://b/k": true, "0123/12": false,
	} {
		if got := remoteOnlyReference(reference); got != want {
			t.Errorf("remoteOnlyReference(%q) = %v", reference, got)
		}
	}
	for candidate, want := range map[string]bool{
		"file:///e/testlogs/pkg/t/test.log": true, "bazel-testlogs/x": true, "testlogs": false,
		"bytestream://h/testlogs/x": false, "/a/b/c": false, `a\testlogs\b`: true,
	} {
		if got, err := trustedOutputKeyCandidate(candidate); err != nil || got != want {
			t.Errorf("trustedOutputKeyCandidate(%q) = %v, %v", candidate, got, err)
		}
	}
	for directory, want := range map[string]string{
		"a/test_attempts/attempt_1.outputs": "a/test_attempts/attempt_1.outputs/test.outputs", "//a/b/": "//a/b",
		"/a/./b": "/a/b", "": ".", ".": ".", `a\b`: "a/b", "///x": "/x",
	} {
		if got := canonicalOutputDirectoryKey(directory); got != want {
			t.Errorf("canonicalOutputDirectoryKey(%q) = %q, want %q", directory, got, want)
		}
	}
	// urlsplit raises for an unbalanced IPv6 host, and decoded text other
	// than ASCII is left to the doctor.
	for _, uri := range []string{"file://[::1/x", "file:///%C3%B1"} {
		if _, err := stripFileURI(uri); err == nil {
			t.Errorf("stripFileURI(%q) is supported", uri)
		}
	}
}

func TestBepEventIDsAreKeyedAsTheDoctorKeysThem(t *testing.T) {
	for raw, want := range map[string]string{
		`{"testResult": {"label": "//a:b", "run": 1, "configuration": {"id": "c"}}}`: `{"test_result":{"configuration":{"id":"c"},"label":"//a:b","run":1}}`,
		`{"buildFinished": {}}`:                  `{"build_finished":{}}`,
		`{"namedSet": {"id": "1"}}`:              `{"named_set":{"id":"1"}}`,
		`{"x": [true, null, -0, "q\"\n\u0001"]}`: `{"x":[true,null,0,"q\"\n\u0001"]}`,
	} {
		value, err := parseJSON([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		normalized, err := normalizedEventID(value)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := canonicalJSON(normalized); err != nil || got != want {
			t.Errorf("%s: got %s, %v; want %s", raw, got, err, want)
		}
	}
	for _, raw := range []string{`{"fooBar": 1, "foo_bar": 2}`, `{"x": 1.5}`, `{"ñ": 1}`, `{"x": "ñ"}`} {
		value, _ := parseJSON([]byte(raw))
		normalized, err := normalizedEventID(value)
		if err == nil {
			_, err = canonicalJSON(normalized)
		}
		if err == nil {
			t.Errorf("%s is supported", raw)
		}
	}
}

// The parser accepts only what Python's json module reads the same way.
func TestBepParserAcceptsWhatPythonReadsAlike(t *testing.T) {
	for _, accepted := range []string{`{}`, ` {"a": [1, -0, 2.5e3, true, null]} `, `{"a": "é😀\/"}`, `"x"`} {
		if _, err := parseJSON([]byte(accepted)); err != nil {
			t.Errorf("%s: %v", accepted, err)
		}
	}
	for _, rejected := range []string{`{"a": NaN}`, `{"a": 01}`, `{"a": 1.}`, `{"a": .5}`, `{"a": 1,}`, `{"a": 1} x`,
		`{"a": "\ud800"}`, "{\"a\": \"\t\"}", `{"a": 1, "a": 2}`, "\v{}", `{'a': 1}`, `{"a": +1}`,
		strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)} {
		if _, err := parseJSON([]byte(rejected)); err == nil {
			t.Errorf("%q is accepted", rejected)
		}
	}
}

func writeBep(t *testing.T, path string, events ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strings.Join(events, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	bepStarted  = `{"id": {"started": {}}, "started": {"uuid": "build-1"}, "children": [{"buildFinished": {}}]}`
	bepFinished = `{"id": {"buildFinished": {}}, "finished": {}, "lastMessage": true}`
)

func TestEvaluateBepReportsFreshAndCachedOutputs(t *testing.T) {
	directory := t.TempDir()
	first, second := filepath.Join(directory, "first.json"), filepath.Join(directory, "second.json")
	writeBep(t, first, bepStarted,
		`{"id": {"targetCompleted": {"label": "//pkg:t"}}, "children": [{"testResult": {"label": "//pkg:t", "run": 1, "shard": 1, "attempt": 1}}, {"testSummary": {"label": "//pkg:t"}}]}`,
		`{"id": {"testResult": {"label": "//pkg:t", "run": 1, "shard": 1, "attempt": 1}}, "testResult": {"status": 1, "testActionOutput": [{"name": "test.log", "uri": "file:///e/testlogs/pkg/t/test.log"}, {"name": "test.outputs", "uri": "file:///e/testlogs/pkg/t/test.outputs"}]}}`,
		`{"id": {"testSummary": {"label": "//pkg:t"}}, "testSummary": {"totalRunCount": 1, "runCount": 1, "shardCount": 1, "attemptCount": 1}}`,
		`{"id": {"testResult": {"label": "//pkg:c", "run": 1, "shard": 1, "attempt": 1}}, "testResult": {"status": "PASSED", "cachedLocally": true, "testActionOutput": [{"name": "test.log", "uri": "file:///e/testlogs/pkg/c/test.log"}]}}`,
		`{"id": {"targetCompleted": {"label": "//pkg:s"}}, "aborted": {"reason": "SKIPPED"}}`,
		bepFinished)
	if err := os.WriteFile(second, []byte("\xef\xbb\xbf"+mustRead(t, first)), 0o644); err != nil {
		t.Fatal(err)
	}

	report := EvaluateBep(BepRequest{Version: 1, Files: []string{first, second}, References: true}, 2)
	if report.Status != "ok" {
		t.Fatalf("status %s", report.Status)
	}
	if want := []BepDocument{{UUID: "build-1"}, {UUID: "build-1", Skip: true}}; !reflect.DeepEqual(want, report.Documents) {
		t.Fatalf("documents %+v", report.Documents)
	}
	if want := [][2]string{{"//pkg:t", "pkg/t/test.outputs"}}; !reflect.DeepEqual(want, report.Eligible) {
		t.Fatalf("eligible %v", report.Eligible)
	}
	if want := [][2]string{{"//pkg:c", "pkg/c/test.outputs"}}; !reflect.DeepEqual(want, report.Cached) {
		t.Fatalf("cached %v", report.Cached)
	}
	if !reflect.DeepEqual([]string{"//pkg:s"}, report.Skipped) || report.Count != 3 || len(report.References) != 3 {
		t.Fatalf("skipped %v, references %d/%d", report.Skipped, report.Count, len(report.References))
	}
	fresh := report.Executions[0]
	if fresh[7] != "PASSED" || fresh[6] != false || !reflect.DeepEqual([]string{"pkg/t/test.outputs"}, fresh[8]) {
		t.Fatalf("execution %v", fresh)
	}
	if carriers := fresh[9].([][]any); len(carriers) != 1 || carriers[0][1] != "file:///e/testlogs/pkg/t/test.outputs" {
		t.Fatalf("carriers %v", carriers)
	}
	// The cached result has no summary and was not announced.
	if want := [][2]string{{"//pkg:c", "BEP TestSummary is missing: //pkg:c"}}; !reflect.DeepEqual(want, report.Errors) {
		t.Fatalf("errors %v", report.Errors)
	}

	lean := EvaluateBep(BepRequest{Version: 1, Files: []string{first}}, 2)
	if len(lean.References) != 0 || lean.Count != 3 || !reflect.DeepEqual([]string{"//pkg:c", "//pkg:t"}, lean.RefLabels) {
		t.Fatalf("lean references %v %d %v", lean.References, lean.Count, lean.RefLabels)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestEvaluateBepChecksTheTopologyAsTheDoctorDoes(t *testing.T) {
	result := func(shard, attempt int) string {
		return `{"id": {"testResult": {"label": "//pkg:t", "run": 1, "shard": ` + string(rune('0'+shard)) + `, "attempt": ` +
			string(rune('0'+attempt)) + `}}, "testResult": {"status": "PASSED"}}`
	}
	for _, test := range []struct {
		name   string
		events []string
		errors [][2]string
	}{
		{"complete", []string{bepStarted, result(1, 1),
			`{"id": {"testSummary": {"label": "//pkg:t"}}, "testSummary": {"totalRunCount": 1, "runCount": 1, "shardCount": 1, "attemptCount": 1}}`,
			bepFinished}, nil},
		{"no final message", []string{bepStarted, `{"id": {"buildFinished": {}}, "finished": {}}`},
			[][2]string{{"", "BEP stream is incomplete: require started, BuildFinished and a final last_message event"}}},
		{"announced event missing", []string{`{"id": {"started": {}}, "started": {}, "children": [{"buildFinished": {}}, {"testResult": {"label": "//pkg:x", "run": 1}}]}`, bepFinished},
			[][2]string{{"//pkg:x", `BEP announced event is missing: {"test_result":{"label":"//pkg:x","run":1}}`}}},
		{"aborted result", []string{bepStarted, `{"id": {"testResult": {"label": "//pkg:t"}}}`, bepFinished},
			[][2]string{{"//pkg:t", "BEP TestResult was aborted or has no result payload: //pkg:t"},
				{"//pkg:t", "BEP TestSummary is missing: //pkg:t"}}},
		{"invalid counts", []string{bepStarted, result(1, 1),
			`{"id": {"testSummary": {"label": "//pkg:t"}}, "testSummary": {"totalRunCount": "1"}}`, bepFinished},
			[][2]string{{"//pkg:t", "invalid BEP TestSummary counts: //pkg:t"}}},
		{"missing shard", []string{bepStarted, result(1, 1),
			`{"id": {"testSummary": {"label": "//pkg:t"}}, "testSummary": {"totalRunCount": 2, "runCount": 1, "shardCount": 2, "attemptCount": 1}}`,
			bepFinished},
			[][2]string{{"//pkg:t", "BEP TestSummary topology does not match received runs/shards/attempts: //pkg:t"}}},
		{"attempts", []string{bepStarted, result(1, 1), result(1, 2),
			`{"id": {"testSummary": {"label": "//pkg:t"}}, "testSummary": {"totalRunCount": 2, "runCount": 1, "shardCount": 1, "attemptCount": 3}}`,
			bepFinished},
			[][2]string{{"//pkg:t", "BEP TestSummary attempt count does not match received attempts: //pkg:t"}}},
	} {
		path := filepath.Join(t.TempDir(), "bep.json")
		writeBep(t, path, test.events...)
		report := EvaluateBep(BepRequest{Version: 1, Files: []string{path}}, 2)
		if report.Status != "ok" || !reflect.DeepEqual(append([][2]string{}, test.errors...), report.Errors) {
			t.Errorf("%s: %s %v", test.name, report.Status, report.Errors)
		}
	}
}

// Anything that the doctor reads in its own way, or that makes it warn or
// fail, is left to it.
func TestEvaluateBepLeavesUnusualInputToTheDoctor(t *testing.T) {
	directory := t.TempDir()
	conflicting := filepath.Join(directory, "conflicting.json")
	writeBep(t, conflicting, bepStarted, `{"id": {"progress": {}}}`, bepFinished)
	for name, content := range map[string]string{
		"invalid JSON":     bepStarted + "\n{broken\n" + bepFinished,
		"not an object":    bepStarted + "\n[1]\n" + bepFinished,
		"invalid UTF-8":    bepStarted + "\n{\"id\": {\"progress\": {}}, \"x\": \"\xff\"}\n" + bepFinished,
		"other label text": bepStarted + "\n{\"id\": {\"testResult\": {\"label\": \"//pkg:ñ\"}}, \"testResult\": {}}\n" + bepFinished,
		"invalid status":   bepStarted + "\n{\"id\": {\"testResult\": {\"label\": \"//pkg:t\"}}, \"testResult\": {\"status\": 9}}\n" + bepFinished,
		"invalid run":      bepStarted + "\n{\"id\": {\"testResult\": {\"label\": \"//pkg:t\", \"run\": 0}}, \"testResult\": {}}\n" + bepFinished,
		"empty identity":   bepStarted + "\n{\"id\": {}}\n" + bepFinished,
		"same build":       bepStarted + "\n" + bepFinished,
	} {
		path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		files := []string{path}
		if name == "same build" {
			files = []string{conflicting, path}
		}
		if report := EvaluateBep(BepRequest{Version: 1, Files: files}, 2); report.Status != "fallback" {
			t.Errorf("%s: status %s", name, report.Status)
		}
	}
}

func TestFreshnessCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bep.json")
	writeBep(t, path, bepStarted, bepFinished)
	var stdout, stderr bytes.Buffer
	request := `{"schema_version": 1, "files": ["` + filepath.ToSlash(path) + `"], "references": false}`
	if status := FreshnessCommand("ddtest", nil, strings.NewReader(request), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	var report BepReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil || report.Status != "ok" || len(report.Documents) != 1 {
		t.Fatalf("report %s, %v", stdout.String(), err)
	}
	stderr.Reset()
	if status := FreshnessCommand("ddtest", nil, strings.NewReader(`{"schema_version": 2, "files": []}`), &stdout, &stderr); status != 1 ||
		!strings.Contains(stderr.String(), "unsupported freshness request schema_version 2") {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
}
