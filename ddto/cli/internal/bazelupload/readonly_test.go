package bazelupload

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// readOnlyOutputs writes a source in a read-only directory, as Bazel leaves
// test outputs, and restores the directory's permissions when the test ends.
func readOnlyOutputs(t *testing.T, directory, name, value string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the user")
	}
	outputs := filepath.Join(directory, "outputs")
	if err := os.Mkdir(outputs, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, outputs, name, value)
	if err := os.Chmod(filepath.Join(outputs, name), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outputs, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outputs, 0o700) })
}

// A delivered source in a read-only output tree is deleted, as the rules
// uploader deletes it.
func TestReadOnlyOutputsAreCleanedUp(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	readOnlyOutputs(t, directory, "test.json", inputPayload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	defer server.Close()

	report := runFixture(t, directory, testPlan(testSource("test", "outputs/test.json")),
		Options{AgentlessURL: server.URL, APIKey: "local-test"})
	result := report.Sources[0]
	if result.Status != statusSucceeded || !result.Deleted || len(result.Warnings) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(directory, "outputs", "test.json")); !os.IsNotExist(err) {
		t.Fatal("the source remains:", err)
	}
}

// After a partial delivery, the replay replaces a source in a read-only output
// tree.
func TestReadOnlyOutputsKeepTheReplay(t *testing.T) {
	directory := t.TempDir()
	writeFixture(t, directory, "CODEOWNERS", "")
	readOnlyOutputs(t, directory, "telemetry.json",
		`{"api_version":"v2","request_type":"app-started","application":{"service_name":"reporting","language_name":"go"}}`)
	source := testSource("telemetry", "outputs/telemetry.json")
	source.Kind = Telemetry
	source.Telemetry = &TelemetryDirective{Synthetic: Object{"api_version": "v2", "request_type": "message-batch",
		"application": Object{"service_name": "reporting"}, "payload": []any{}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("DD-Telemetry-Request-Type") == "message-batch" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	report := runFixture(t, directory, testPlan(source), Options{AgentlessURL: server.URL, APIKey: "local-test"})
	result := report.Sources[0]
	if result.Status != statusFailed || len(result.Warnings) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "outputs", "telemetry.json"))
	if err != nil || !strings.Contains(string(raw), telemetryReplayKey) {
		t.Fatalf("the source holds no replay: %s, %v", raw, err)
	}
}
