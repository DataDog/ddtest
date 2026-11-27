package bazeldoctor

import (
	"strings"
)

// report is the doctor's machine-readable report, built as
// _new_diagnostic_report builds it and written with json.dumps(indent=2,
// sort_keys=True).
type report map[string]any

func newReport(args Args) report {
	return report{
		"schema_version": 1,
		"tool":           "dd-test-optimization-doctor",
		"status":         "running",
		"result": map[string]any{
			"status":      "running",
			"reason_code": "running",
			"reason":      "Doctor validation is still running.",
			"next_steps":  []any{},
		},
		"config": map[string]any{
			"config_path":             "",
			"workspace":               "",
			"testlogs_dir":            "",
			"expected_targets":        []any{},
			"expected_targets_source": "discovery",
			"freshness_source":        args.FreshnessSource,
			"freshness_mode":          args.FreshnessMode,
			"artifact_source":         args.ArtifactSource,
			"remote_artifacts":        args.RemoteArtifacts,
		},
		"bep": map[string]any{
			"files":                     []any{},
			"seen_targets":              []any{},
			"eligible_outputs":          0,
			"eligible_output_keys":      []any{},
			"cached_outputs":            0,
			"cached_output_keys":        []any{},
			"remote_only_outputs":       []any{},
			"missing_output_mappings":   []any{},
			"skipped_targets":           []any{},
			"artifact_references":       0,
			"selected_artifact_outputs": []any{},
			"blocked_artifact_labels":   []any{},
		},
		"artifacts": map[string]any{
			"staging_requested": false,
			"staging_enabled":   false,
			"staging_dir":       "",
			"staged_count":      0,
			"staged":            []any{},
		},
		"targets": map[string]any{
			"expected":    []any{},
			"seen_in_bep": []any{},
			"fresh":       []any{},
			"cached":      []any{},
			"missing":     []any{},
			"remote_only": []any{},
			"skipped":     []any{},
		},
		"outputs": []any{},
		"summary": map[string]any{
			"expected_targets":      0,
			"validated_output_dirs": 0,
			"upload_candidates":     0,
			"payloads":              payloadCounts{}.report(),
			"metadata_files":        0,
			"payload_selection":     map[string]any{},
		},
		"errors":   []any{},
		"warnings": []any{},
	}
}

func (r report) section(name string) map[string]any {
	return r[name].(map[string]any)
}

// setResult is _set_report_result.
func (r report) setResult(status, reasonCode, reason string, nextSteps ...string) {
	steps := []any{}
	for _, step := range nextSteps {
		steps = append(steps, step)
	}
	r["result"] = map[string]any{"status": status, "reason_code": reasonCode, "reason": reason, "next_steps": steps}
}

// nonEmpty is Python's truthiness of a report list.
func nonEmpty(value any) bool {
	list, ok := value.([]any)
	return ok && len(list) > 0
}

// classify is _classify_diagnostic_failure.
func (r report) classify(message string) (string, string, []string) {
	targets := r.section("targets")
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(message, "BEP freshness validation is required but no BEP JSON file was configured") ||
		strings.Contains(message, "BEP freshness source was selected but no BEP JSON file was configured") ||
		strings.Contains(message, "--artifact-source=bep requires --bep-json"):
		return "missing_bep_json", "BEP freshness or artifact staging was required, but no BEP JSON was configured.",
			[]string{"Pass --bep-json from the matching bazel test invocation."}
	case nonEmpty(targets["cached"]) && (strings.Contains(message, "not fresh in BEP") ||
		strings.Contains(message, "did not authorize") || strings.Contains(message, "only cached results")):
		return "target_cached_by_bazel", "Cached Bazel outputs did not satisfy the requested BEP freshness contract.",
			[]string{"Use the BEP from the exact matching bazel test invocation and verify each expected target is fresh or exclusively cached."}
	case nonEmpty(targets["missing"]):
		return "expected_target_not_seen_in_bep", "At least one expected target was not present in the BEP file.",
			[]string{"Use the BEP file produced by the same bazel test command that ran the expected target."}
	case nonEmpty(targets["remote_only"]) || strings.Contains(message, "remote-only"):
		return "bep_output_remote_only_without_downloader",
			"BEP selected remote-only outputs that could not be materialized locally.",
			[]string{"Enable --remote-artifacts=download; configure --bep-artifact-downloader for non-HTTP remote providers or HTTP endpoints requiring custom auth."}
	case strings.Contains(message, "no Test Optimization output directories found"):
		return "no_test_outputs_found", "No local or staged test.outputs directories were found.",
			[]string{"Use --artifact-source=bep with the matching --bep-json, or configure Bazel to materialize test outputs."}
	case strings.Contains(message, "outputs.zip") && strings.Contains(message, "payload") && strings.Contains(lower, "json"):
		return "zip_contains_no_payloads", "outputs.zip was found, but it did not contain payload JSON files.",
			[]string{"Inspect the archive for payloads/tests, payloads/coverage, or payloads/telemetry JSON files."}
	case strings.Contains(message, "payload") && strings.Contains(lower, "json"):
		return "no_payload_json_found", "Test output directories were found, but no JSON payloads were available.",
			[]string{"Inspect TEST_UNDECLARED_OUTPUTS_DIR and outputs.zip for payloads/tests, payloads/coverage, or payloads/telemetry files."}
	}
	return "doctor_failed", "Doctor validation failed before upload.", []string{"Inspect the doctor errors array and command logs."}
}

// recordFailure is _record_diagnostic_failure for a _fail.
func (r report) recordFailure(message string) {
	r["exit_code"] = 1
	r["status"] = "fail"
	if message != "" {
		r["errors"] = append(r["errors"].([]any), message)
	}
	code, reason, steps := r.classify(message)
	r.setResult("fail", code, reason, steps...)
}

// recordSuccess is main's ending for a run that returned 0.
func (r report) recordSuccess() {
	r["status"] = "ok"
	r["exit_code"] = 0
	r.setResult("ok", "ok", "Doctor validation succeeded.")
}

// reportList converts strings to a report list.
func reportList(values []string) []any {
	list := make([]any, len(values))
	for i, value := range values {
		list[i] = value
	}
	return list
}
