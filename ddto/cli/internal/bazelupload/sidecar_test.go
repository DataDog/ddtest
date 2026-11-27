package bazelupload

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Numbers read back as Python's json module writes the values that it reads;
// each expected string is json.dumps(json.loads(literal)).
func TestSidecarNumbersReadAsPythonWritesThem(t *testing.T) {
	for _, test := range []struct{ literal, python string }{
		{"0", "0"},
		{"-0", "0"},
		{"7", "7"},
		{"-12", "-12"},
		{"123456789012345678901234567890", "123456789012345678901234567890"},
		{"1.0", "1.0"},
		{"1.50", "1.5"},
		{"-0.0", "-0.0"},
		{"0.1", "0.1"},
		{"1e2", "100.0"},
		{"1E2", "100.0"},
		{"1e-5", "1e-05"},
		{"1.5e-05", "1.5e-05"},
		{"1e16", "1e+16"},
		{"1e15", "1000000000000000.0"},
		{"123456789012345.6", "123456789012345.6"},
		{"1e-4", "0.0001"},
		{"0.0001", "0.0001"},
		{"0.00001", "1e-05"},
		{"12345678901234567890.0", "1.2345678901234567e+19"},
		{"2.5e+300", "2.5e+300"},
		{"1e-400", "0.0"},
		{"3.14159", "3.14159"},
		{"100.0", "100.0"},
		{"1e22", "1e+22"},
		{"5e-324", "5e-324"},
	} {
		if got := pythonNumber(test.literal); got != test.python {
			t.Errorf("%s: got %s, want %s", test.literal, got, test.python)
		}
	}
}

func openRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root
}

// A sidecar reads as read_bazel_metadata reads it.
func TestReadSidecarFollowsTheRulesUploader(t *testing.T) {
	directory := t.TempDir()
	for name, value := range map[string]string{
		"valid":   "\xef\xbb\xbf" + `{"bazel.target": "//pkg:a", "shards": 2, "ratio": 1.50}`,
		"array":   `[1]`,
		"broken":  `{`,
		"nan":     `{"value": NaN}`,
		"invalid": "{\"value\": \"\xff\"}",
		"two":     `{} {}`,
	} {
		if err := os.MkdirAll(filepath.Join(directory, name), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(directory, name), sidecarName, value)
	}
	if err := os.MkdirAll(filepath.Join(directory, "missing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "folder", sidecarName), 0o755); err != nil {
		t.Fatal(err)
	}
	root := openRoot(t, directory)

	metadata, warning := readSidecar(root, "valid")
	encoded, _ := json.Marshal(metadata)
	if warning != "" || string(encoded) != `{"bazel.target":"//pkg:a","ratio":1.5,"shards":2}` {
		t.Fatalf("valid: %s, %q", encoded, warning)
	}
	for name, want := range map[string]string{"array": "bazel_metadata_invalid", "broken": "bazel_metadata_invalid",
		"nan": "bazel_metadata_invalid", "invalid": "bazel_metadata_invalid", "two": "bazel_metadata_invalid",
		"missing": "", "folder": ""} {
		if metadata, warning := readSidecar(root, name); metadata != nil || warning != want {
			t.Errorf("%s: %v, %q; want %q", name, metadata, warning, want)
		}
	}

	if runtime.GOOS != "windows" {
		for name, target := range map[string]string{"linked": filepath.Join(directory, "valid", sidecarName),
			"dangling": filepath.Join(directory, "nowhere.json")} {
			if err := os.MkdirAll(filepath.Join(directory, name), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(directory, name, sidecarName)); err != nil {
				t.Fatal(err)
			}
		}
		if metadata, warning := readSidecar(root, "linked"); metadata != nil || warning != "bazel_metadata_unsafe" {
			t.Errorf("linked: %v, %q", metadata, warning)
		}
		if metadata, warning := readSidecar(root, "dangling"); metadata != nil || warning != "" {
			t.Errorf("dangling: %v, %q", metadata, warning)
		}
	}
}

// choose follows ContextPlan.select.
func TestContextSelectionFollowsTheRulesUploader(t *testing.T) {
	two := []RepoContext{{"one", "first"}, {"two", "second"}}
	for _, test := range []struct {
		name      string
		selection ContextSelection
		repo      string
		context   string
		warning   string
	}{
		{"override", ContextSelection{Primary: "main", ByRepo: two, Override: true}, "two", "main", ""},
		{"single repository", ContextSelection{Primary: "main", ByRepo: two[:1]}, "", "main", ""},
		{"several repositories", ContextSelection{Primary: "main", ByRepo: two}, "two", "second", ""},
		{"first match wins", ContextSelection{ByRepo: []RepoContext{{"two", "a"}, {"two", "b"}}}, "two", "a", ""},
		{"no repository", ContextSelection{Primary: "main", ByRepo: two}, "", "", "context_repo_metadata_missing"},
		{"unknown repository", ContextSelection{Primary: "main", ByRepo: two}, "three", "", "context_repo_not_found"},
		{"required match", ContextSelection{Primary: "main", ByRepo: two[:1], RequireRepoMatch: true}, "two", "", "context_repo_not_found"},
	} {
		if context, warning := test.selection.Choose(test.repo); context != test.context || warning != test.warning {
			t.Errorf("%s: %q, %q; want %q, %q", test.name, context, warning, test.context, test.warning)
		}
	}
}

// A source that names its output directory sends the same request as one
// whose plan carries the sidecar's metadata and the selected context.
func TestOutputDirectorySendsWhatThePlanWouldCarry(t *testing.T) {
	bodies := func(plan Plan, directory string) string {
		var mu sync.Mutex
		var received []string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			received = append(received, string(body))
			mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
		}))
		defer server.Close()
		report := runFixture(t, directory, plan, Options{AgentlessURL: server.URL, APIKey: "local-test", KeepPayloads: true})
		if report.ExitCode != 0 || len(report.Sources[0].Warnings) != 0 {
			t.Fatalf("report %+v", report.Sources)
		}
		return strings.Join(received, "\n")
	}
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "* @team\n")
	if err := os.MkdirAll(filepath.Join(directory, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(directory, "out"), "test.json", inputPayload)
	carried := testPlan(testSource("test", "out/test.json"))
	sidecar, _ := json.Marshal(carried.Sources[0].Metadata)
	writeFixture(t, filepath.Join(directory, "out"), sidecarName, string(sidecar))

	read := testPlan(Source{ID: "test", Path: "out/test.json", Kind: Test, Output: "out"})
	read.ContextSelection = &ContextSelection{Primary: "ctx"}
	if want, got := bodies(carried, directory), bodies(read, directory); want != got || want == "" {
		t.Fatalf("requests differ:\n%s\n%s", want, got)
	}
}

