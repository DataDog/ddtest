package bazeldoctor

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testConfig = `{"context_manifest_path": "", "context_manifest_short_path": "", "forbid_dd_git_test_env": false,
"require_git_metadata": false, "expected_targets": %s, "expected_targets_file_path": "",
"expected_targets_file_short_path": "", "require_json_payloads": true, "require_bazel_metadata": true,
"forbid_full_bundle_no_match": true, "forbid_msgpack_payloads": true, "allowed_payload_selections": ["module"],
"expected_payload_selection_by_target": {}}`

// validate runs the command on a workspace whose testlogs hold one output
// of //pkg:target, as the rules' doctor tests write it.
func validate(t *testing.T, expectedTargets string, change func(*Request)) (Result, string, string, map[string]any) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Python doctor validates the outputs on Windows")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "pkg", "target", "test.outputs")
	writeFile(t, filepath.Join(output, "payloads", "tests", "span_events_1.json"), `{"events":[{"type":"test","content":{}}]}`)
	writeFile(t, filepath.Join(output, "bazel_target_metadata.json"),
		`{"bazel.target": "//pkg:target", "bazel.go.payload_selection": "module"}`)
	t.Setenv("BUILD_WORKSPACE_DIRECTORY", root)
	t.Setenv("TESTLOGS_DIR", root)
	t.Setenv("DD_TEST_OPTIMIZATION_LOG_LEVEL", "")
	t.Setenv("DD_TEST_OPTIMIZATION_DEBUG", "")
	request := Request{Version: 1, ConfigPath: filepath.Join(root, "doctor.config.json"),
		Config: json.RawMessage(strings.Replace(testConfig, "%s", expectedTargets, 1)),
		Args: Args{FreshnessSource: "auto", FreshnessMode: "disabled", ArtifactSource: "local",
			RemoteArtifacts: "disabled", BepArtifactDownloaderTimeout: 300,
			ReportJSON: filepath.Join(root, "out", "report.json")}}
	if change != nil {
		change(&request)
	}
	input, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if status := Command(context.Background(), "ddtest", nil, bytes.NewReader(input), &stdout, &stderr); status != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("result %q: %v", stdout.String(), err)
	}
	var report map[string]any
	if data, err := os.ReadFile(request.Args.ReportJSON); err == nil {
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
	}
	return result, result.Stdout, result.Stderr, report
}

func TestValidateSucceeds(t *testing.T) {
	result, stdout, stderr, report := validate(t, `["//pkg:target"]`, nil)
	if result.Status != "done" || result.ExitCode != 0 {
		t.Fatalf("result %+v; stderr %s", result, stderr)
	}
	want := prefix + "payload selection summary: module=1\n" + prefix + "OK: validated 1 test output directorie(s)\n"
	if stdout != want || stderr != "" {
		t.Fatalf("stdout %q, stderr %q", stdout, stderr)
	}
	if report["status"] != "ok" || report["exit_code"] != float64(0) {
		t.Fatalf("report status %v, exit code %v", report["status"], report["exit_code"])
	}
}

func TestValidateFails(t *testing.T) {
	result, stdout, stderr, report := validate(t, `["//pkg:missing"]`, nil)
	if result.Status != "done" || result.ExitCode != 1 || stdout != "" {
		t.Fatalf("result %+v, stdout %q", result, stdout)
	}
	if !strings.HasPrefix(stderr, prefix+"expected target output root not found for //pkg:missing: ") {
		t.Fatalf("stderr %q", stderr)
	}
	reportResult := report["result"].(map[string]any)
	if report["status"] != "fail" || reportResult["reason_code"] != "doctor_failed" {
		t.Fatalf("report %v", reportResult)
	}
}

// A configuration that Python reads in its own way leaves the validation to
// the Python doctor, with nothing printed or written.
func TestValidateFallsBack(t *testing.T) {
	result, stdout, stderr, report := validate(t, `["//pkg:target"]`, func(request *Request) {
		request.Config = json.RawMessage(`{"expected_targets": [], "expected_targets": []}`)
	})
	if result.Status != "fallback" || stdout != "" || stderr != "" || report != nil {
		t.Fatalf("result %+v, stdout %q, stderr %q, report %v", result, stdout, stderr, report)
	}
}

func TestValidateRejectsBadRequests(t *testing.T) {
	var stdout, stderr bytes.Buffer
	ctx := context.Background()
	if status := Command(ctx, "ddtest", nil, strings.NewReader(`{"schema_version": 2}`), &stdout, &stderr); status != 1 {
		t.Fatalf("status %d", status)
	}
	if status := Command(ctx, "ddtest", []string{"extra"}, strings.NewReader(""), &stdout, &stderr); status != 2 {
		t.Fatalf("status %d", status)
	}
	if !strings.HasPrefix(stderr.String(), "[dd-test-optimization-doctor] ERROR: unsupported validation request") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

// A cancelled run stops staging, removes its run directory and reports no
// result.
func TestStagingStopsWhenInterrupted(t *testing.T) {
	d := newTestDoctor(t, Args{RemoteArtifacts: "disabled"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d.ctx = ctx
	f := &freshness{ambiguousOutputs: map[string]bool{}, ambiguousLabels: map[string]bool{},
		references: []reference{{label: "//pkg:t", outputKey: "pkg/t/test.outputs", fetchValue: "x/outputs.zip",
			stageableCarrier: true}}}
	base := filepath.Join(d.workspace, "stage")
	stoppedRun := func() (wasInterrupted bool) {
		defer func() { _, wasInterrupted = recover().(interrupted) }()
		d.stageArtifacts(f, base, nil)
		return false
	}
	if !stoppedRun() {
		t.Fatal("staging did not stop")
	}
	if entries, _ := os.ReadDir(filepath.Join(base, "__runs")); len(entries) != 0 {
		t.Fatalf("run directories remain: %v", entries)
	}
}
