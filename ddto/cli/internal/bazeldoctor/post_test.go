package bazeldoctor

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelupload"
)

const postPayload = `{"version": 1,
"metadata": {"*": {"runtime-id":"process-a","language":"go","library_version":"fixture"}},
"events": [{"type":"test","version":2,"content":{
 "trace_id":1,"span_id":2,"test_session_id":3,"test_module_id":4,"test_suite_id":5,
 "name":"go.test","service":"original","resource":"fixture","type":"test","start":1000000000000000000,"duration":1,"error":0,
 "meta":{"test.name":"fixture","test.status":"pass","test.source.file":"pkg/thing_test.go"}}}]}`

const postDoctorConfig = `{"expected_targets": [], "forbid_dd_git_test_env": false, "require_git_metadata": false,
"require_json_payloads": true, "require_bazel_metadata": true, "forbid_full_bundle_no_match": true,
"forbid_msgpack_payloads": true, "allowed_payload_selections": ["%s"], "expected_payload_selection_by_target": {}}`

type postFixture struct {
	root, testlogs, output, report, doctorReport string
	request                                      PostRequest
}

func newPostFixture(t *testing.T) *postFixture {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("post-process runs fall back on Windows")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &postFixture{root: root, testlogs: filepath.Join(root, "bazel-testlogs")}
	f.output = filepath.Join(f.testlogs, "pkg", "target", "test.outputs")
	writeFile(t, filepath.Join(f.output, "payloads", "tests", "span_events_1.json"), postPayload)
	writeFile(t, filepath.Join(f.output, "bazel_target_metadata.json"),
		`{"bazel.target": "//pkg:target", "bazel.package": "pkg", "bazel.go.payload_selection": "module"}`)
	f.report = filepath.Join(root, "out", "uploader-report.json")
	f.doctorReport = filepath.Join(root, "out", "doctor-report.json")
	f.request = PostRequest{Version: 1, Config: PostConfig{Workspace: root, DryRun: true, LogLevel: "INFO",
		MaxDepth: 0, Workers: 2, FreshnessSource: "auto", FreshnessMode: "disabled", FreshnessDisabledExplicitly: true,
		ArtifactSource: "local", RemoteArtifacts: "disabled", ArtifactStagingDir: filepath.Join(root, ".topt", "bep-artifacts"),
		BepArtifactDownloaderTimeout: 300, ReportJSON: f.report, TestlogsDir: f.testlogs, RulesVersion: "test",
		UploaderBackend: "ddtest", Agentless: true},
		Resources: PostResources{Contexts: map[string]bazelupload.Object{"context-1": {"git.commit.sha": "abcdef",
			"git.repository_url": "https://example.org/repo"}}, PrimaryContext: "context-1",
			ContextSelection: &bazelupload.ContextSelection{Primary: "context-1"}}}
	t.Setenv("DD_TEST_OPTIMIZATION_LOG_LEVEL", "")
	t.Setenv("DD_API_KEY", "")
	t.Setenv("DD_SITE", "datadoghq.com")
	return f
}

func (f *postFixture) validate(selection string) {
	f.request.Validation = &PostValidation{Config: json.RawMessage(strings.Replace(postDoctorConfig, "%s", selection, 1)),
		ReportJSON: f.doctorReport}
}

func (f *postFixture) run(t *testing.T) (int, string, string, map[string]any) {
	t.Helper()
	input, err := json.Marshal(f.request)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	status := PostCommand(context.Background(), "ddtest", nil, bytes.NewReader(input), &stdout, &stderr)
	var report map[string]any
	if data, err := os.ReadFile(f.report); err == nil {
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatal(err)
		}
	}
	return status, stdout.String(), stderr.String(), report
}

func TestPostProcessDryRunValidates(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	status, stdout, stderr, report := f.run(t)
	if status != 0 {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout, stderr)
	}
	for _, want := range []string{
		prefix + "payload selection summary: module=1\n",
		prefix + "OK: validated 1 test output directorie(s)\n",
		"[dd-uploader] summary: mode=dry-run result=success exit_code=0",
		"[dd-uploader] files: discovered=1 eligible=1 processed=1 succeeded=1 failed=0 skipped=0 cancelled=0",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "[dd-uploader] INFO: dry-run validated 1 test payloads") {
		t.Errorf("stderr %s", stderr)
	}
	result := report["result"].(map[string]any)
	if report["tool"] != "dd-test-optimization-uploader" || result["reason_code"] != "upload_skipped_dry_run" {
		t.Fatalf("report %v", result)
	}
	if _, err := os.Stat(filepath.Join(f.output, "payloads", "tests", "span_events_1.json")); err != nil {
		t.Fatal("a dry run deleted the payload")
	}
	if data, err := os.ReadFile(f.doctorReport); err != nil || !strings.Contains(string(data), `"status": "ok"`) {
		t.Fatalf("doctor report %s, %v", data, err)
	}
}

