package bazeldoctor

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The BEP fixtures were written by the rules' complete_bep_json and
// _bep_test_result test helpers, for //pkg:target with its output below
// ROOTPLACEHOLDER.
func withBep(t *testing.T, name string) func(*Request) {
	return func(request *Request) {
		root := filepath.Dir(request.ConfigPath)
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		bep := filepath.Join(root, "bep.json")
		writeFile(t, bep, strings.ReplaceAll(string(data), "/ROOTPLACEHOLDER", root))
		request.BepFiles = []string{bep}
		request.Args.BepJSON = []string{bep}
		request.Args.FreshnessSource, request.Args.FreshnessMode = "bep", "required"
	}
}

func TestValidateStrictFreshBep(t *testing.T) {
	result, stdout, stderr, report := validate(t, `["//pkg:target"]`, withBep(t, "bep-fresh.json"))
	if result.Status != "done" || result.ExitCode != 0 || !strings.Contains(stdout, "OK: validated 1 test output directorie(s)") {
		t.Fatalf("result %+v\nstdout %s\nstderr %s", result, stdout, stderr)
	}
	bep := report["bep"].(map[string]any)
	if bep["eligible_outputs"] != float64(1) || bep["strict_completeness"] != true {
		t.Fatalf("bep %v", bep)
	}
}

func TestValidateAllCachedBep(t *testing.T) {
	result, stdout, stderr, _ := validate(t, `["//pkg:target"]`, func(request *Request) {
		withBep(t, "bep-cached.json")(request)
		_ = os.RemoveAll(filepath.Join(filepath.Dir(request.ConfigPath), "pkg"))
	})
	want := "OK: all expected targets were cached or skipped as platform-incompatible"
	if result.Status != "done" || result.ExitCode != 0 || !strings.Contains(stdout, want) {
		t.Fatalf("result %+v\nstdout %s\nstderr %s", result, stdout, stderr)
	}
}

func TestValidateRequiredBepIsMissing(t *testing.T) {
	result, _, stderr, report := validate(t, `["//pkg:target"]`, func(request *Request) {
		request.Args.FreshnessSource, request.Args.FreshnessMode = "bep", "required"
	})
	if result.ExitCode != 1 || !strings.Contains(stderr, "BEP freshness validation is required but no BEP JSON file was configured") {
		t.Fatalf("result %+v stderr %s", result, stderr)
	}
	if report["result"].(map[string]any)["reason_code"] != "missing_bep_json" {
		t.Fatalf("report %v", report["result"])
	}
}

// The default collector's bundle holds the report, a summary of the BEP and
// the redacted command flags.
func TestValidateWritesSupportBundle(t *testing.T) {
	var bundle string
	result, _, stderr, _ := validate(t, `["//pkg:target"]`, func(request *Request) {
		withBep(t, "bep-fresh.json")(request)
		bundle = filepath.Join(filepath.Dir(request.ConfigPath), "out", "support.zip")
		request.Args.SupportBundle = bundle
		request.SupportBundle = &SupportBundle{Runtime: map[string]string{"platform": "test", "python": "3",
			"bazel": "bazel", "bazel_version": "unknown", "bazel_version_error": ""}, Renderer: true}
	})
	if result.ExitCode != 0 {
		t.Fatalf("result %+v stderr %s", result, stderr)
	}
	archive, err := zip.OpenReader(bundle)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = archive.Close() }()
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	want := "bep/1_bep.summary.json,command/flags.json,diagnostics.json,environment/runtime.json,redaction-manifest.json," +
		"reports/doctor-report.json,summary.md"
	if strings.Join(names, ",") != want {
		t.Fatalf("bundle files %v", names)
	}
}

func TestValidateCollectorMissingWarns(t *testing.T) {
	result, _, stderr, _ := validate(t, `["//pkg:target"]`, func(request *Request) {
		request.Args.SupportBundle = filepath.Join(filepath.Dir(request.ConfigPath), "support.zip")
		request.SupportBundle = &SupportBundle{CollectorMissing: true}
	})
	if result.ExitCode != 0 || stderr != prefix+"warning: support bundle collector not found\n" {
		t.Fatalf("result %+v stderr %q", result, stderr)
	}
}

func TestValidateConfigurationFailures(t *testing.T) {
	for _, c := range []struct {
		name   string
		change func(*Request)
		want   string
	}{
		{"unsorted targets file", func(request *Request) {
			file := filepath.Join(filepath.Dir(request.ConfigPath), "targets.json")
			writeFile(t, file, `{"schema_version": 1, "targets": ["//pkg:target", "//a:b"]}`)
			request.Args.ExpectedTargetsFile = file
		}, "expected_targets_file targets must be sorted"},
		{"targets file version", func(request *Request) {
			file := filepath.Join(filepath.Dir(request.ConfigPath), "targets.json")
			writeFile(t, file, `{"schema_version": 2, "targets": []}`)
			request.ExpectedTargetsFile = file
		}, "expected_targets_file schema_version must be 1, got 2"},
		{"runtime and configured targets", func(request *Request) {
			request.Args.ExpectedTargets = []string{"//pkg:other"}
		}, "configured and runtime expected targets contain different target sets"},
		{"invalid label", func(request *Request) {
			request.Args.ExpectedTargets = []string{"//pkg:target", "@ext//pkg:target"}
		}, "expected_targets does not support external labels"},
		{"context entry", func(request *Request) {
			request.Args.ContextEntries = []string{"repo"}
		}, "--context-entry must use <apparent-repo-name>=<context-json-path>"},
		{"context without telemetry", func(request *Request) {
			context := filepath.Join(filepath.Dir(request.ConfigPath), "ctx", "context.json")
			writeFile(t, context, `{}`)
			request.Args.ContextEntries = []string{"repo=" + context}
		}, "context entry for 'repo' is missing sibling telemetry_facts.json"},
		{"bazelrc", func(request *Request) {
			root := filepath.Dir(request.ConfigPath)
			writeFile(t, filepath.Join(root, ".bazelrc"), "test --test_env=DD_API_KEY\n")
			request.Config = []byte(strings.Replace(string(request.Config), `"forbid_dd_git_test_env": false`,
				`"forbid_dd_git_test_env": true`, 1))
		}, ".bazelrc:1 sets DD_API_KEY with --test_env"},
		{"git metadata", func(request *Request) {
			request.Config = []byte(strings.Replace(string(request.Config), `"require_git_metadata": false`,
				`"require_git_metadata": true`, 1))
		}, "require_git_metadata=True but no context.json was provided in data"},
		{"selection", func(request *Request) {
			request.Config = []byte(strings.Replace(string(request.Config), `["module"]`, `["full_bundle_fallback"]`, 1))
		}, "payload_selection"},
	} {
		t.Run(c.name, func(t *testing.T) {
			result, _, stderr, _ := validate(t, `["//pkg:target"]`, c.change)
			if result.Status != "done" || result.ExitCode != 1 || !strings.Contains(stderr, c.want) {
				t.Fatalf("result %+v stderr %q", result, stderr)
			}
		})
	}
}
