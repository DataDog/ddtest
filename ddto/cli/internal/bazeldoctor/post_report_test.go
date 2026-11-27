package bazeldoctor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelupload"
)

func TestAggregateReport(t *testing.T) {
	posixOnly(t)
	succeeded := true
	report := aggregateReport{exitCode: 1, configuredWorkers: 8, elapsed: 1.25, nativeWorkers: 4,
		discovered: map[string]int{"test": 2, "coverage": 1, "telemetry": 1},
		results: []postResult{
			{ID: "a", Kind: bazelupload.Test, Status: "succeeded", Deleted: true, ChunksCreated: 2, ChunksUploaded: 2,
				RequestIDs: []string{"ddtest:r1"}},
			{ID: "b", Kind: bazelupload.Coverage, Status: "failed", Failure: "http_status_500",
				Requests: bazelupload.RequestCounts{Planned: 1, Attempted: 3, Failed: 1, Retries: 2}},
			{ID: "c", Kind: bazelupload.Telemetry, Status: "succeeded", Warnings: []string{"w"},
				Requests: bazelupload.RequestCounts{Planned: 2, Attempted: 2, Succeeded: 2}},
		},
		cancelled: 1, warnings: []string{"w"},
		requests: []bazelupload.BatchRequest{{ID: "ddtest:r1", Sources: []string{"a"}, Attempts: 1, Succeeded: &succeeded}},
	}
	stats := report.statistics()
	files := stats["files"].(map[string]any)
	if files["discovered"] != 4 || files["eligible"] != 4 || files["processed"] != 3 || files["deleted"] != 1 ||
		files["retained"] != 3 || files["cancelled"] != 1 {
		t.Fatalf("files %v", files)
	}
	requests := stats["requests"].(map[string]any)
	if requests["planned"] != 4 || requests["attempted"] != 6 || requests["succeeded"] != 3 || requests["failed"] != 1 ||
		requests["retries"] != 2 {
		t.Fatalf("requests %v", requests)
	}
	if stats["warnings"].(map[string]any)["w"] != 2 || stats["failures"].(map[string]any)["http_status_500"] != 1 {
		t.Fatalf("warnings %v failures %v", stats["warnings"], stats["failures"])
	}
	if report.resultName() != "partial_failure" || stats["splitting"].(map[string]any)["source_files_split"] != 1 {
		t.Fatalf("result %s", report.resultName())
	}
	lines := report.humanLines()
	if lines[0] != "[dd-uploader] summary: mode=upload result=partial_failure exit_code=1 workers=8 peak=0 native_workers=4 elapsed=1.25s" ||
		lines[2] != "[dd-uploader] types: tests=1/0/0 coverage=0/1/0 telemetry=1/0/0 (succeeded/failed/skipped)" {
		t.Fatalf("lines %q", lines)
	}
	document := report.schemaV1(legacyContext{testOutputsDirs: 2, bepFiles: []string{"bep.json"}, selectedSource: "bep"})
	result := document["result"].(map[string]any)
	if document["status"] != "fail" || result["reason_code"] != "upload_failed_http" ||
		document["upload"].(map[string]any)["payloads_failed"] != 1 || document["batching"] == nil {
		t.Fatalf("schema v1 %v %v", result, document["upload"])
	}
	telemetry := document["payloads"].(map[string]any)["telemetry"].(map[string]any)
	if telemetry["processed"] != 2 {
		t.Fatalf("telemetry %v", telemetry)
	}
}

func TestResultReasons(t *testing.T) {
	posixOnly(t)
	for _, c := range []struct {
		report  aggregateReport
		context legacyContext
		want    string
	}{
		{aggregateReport{}, legacyContext{}, "no_test_outputs_found"},
		{aggregateReport{}, legacyContext{testOutputsDirs: 1}, "no_payload_json_found"},
		{aggregateReport{exitCode: 1, discovered: map[string]int{"test": 1}}, legacyContext{testOutputsDirs: 1}, "payload_enrichment_failed"},
		{aggregateReport{dryRun: true, discovered: map[string]int{"test": 1}}, legacyContext{testOutputsDirs: 1}, "upload_skipped_dry_run"},
		{aggregateReport{discovered: map[string]int{"test": 1}}, legacyContext{testOutputsDirs: 1}, "ok"},
		{aggregateReport{}, legacyContext{reasonCode: "interrupted"}, "interrupted"},
	} {
		if code, _, _ := c.report.resultReason(c.context); code != c.want {
			t.Errorf("reason %s, want %s", code, c.want)
		}
	}
}