// A doctor failure keeps the run's failure, and the payload is still sent.
func TestPostProcessSendsDespiteDoctorFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	f := newPostFixture(t)
	f.validate("full_bundle_fallback")
	f.request.Config.DryRun = false
	t.Setenv("DD_API_KEY", "secret-key")
	t.Setenv("DD_TEST_OPTIMIZATION_AGENTLESS_URL", server.URL)
	status, stdout, stderr, report := f.run(t)
	if status != 1 || requests.Load() == 0 {
		t.Fatalf("status %d after %d requests\nstdout %s\nstderr %s", status, requests.Load(), stdout, stderr)
	}
	if !strings.Contains(stderr, prefix) || strings.Contains(stdout+stderr, "secret-key") {
		t.Fatalf("stderr %s", stderr)
	}
	if report["exit_code"] != float64(1) || report["upload"].(map[string]any)["payloads_uploaded"] != float64(1) {
		t.Fatalf("report exit %v upload %v", report["exit_code"], report["upload"])
	}
	if _, err := os.Stat(filepath.Join(f.output, "payloads", "tests", "span_events_1.json")); !os.IsNotExist(err) {
		t.Fatal("the sent payload was not deleted")
	}
}

// Before sending, an input that only the Python uploader reads leaves the
// invocation to it, with nothing printed.
func TestPostProcessFallsBackBeforeSending(t *testing.T) {
	f := newPostFixture(t)
	bep := filepath.Join(f.root, "bep.json")
	writeFile(t, bep, "not json\n")
	f.request.Config.BepJSONFiles = []string{bep}
	f.request.Config.FreshnessMode, f.request.Config.FreshnessSource = "required", "bep"
	status, stdout, stderr, report := f.run(t)
	if status != PostFallbackExit || stdout != "" || stderr != "" || report != nil {
		t.Fatalf("status %d\nstdout %q\nstderr %q\nreport %v", status, stdout, stderr, report)
	}
}

// The doctor's Git check runs on the contexts that the uploader loaded.
func TestPostProcessChecksContextGitMetadata(t *testing.T) {
	f := newPostFixture(t)
	f.request.Validation = &PostValidation{Config: json.RawMessage(`{"require_git_metadata": true,
"require_json_payloads": true, "require_bazel_metadata": true, "allowed_payload_selections": ["module"]}`)}
	f.request.Resources.ContextRecords = []ContextRecord{{RepoKey: "repo", Context: "context-1"}}
	status, stdout, stderr, _ := f.run(t)
	want := prefix + "context repo is missing required git metadata: git.branch or git.tag\n"
	if status != 1 || !strings.Contains(stderr, want) {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout, stderr)
	}
}

// A sidecar that the doctor's reader cannot read leaves the invocation,
// checks included, to Python before anything is sent.
func TestPostProcessFallsBackOnUnreadableSidecar(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	writeFile(t, filepath.Join(f.output, "bazel_target_metadata.json"),
		`{"bazel.target": "//pkg:target", "bazel.target": "//pkg:target", "bazel.go.payload_selection": "full_bundle_no_match"}`)
	status, stdout, stderr, report := f.run(t)
	if status != PostFallbackExit || stdout != "" || stderr != "" || report != nil {
		t.Fatalf("status %d\nstdout %q\nstderr %q\nreport %v", status, stdout, stderr, report)
	}
}

// After sending, doctor checks that cannot run in Go fail the run.
func TestPostProcessFailsWhenDoctorChecksCannotRun(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	writeFile(t, filepath.Join(f.output, "bazel_target_metadata.json"),
		`{"bazel.target": "//pkg:target", "bazel.go.payload_selection": ["module"]}`)
	status, stdout, stderr, report := f.run(t)
	if status != 1 || !strings.Contains(stderr, "doctor checks could not run: unhashable payload selection") {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout, stderr)
	}
	if report["exit_code"] != float64(1) {
		t.Fatalf("report exit %v", report["exit_code"])
	}
}

