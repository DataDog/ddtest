package bazeldoctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noLabels(string) string { return "" }

func TestExecutionOutputKey(t *testing.T) {
	posixOnly(t)
	for raw, want := range map[string]string{
		"bazel-out/k8/testlogs/pkg/t/test.outputs/outputs.zip": "pkg/t/test.outputs",
		`bazel-out\k8\testlogs\pkg\t\test.outputs`:             "pkg/t/test.outputs",
		"./pkg/t/test.outputs":                                 "pkg/t/test.outputs",
		"bazel-out/k8/testlogs/pkg/t/test.log":                 "",
		"/abs/testlogs/a/testlogs/pkg/t/test.outputs/x/y":      "pkg/t/test.outputs",
	} {
		if got := executionOutputKey(raw); got != want {
			t.Errorf("executionOutputKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseExecutionLog(t *testing.T) {
	posixOnly(t)
	path := filepath.Join(t.TempDir(), "exec.json")
	writeFile(t, path, strings.Join([]string{
		`{"mnemonic":"TestRunner","targetLabel":"//pkg:a","listedOutputs":["bazel-out/k8/testlogs/pkg/a/test.outputs/outputs.zip"]}`,
		`{"mnemonic":"TestRunner","targetLabel":"//pkg:b","cacheHit":true,"listedOutputs":["bazel-out/k8/testlogs/pkg/b/test.outputs"]}`,
		`{"mnemonic":"TestRunner","targetLabel":"//pkg:c","runner":"remote cache hit","listedOutputs":["x/testlogs/pkg/c/test.outputs"]}`,
		`{"mnemonic":"GoCompile","targetLabel":"//pkg:d","listedOutputs":["x/testlogs/pkg/d/test.outputs"]}`,
		`{"mnemonic":"TestRunner","targetLabel":"//pkg:e","actualOutputs":[{"path":"x/testlogs/pkg/e/test.outputs/f"}]}`,
		"",
	}, "\n"))
	eligible, err := parseExecutionLog(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 2 || !eligible[pair{"//pkg:a", "pkg/a/test.outputs"}] || !eligible[pair{"//pkg:e", "pkg/e/test.outputs"}] {
		t.Fatalf("eligible %v", eligible)
	}
	writeFile(t, path, "{not json\n")
	if _, err := parseExecutionLog(path); err == nil || !strings.Contains(err.Error(), "invalid execution log JSON in") {
		t.Fatalf("err %v", err)
	}
}

func outputsOf(keys ...string) postDiscovery {
	var disc postDiscovery
	for i, key := range keys {
		path := "/testlogs/" + key
		disc.outputs = append(disc.outputs, postOutput{path: path, outputKey: key})
		disc.tasks = append(disc.tasks, postTask{id: itoa(i), kind: "test", outputDir: path, outputKey: key,
			sourcePath: path + "/payloads/tests/a.json"})
	}
	return disc
}

func TestSelectExpectedOutputs(t *testing.T) {
	posixOnly(t)
	disc := outputsOf("pkg/a/test.outputs", "pkg/a/shard_1_of_2/test.outputs", "pkg/b/test.outputs", "other/test.outputs")
	selected, err := selectExpectedOutputs(disc, []string{"//pkg:a"}, false, nil, noLabels)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.outputs) != 2 || len(selected.tasks) != 2 || selected.tasks[0].label != "//pkg:a" {
		t.Fatalf("selected %+v", selected)
	}
	if _, err := selectExpectedOutputs(disc, []string{"//pkg:a", "//pkg:missing"}, false, nil, noLabels); err == nil ||
		err.Error() != "expected targets have no local test.outputs: //pkg:missing" {
		t.Fatalf("err %v", err)
	}
	if _, err := selectExpectedOutputs(disc, []string{"//pkg:missing"}, true, nil, noLabels); err != nil {
		t.Fatalf("allowed missing: %v", err)
	}
	bep := map[pair]bool{{"//pkg:other", "pkg/b/test.outputs"}: true}
	if _, err := selectExpectedOutputs(disc, []string{"//pkg:b"}, false, bep, noLabels); err == nil ||
		!strings.HasPrefix(err.Error(), "BEP output target conflicts with expected target") {
		t.Fatalf("err %v", err)
	}
	retries := outputsOf("pkg/a/test.outputs", "pkg/a/test_attempts/attempt_1.outputs/test.outputs")
	retries.outputs[1].path = "/testlogs/pkg/a/test_attempts/attempt_1.outputs"
	label := func(string) string { return "//pkg:wrong" }
	if _, err := selectExpectedOutputs(retries, []string{"//pkg:a"}, false, nil, label); err == nil ||
		!strings.HasPrefix(err.Error(), "bazel.target metadata conflicts with expected target") {
		t.Fatalf("err %v", err)
	}
}

func TestNoExpectedPayloads(t *testing.T) {
	posixOnly(t)
	plan := emptyPlan()
	plan.cached[pair{"//pkg:a", "pkg/a/test.outputs"}] = true
	plan.skippedTargets["//pkg:b"] = true
	if !noExpectedPayloads([]string{"//pkg:a", "//pkg:b"}, plan) {
		t.Fatal("cached and skipped targets need no payloads")
	}
	plan.eligible[pair{"//pkg:c", "pkg/c/test.outputs"}] = true
	if noExpectedPayloads([]string{"//pkg:a", "//pkg:b"}, plan) {
		t.Fatal("a fresh output needs its payloads")
	}
	if noExpectedPayloads(nil, emptyPlan()) {
		t.Fatal("no expected targets")
	}
}

func TestFilterForFreshness(t *testing.T) {
	posixOnly(t)
	disc := outputsOf("pkg/a/test.outputs", "pkg/b/test.outputs", "pkg/c/test.outputs")
	labels := map[string]string{"/testlogs/pkg/a/test.outputs": "//pkg:a", "/testlogs/pkg/b/test.outputs": "//pkg:b",
		"/testlogs/pkg/c/test.outputs": "//pkg:c"}
	label := func(dir string) string { return labels[dir] }
	plan := emptyPlan()
	plan.selectedSource, plan.eligibilityEnabled = "bep", true
	plan.eligible[pair{"//pkg:a", "pkg/a/test.outputs"}] = true
	plan.eligible[pair{"//pkg:c", "pkg/c/test.outputs"}] = true
	plan.blockedLabels["//pkg:c"] = true
	result, err := filterForFreshness(disc, plan, "auto", nil, label)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.discovery.outputs) != 1 || result.discovery.outputs[0].outputKey != "pkg/a/test.outputs" ||
		result.discovery.tasks[0].label != "//pkg:a" {
		t.Fatalf("selected %+v", result.discovery)
	}
	if strings.Join(result.skipped, ",") != "pkg/b/test.outputs,pkg/c/test.outputs" ||
		strings.Join(result.identified, ",") != "//pkg:a,//pkg:c" {
		t.Fatalf("skipped %v identified %v", result.skipped, result.identified)
	}
	if strings.Join(result.discovery.warnings, ",") != "freshness_outputs_skipped" {
		t.Fatalf("warnings %v", result.discovery.warnings)
	}
	disabled := emptyPlan()
	if result, err := filterForFreshness(disc, disabled, "disabled", nil, label); err != nil || len(result.discovery.outputs) != 3 {
		t.Fatalf("disabled freshness selects every output: %+v, %v", result, err)
	}
}

// Without a sidecar label, required freshness cannot authorize an output.
func TestFilterForFreshnessRequiresLabels(t *testing.T) {
	posixOnly(t)
	root := t.TempDir()
	output := filepath.Join(root, "pkg", "a", "test.outputs")
	writeFile(t, filepath.Join(output, "bazel_target_metadata.json"), `{}`)
	disc := postDiscovery{outputs: []postOutput{{path: output, outputKey: "pkg/a/test.outputs"}},
		tasks: []postTask{{id: "1", kind: "test", outputDir: output, outputKey: "pkg/a/test.outputs"}}}
	plan := emptyPlan()
	plan.selectedSource, plan.eligibilityEnabled, plan.requireTestResults = "bep", true, true
	plan.eligible[pair{"//pkg:a", "pkg/a/test.outputs"}] = true
	_, err := filterForFreshness(disc, plan, "required", nil, noLabels)
	if err == nil || !strings.Contains(err.Error(), "bazel.target metadata is missing or unsafe") {
		t.Fatalf("err %v", err)
	}
}

func TestValidateAccounted(t *testing.T) {
	posixOnly(t)
	disc := outputsOf("pkg/a/test.outputs")
	disc.tasks[0].label = "//pkg:a"
	plan := emptyPlan()
	plan.selectedSource, plan.requireTestResults = "bep", true
	plan.eligible[pair{"//pkg:a", "pkg/a/test.outputs"}] = true
	ok := map[string]postResult{"0": {Status: "succeeded", TestEvents: 1}}
	if warning, err := validateAccounted(plan, disc, ok, []string{"//pkg:a"}, true, nil, nil); err != nil || warning != "" {
		t.Fatalf("warning %q err %v", warning, err)
	}
	if _, err := validateAccounted(plan, disc, ok, []string{"//pkg:a", "//pkg:gone"}, true, nil, nil); err == nil ||
		err.Error() != "expected target output is neither fresh nor exclusively cached in BEP: //pkg:gone (no TestResult matched this target)" {
		t.Fatalf("err %v", err)
	}
	none := map[string]postResult{"0": {Status: "skipped"}}
	if _, err := validateAccounted(plan, disc, none, []string{"//pkg:a"}, true, nil, nil); err == nil ||
		err.Error() != "fresh expected test output produced no uploadable payloads: //pkg:a pkg/a/test.outputs" {
		t.Fatalf("err %v", err)
	}
	plan.remoteOnly = []remoteOutput{{label: "//pkg:a", outputKey: "pkg/a/test.outputs"}}
	if _, err := validateAccounted(plan, disc, ok, []string{"//pkg:a"}, true, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "remote-only test outputs for //pkg:a") || err.(*postError).exitCode != 1 {
		t.Fatalf("err %v", err)
	}
	plan.remoteOnly = nil
	plan.ambiguousLabels["//pkg:a"] = true
	if _, err := validateAccounted(plan, disc, ok, []string{"//pkg:a"}, true, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err %v", err)
	}
	if warning, err := validateAccounted(emptyPlan(), disc, ok, nil, true, nil, nil); err != nil || warning != "" {
		t.Fatal("only BEP freshness is accounted")
	}
}

func TestPostTestsExecuted(t *testing.T) {
	posixOnly(t)
	root := t.TempDir()
	if testsExecuted([]scanRoot{{path: root}}) {
		t.Fatal("no test.log yet")
	}
	writeFile(t, filepath.Join(root, "pkg", "t", "test.log"), "")
	if !testsExecuted([]scanRoot{{path: root}}) {
		t.Fatal("test.log not found")
	}
	if testsExecuted([]scanRoot{{path: filepath.Join(root, "missing")}}) {
		t.Fatal("a missing root ran no tests")
	}
}

func TestCommonRoot(t *testing.T) {
	posixOnly(t)
	root := t.TempDir()
	for _, dir := range []string{"a/x", "a/y"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := commonRoot([]string{filepath.Join(root, "a/x/f.json"), filepath.Join(root, "a/y/g.json")})
	if got != purePath(filepath.Join(root, "a")) {
		t.Fatalf("commonRoot %s", got)
	}
}