func TestWriteJSONReport(t *testing.T) {
	posixOnly(t)
	path := filepath.Join(t.TempDir(), "out", "report.json")
	if err := writeJSONReport(path, map[string]any{"b": 1, "a": json.Number("2.50")}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "{\n  \"a\": 2.50,\n  \"b\": 1\n}\n" {
		t.Fatalf("report %q", data)
	}
}

// Facts join the stream of their service: appended to its message batch, or
// sent in a synthetic batch beside another request.
func TestTelemetryPlan(t *testing.T) {
	posixOnly(t)
	root := t.TempDir()
	facts := filepath.Join(root, "telemetry_facts.json")
	writeFile(t, facts, `{"service_name": "svc", "runtime_name": "go", "env": "ci",
"counts": [{"name": "c", "value": 1}], "distributions": [{"name": "d", "value": [1]}]}`)
	task := func(name, kind string) postTask {
		path := filepath.Join(root, name+".json")
		writeFile(t, path, `{"api_version": "v2", "request_type": "`+kind+`", "runtime_id": "r", "seq_id": 3,
"application": {"service_name": "svc", "language_name": "go"}}`)
		return postTask{id: name, kind: "telemetry", sourcePath: path}
	}
	p := &poster{request: PostRequest{Resources: PostResources{TelemetryFactsPaths: []string{facts},
		PrimaryContext: "p", Contexts: map[string]bazelupload.Object{"p": {"ci.provider.name": " github "}}}}}
	plan, warnings := p.telemetryPlan([]postTask{task("batch", "message-batch"), task("started", "app-started")})
	if len(warnings) != 0 {
		t.Fatalf("warnings %v", warnings)
	}
	batch, started := plan.directives["batch"], plan.directives["started"]
	if batch == nil || len(batch.AppendMessages) != 2 || batch.Environment != "ci" || batch.ProviderSuffix != "github" ||
		started == nil || started.Environment != "ci" || len(started.SyntheticMessages) != 0 {
		t.Fatalf("directives %+v %+v", batch, started)
	}
	plan, _ = p.telemetryPlan([]postTask{task("started", "app-started")})
	if directive := plan.directives["started"]; len(directive.SyntheticMessages) != 2 || directive.SyntheticSeqID != 4 {
		t.Fatalf("synthetic %+v", directive)
	}
	writeFile(t, facts, `{"runtime_name": "go"}`)
	if _, warnings := p.telemetryPlan([]postTask{task("started", "app-started")}); strings.Join(warnings, ",") != "telemetry_facts_service_missing" {
		t.Fatalf("warnings %v", warnings)
	}
}

func TestReportingService(t *testing.T) {
	posixOnly(t)
	metadata := bazelupload.Object{"bazel.test_optimization.repo_name": "r", "bazel.test_optimization.service_name": "s",
		"bazel.test_optimization.runtime_name": "go"}
	context := bazelupload.Object{"topt.sync.repository_name": "r", "service.name": "s", "runtime.name": "go"}
	if service, err := reportingService(context, metadata); err != nil || service != "s" {
		t.Fatalf("service %q err %v", service, err)
	}
	if _, err := reportingService(bazelupload.Object{"service.name": "s"}, metadata); err == nil {
		t.Fatal("a mismatched context authorized the service")
	}
	if service, err := reportingService(nil, bazelupload.Object{"bazel.target": "//x:y"}); err != nil || service != "" {
		t.Fatal("legacy sidecars keep their service")
	}
}

// A failed source is copied with its sidecar and catalogued before the
// run's staging goes away.
func TestRetainPending(t *testing.T) {
	posixOnly(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "testlogs", "pkg", "a", "test.outputs")
	source := filepath.Join(output, "payloads", "tests", "a.json")
	writeFile(t, source, `{"events":[]}`)
	writeFile(t, filepath.Join(output, "bazel_target_metadata.json"), `{"bazel.target":"//pkg:a"}`)
	p := &poster{cfg: PostConfig{RetainedOutputsDir: filepath.Join(root, "retained"), ArtifactStagingDir: filepath.Join(root, "staging")},
		request: PostRequest{Resources: PostResources{Contexts: map[string]bazelupload.Object{"c": {"git.commit.sha": "x"}},
			ContextRecords: []ContextRecord{{RepoKey: "repo", Context: "c"}}}}}
	disc := postDiscovery{tasks: []postTask{{id: "file-000001", kind: "test", sourcePath: source, outputDir: output,
		outputKey: "pkg/a/test.outputs", label: "//pkg:a"}}}
	run := uploadRun{byID: map[string]postResult{"file-000001": {ID: "file-000001", Status: "failed", Failure: "http_status_500"}}}
	outcome, err := p.retainPending(disc, &aggregateReport{exitCode: 1}, nil, run, []string{"//pkg:a"}, "upload_failed_http")
	if err != nil || outcome.sourceCount != 1 || outcome.catalog == "" {
		t.Fatalf("outcome %+v err %v", outcome, err)
	}
	var catalog map[string]any
	data, _ := os.ReadFile(outcome.catalog)
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	entry := catalog["sources"].([]any)[0].(map[string]any)
	if entry["outcome"] != "failed" || entry["metadata"] == nil || catalog["uploader_reason_code"] != "upload_failed_http" ||
		len(catalog["contexts"].([]any)) != 1 {
		t.Fatalf("catalog %s", data)
	}
	p.cfg.RetainedOutputsDir = filepath.Join(output, "inside")
	if _, err := p.retainPending(disc, nil, nil, run, nil, "x"); err == nil {
		t.Fatal("a destination inside the source root was accepted")
	}
}

func TestWriteRetentionStatus(t *testing.T) {
	posixOnly(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(root, "status.json")
	if err := writeRetentionStatus(status, root, true); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(status); !strings.Contains(string(data), `"cleanup_safe": true`) {
		t.Fatalf("status %s", data)
	}
	if err := writeRetentionStatus(status, filepath.Join(root, "other"), true); err == nil {
		t.Fatal("a status outside the wrapper root was written")
	}
}