// Sidecar and selection warnings reach the source's result.
func TestSidecarWarningsReachTheReport(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	if err := os.MkdirAll(filepath.Join(directory, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(directory, "out"), "test.json", inputPayload)
	writeFixture(t, filepath.Join(directory, "out"), sidecarName, "{")
	plan := testPlan(Source{ID: "test", Path: "out/test.json", Kind: Test, Output: "out"})
	plan.ContextSelection = &ContextSelection{Primary: "ctx", ByRepo: []RepoContext{{"one", "ctx"}, {"two", "ctx"}}}
	report := runFixture(t, directory, plan, Options{DryRun: true, AgentlessURL: "http://127.0.0.1:1"})
	if got := strings.Join(report.Sources[0].Warnings, ","); !strings.HasPrefix(got, "bazel_metadata_invalid,context_repo_metadata_missing") {
		t.Fatalf("warnings %q", got)
	}
}

// A cancelled upload reads no further sidecar and reports the sources as
// cancelled.
func TestACancelledUploadStopsReadingSidecars(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	if err := os.MkdirAll(filepath.Join(directory, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(directory, "out"), "test.json", inputPayload)
	writeFixture(t, filepath.Join(directory, "out"), sidecarName, "{")
	plan := testPlan(Source{ID: "test", Path: "out/test.json", Kind: Test, Output: "out"})
	plan.ContextSelection = &ContextSelection{Primary: "ctx"}
	results := []FileResult{{ID: "test", Status: statusCancelled}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	readSidecars(ctx, openRoot(t, directory), &plan, results, 4)
	if len(results[0].Warnings) != 0 || plan.Sources[0].Context != "" {
		t.Fatalf("read a sidecar after cancellation: %+v %+v", results[0], plan.Sources[0])
	}

	root := openRoot(t, directory)
	report, err := upload(ctx, root, plan, Options{DryRun: true, AgentlessURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Sources[0].Status != statusCancelled {
		t.Fatalf("status %s", report.Sources[0].Status)
	}
}

func TestOutputDirectoriesNeedAContextSelection(t *testing.T) {
	plan := `{"schema_version":1,"rules_version":"r","runtime_id":"x","sources":[{"id":"a","path":"a.json","kind":"test","output":"out"}]}`
	if _, err := readPlan(bytes.NewReader([]byte(plan))); err == nil || !strings.Contains(err.Error(), "context_selection") {
		t.Fatalf("err %v", err)
	}
}