// The doctor checks every selected output, including one with nothing to
// upload.
func TestPostProcessValidatesOutputsWithoutPayloads(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	empty := filepath.Join(f.testlogs, "pkg", "empty", "test.outputs")
	writeFile(t, filepath.Join(empty, "bazel_target_metadata.json"),
		`{"bazel.target": "//pkg:empty", "bazel.go.payload_selection": "module"}`)
	status, stdout, stderr, _ := f.run(t)
	if status != 1 || !strings.Contains(stderr, prefix+"missing JSON payloads under "+empty+"\n") {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout, stderr)
	}
}

// A cancellation that arrives once the payloads are sent stops the doctor's
// checks with the interrupted status.
func TestPostProcessChecksStopWhenCancelled(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	p := &poster{ctx: ctx, request: f.request, cfg: f.request.Config, workers: 2,
		d:   &doctor{ctx: ctx, out: &output{level: levels["INFO"]}, cache: newOutputs(), report: newReport(Args{}), workers: 2},
		log: newPostLog("INFO", &stdout, &stderr, "")}
	output := postOutput{path: f.output, outputKey: "pkg/target/test.outputs"}
	outputs, err := p.prepareValidation([]postOutput{output}, nil)
	if err != nil || len(outputs) != 1 {
		t.Fatalf("outputs %v, err %v", outputs, err)
	}
	if status := p.validateOutputs(postDiscovery{outputs: outputs}, uploadRun{}); status != 130 {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout.String(), stderr.String())
	}
}

// The doctor's report that post-process writes accounts for the expected
// targets and the BEP as the standalone doctor's report does.
func TestPostProcessDoctorReportAccountsForBep(t *testing.T) {
	f := newPostFixture(t)
	f.validate("module")
	data, err := os.ReadFile(filepath.Join("testdata", "bep-fresh.json"))
	if err != nil {
		t.Fatal(err)
	}
	bep := filepath.Join(f.root, "bep.json")
	writeFile(t, bep, strings.ReplaceAll(string(data), "/ROOTPLACEHOLDER", f.root))
	f.request.Config.BepJSONFiles = []string{bep}
	f.request.Config.FreshnessSource, f.request.Config.FreshnessMode = "bep", "required"
	f.request.Config.FreshnessDisabledExplicitly = false
	f.request.Config.ExpectedTargets = []string{"//pkg:target"}
	f.request.Validation.ConfigPath = filepath.Join(f.root, "doctor.config.json")
	if status, stdout, stderr, _ := f.run(t); status != 0 {
		t.Fatalf("status %d\nstdout %s\nstderr %s", status, stdout, stderr)
	}
	var report struct {
		Config  map[string]any      `json:"config"`
		Targets map[string][]string `json:"targets"`
		Bep     map[string]any      `json:"bep"`
		Summary map[string]any      `json:"summary"`
		Result  map[string]any      `json:"result"`
	}
	text, err := os.ReadFile(f.doctorReport)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(text, &report); err != nil {
		t.Fatal(err)
	}
	want := []string{"//pkg:target"}
	for _, name := range []string{"expected", "seen_in_bep", "fresh"} {
		if strings.Join(report.Targets[name], ",") != strings.Join(want, ",") {
			t.Errorf("targets.%s = %v, want %v", name, report.Targets[name], want)
		}
	}
	if report.Bep["eligible_outputs"] != float64(1) || report.Config["expected_targets_source"] != "uploader" ||
		report.Config["config_path"] != filepath.Join(f.root, "doctor.config.json") ||
		report.Summary["expected_targets"] != float64(1) || report.Result["status"] != "ok" {
		t.Fatalf("report %s", text)
	}
}

// A request from newer rules, with a field or a schema version that this
// ddtest does not know, leaves the invocation to the Python uploader with
// nothing printed. A malformed request still fails.
func TestPostProcessFallsBackOnNewerRequests(t *testing.T) {
	posixOnly(t)
	for name, input := range map[string]string{
		"unknown field": `{"schema_version": 1, "config": {}, "resources": {}, "future": true}`,
		"newer version": `{"schema_version": 2, "config": {}, "resources": {}}`,
	} {
		var stdout, stderr bytes.Buffer
		if status := PostCommand(context.Background(), "ddtest", nil, strings.NewReader(input), &stdout, &stderr); status != PostFallbackExit ||
			stdout.Len() != 0 || stderr.Len() != 0 {
			t.Errorf("%s: status %d stdout %q stderr %q", name, status, stdout.String(), stderr.String())
		}
	}
	var stdout, stderr bytes.Buffer
	if status := PostCommand(context.Background(), "ddtest", nil, strings.NewReader(`{"schema_version": "x"}`), &stdout, &stderr); status != 2 {
		t.Errorf("malformed request: status %d", status)
	}
}
