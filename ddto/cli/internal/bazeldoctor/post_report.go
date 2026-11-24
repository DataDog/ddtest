package bazeldoctor

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sort"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelupload"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
)

// This file is the uploader's reporting.py: one aggregate gives the summary
// lines and the schema v1 report.

const maxTestPayloadBytes = 4_718_592

type phaseTime struct {
	name      string
	wall, cpu float64
}

// aggregateReport is AggregateReport.
type aggregateReport struct {
	dryRun            bool
	exitCode          int
	configuredWorkers int
	elapsed           float64
	discovered        map[string]int
	results           []postResult
	cancelled         int
	warnings          []string
	phases            []phaseTime
	processCPU        *[2]float64
	retention         map[string]any
	requests          []bazelupload.BatchRequest
	nativeWorkers     int
}

// legacyContext is LegacyReportContext.
type legacyContext struct {
	validateEnrichment                               bool
	uploaderBackend, artifactSource, remoteArtifacts string
	freshnessSource, freshnessMode                   string
	allowCachedPayloadUploads                        bool
	bepFiles                                         []string
	selectedSource                                   string
	strictCompleteness                               bool
	completenessErrors                               []pair
	eligible, cached, remoteOnly, skippedOutputs     int
	missingOutputLabels, skippedTargets              int
	stagingDir                                       string
	stagedTestlogsDirs, selectedRemote, stagedRemote int
	remoteIgnored, testOutputsDirs                   int
	reasonCode, reason                               string
	nextSteps                                        []string
}

func (r aggregateReport) mode() string {
	if r.dryRun {
		return "dry-run"
	}
	return "upload"
}

func (r aggregateReport) resultName() string {
	if r.exitCode == 0 {
		return "success"
	}
	for _, result := range r.results {
		if result.Status == "succeeded" || result.Requests.Succeeded > 0 {
			return "partial_failure"
		}
	}
	return "failure"
}

func statusCount(results []postResult, kind, status string) int {
	count := 0
	for _, result := range results {
		if (kind == "" || string(result.Kind) == kind) && result.Status == status {
			count++
		}
	}
	return count
}

func round6(value float64) float64 {
	return math.Round(value*1e6) / 1e6
}

// statistics is AggregateReport.statistics.
func (r aggregateReport) statistics() map[string]any {
	succeeded, failed := statusCount(r.results, "", "succeeded"), statusCount(r.results, "", "failed")
	skipped := statusCount(r.results, "", "skipped")
	processed := len(r.results)
	eligible := processed + r.cancelled
	deleted, discovered := 0, 0
	for _, result := range r.results {
		if result.Deleted {
			deleted++
		}
	}
	types, discoveredByType := map[string]any{}, map[string]any{}
	for _, kind := range payloadKinds {
		types[kind] = map[string]any{"succeeded": statusCount(r.results, kind, "succeeded"),
			"failed": statusCount(r.results, kind, "failed"), "skipped": statusCount(r.results, kind, "skipped")}
		discoveredByType[kind] = r.discovered[kind]
		discovered += r.discovered[kind]
	}
	failures, warnings := map[string]any{}, map[string]any{}
	count := func(counts map[string]any, code string) {
		value, _ := counts[code].(int)
		counts[code] = value + 1
	}
	for _, code := range r.warnings {
		count(warnings, code)
	}
	var sourceBytes, preparedBytes, split, created, uploaded, chunksFailed, oversized int
	requests := map[string]int{}
	for _, result := range r.results {
		if result.Failure != "" {
			count(failures, result.Failure)
		}
		for _, code := range result.Warnings {
			count(warnings, code)
		}
		sourceBytes += result.SourceBytes
		preparedBytes += result.PreparedBytes
		if result.ChunksCreated > 1 {
			split++
		}
		created += result.ChunksCreated
		uploaded += result.ChunksUploaded
		chunksFailed += result.ChunksFailed
		if result.Failure == "single_event_exceeds_payload_limit" {
			oversized++
		}
		if len(result.RequestIDs) == 0 {
			requests["planned"] += result.Requests.Planned
			requests["attempted"] += result.Requests.Attempted
			requests["succeeded"] += result.Requests.Succeeded
			requests["failed"] += result.Requests.Failed
			requests["retries"] += result.Requests.Retries
		}
	}
	for _, request := range r.requests {
		requests["planned"]++
		requests["attempted"] += request.Attempts
		requests["retries"] += max(0, request.Attempts-1)
		if request.Succeeded != nil && *request.Succeeded {
			requests["succeeded"]++
		} else if request.Succeeded != nil {
			requests["failed"]++
		}
	}
	document := map[string]any{
		"mode": r.mode(), "result": r.resultName(), "exit_code": r.exitCode, "elapsed_seconds": round6(r.elapsed),
		"files": map[string]any{"discovered": discovered, "eligible": eligible, "processed": processed,
			"succeeded": succeeded, "failed": failed, "skipped": skipped, "cancelled": r.cancelled,
			"deleted": deleted, "retained": max(0, eligible-deleted)},
		"payload_types": types, "discovered_by_type": discoveredByType,
		"concurrency": map[string]any{"workers": r.configuredWorkers, "worker_threads": 0, "peak_active_workers": 0,
			"native_workers": r.nativeWorkers},
		"splitting": map[string]any{"threshold_bytes": maxTestPayloadBytes, "source_files_split": split,
			"chunks_created": created, "chunks_uploaded": uploaded, "chunks_failed": chunksFailed,
			"oversized_single_events": oversized},
		"requests": map[string]any{"planned": requests["planned"], "attempted": requests["attempted"],
			"succeeded": requests["succeeded"], "failed": requests["failed"], "retries": requests["retries"]},
		"warnings": warnings, "failures": failures,
	}
	if len(r.phases) > 0 {
		phases := map[string]any{}
		for _, phase := range r.phases {
			phases[phase.name] = map[string]any{"wall_seconds": phase.wall, "main_thread_cpu_seconds": phase.cpu}
		}
		var cpu any
		if r.processCPU != nil {
			cpu = map[string]any{"user": r.processCPU[0], "system": r.processCPU[1]}
		}
		document["performance"] = map[string]any{"invocation_wall_seconds": r.elapsed, "worker_phases": map[string]any{},
			"execution_environment": map[string]any{"host_logical_cpus": runtime.NumCPU(),
				"gomaxprocs": runtime.GOMAXPROCS(0), "go_version": runtime.Version()},
			"invocation_phases": phases, "processed_source_bytes": sourceBytes, "prepared_body_bytes": preparedBytes,
			"process_cpu_seconds": cpu}
	}
	if len(r.retention) > 0 {
		document["retention"] = r.retention
	}
	if len(r.requests) > 0 {
		shared, sources := 0, map[string]any{}
		for _, request := range r.requests {
			if len(request.Sources) > 1 {
				shared++
			}
			sources[request.ID] = reportList(request.Sources)
		}
		document["batching"] = map[string]any{"logical_requests": len(r.requests), "shared_requests": shared,
			"request_sources": sources}
	}
	return document
}

// humanLines is AggregateReport.human_lines.
func (r aggregateReport) humanLines() []string {
	stats := r.statistics()
	files := stats["files"].(map[string]any)
	types := stats["payload_types"].(map[string]any)
	splitting := stats["splitting"].(map[string]any)
	requests := stats["requests"].(map[string]any)
	typeValue := func(kind string) string {
		counts := types[kind].(map[string]any)
		return fmt.Sprintf("%d/%d/%d", counts["succeeded"], counts["failed"], counts["skipped"])
	}
	native := ""
	if r.nativeWorkers > 0 {
		native = fmt.Sprintf("native_workers=%d ", r.nativeWorkers)
	}
	lines := []string{
		fmt.Sprintf("[dd-uploader] summary: mode=%s result=%s exit_code=%d workers=%d peak=0 %selapsed=%.2fs",
			r.mode(), r.resultName(), r.exitCode, r.configuredWorkers, native, r.elapsed),
		fmt.Sprintf("[dd-uploader] files: discovered=%d eligible=%d processed=%d succeeded=%d failed=%d skipped=%d cancelled=%d",
			files["discovered"], files["eligible"], files["processed"], files["succeeded"], files["failed"], files["skipped"],
			files["cancelled"]),
		fmt.Sprintf("[dd-uploader] types: tests=%s coverage=%s telemetry=%s (succeeded/failed/skipped)",
			typeValue("test"), typeValue("coverage"), typeValue("telemetry")),
		fmt.Sprintf("[dd-uploader] split: files=%d chunks_created=%d chunks_uploaded=%d chunks_failed=%d",
			splitting["source_files_split"], splitting["chunks_created"], splitting["chunks_uploaded"], splitting["chunks_failed"]),
		fmt.Sprintf("[dd-uploader] requests: planned=%d attempted=%d succeeded=%d failed=%d retries=%d",
			requests["planned"], requests["attempted"], requests["succeeded"], requests["failed"], requests["retries"]),
		fmt.Sprintf("[dd-uploader] cleanup: deleted=%d retained=%d", files["deleted"], files["retained"]),
	}
	if performance, ok := stats["performance"].(map[string]any); ok {
		lines = append(lines, fmt.Sprintf("[dd-uploader] performance: processed_source_bytes=%d prepared_body_bytes=%d",
			performance["processed_source_bytes"], performance["prepared_body_bytes"]),
			"[dd-uploader] phase times: worker wall/CPU sums overlap; inclusive parents contain children")
		phases := performance["invocation_phases"].(map[string]any)
		names := make([]string, 0, len(phases))
		for name := range phases {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			values := phases[name].(map[string]any)
			lines = append(lines, fmt.Sprintf("[dd-uploader] invocation phase %s: wall=%.3fs main_thread_cpu=%.3fs",
				name, values["wall_seconds"], values["main_thread_cpu_seconds"]))
		}
		if r.processCPU != nil {
			lines = append(lines, fmt.Sprintf("[dd-uploader] process CPU: user=%.3fs system=%.3fs", r.processCPU[0], r.processCPU[1]))
		}
	}
	return lines
}

// legacyCounts is _legacy_payload_type_counts.
func (r aggregateReport) legacyCounts() map[string]map[string]int {
	counts := map[string]map[string]int{}
	for kind, key := range map[string]string{"test": "tests", "coverage": "coverage"} {
		counts[key] = map[string]int{"processed": statusCount(r.results, kind, "succeeded"),
			"failed": statusCount(r.results, kind, "failed"), "skipped": statusCount(r.results, kind, "skipped")}
	}
	processed, failed := 0, 0
	for _, result := range r.results {
		if result.Kind != bazelupload.Telemetry {
			continue
		}
		switch {
		case r.dryRun && result.Status == "succeeded":
			processed += result.Requests.Planned
		case !r.dryRun:
			processed += result.Requests.Succeeded
		}
		if result.Requests.Failed > 0 {
			failed += result.Requests.Failed
		} else if result.Status == "failed" {
			failed++
		}
	}
	counts["telemetry"] = map[string]int{"processed": processed, "failed": failed,
		"skipped": statusCount(r.results, "telemetry", "skipped")}
	return counts
}

// resultReason is _result_reason.
func (r aggregateReport) resultReason(context legacyContext) (string, string, []string) {
	if context.reasonCode != "" {
		return context.reasonCode, context.reason, context.nextSteps
	}
	stats := r.statistics()
	attempted := stats["requests"].(map[string]any)["attempted"].(int)
	switch {
	case context.testOutputsDirs == 0:
		return "no_test_outputs_found", "No local or staged test.outputs directories were found.",
			[]string{"Use --artifact-source=bep with the matching --bep-json, or configure Bazel to materialize test outputs."}
	case stats["files"].(map[string]any)["discovered"].(int) == 0:
		return "no_payload_json_found", "Test output directories were found, but no payloads were available.",
			[]string{"Inspect TEST_UNDECLARED_OUTPUTS_DIR and outputs.zip."}
	case r.exitCode != 0 && attempted > 0:
		return "upload_failed_http", "One or more payload uploads failed.",
			[]string{"Check HTTP status diagnostics and Datadog credentials/site configuration."}
	case r.exitCode != 0:
		return "payload_enrichment_failed", "Dry-run or payload processing failed for at least one payload.",
			[]string{"Inspect uploader logs for the first payload validation failure."}
	case r.dryRun:
		return "upload_skipped_dry_run", "Dry-run completed successfully; real upload was not requested.",
			[]string{"Run again without --dry-run to send payloads."}
	}
	return "ok", "Uploader completed successfully.", nil
}

// schemaV1 is AggregateReport.schema_v1_report.
func (r aggregateReport) schemaV1(context legacyContext) map[string]any {
	stats := r.statistics()
	status := "ok"
	if r.exitCode != 0 {
		status = "fail"
	}
	reasonCode, reason, nextSteps := r.resultReason(context)
	legacy := r.legacyCounts()
	failedPayloads, successful := 0, 0
	for _, counts := range legacy {
		failedPayloads += counts["failed"]
		successful += counts["processed"]
	}
	attempted := !r.dryRun && stats["requests"].(map[string]any)["attempted"].(int) > 0
	payloadsAttempted, payloadsUploaded := 0, 0
	if attempted {
		payloadsAttempted, payloadsUploaded = successful+failedPayloads, successful
	}
	errorsList := []any{}
	for _, e := range context.completenessErrors {
		errorsList = append(errorsList, map[string]any{"label": e.label, "message": e.key})
	}
	legacyMap := func(key string) map[string]any {
		return map[string]any{"processed": legacy[key]["processed"], "failed": legacy[key]["failed"],
			"skipped": legacy[key]["skipped"]}
	}
	discovered := stats["discovered_by_type"].(map[string]any)
	document := map[string]any{
		"schema_version": 1, "tool": "dd-test-optimization-uploader", "status": status, "exit_code": r.exitCode,
		"result": map[string]any{"status": status, "reason_code": reasonCode, "reason": reason,
			"next_steps": reportList(nextSteps)},
		"config": map[string]any{"dry_run": r.dryRun, "validate_enrichment": context.validateEnrichment,
			"uploader_backend": context.uploaderBackend, "artifact_source": context.artifactSource,
			"remote_artifacts": context.remoteArtifacts, "freshness_source": context.freshnessSource,
			"freshness_mode": context.freshnessMode, "allow_cached_payload_uploads": context.allowCachedPayloadUploads},
		"bep": map[string]any{"files": reportList(context.bepFiles), "freshness_selected_source": context.selectedSource,
			"strict_completeness": context.strictCompleteness, "completeness_errors": errorsList,
			"eligible_outputs": context.eligible, "cached_outputs": context.cached, "remote_only_outputs": context.remoteOnly,
			"skipped_outputs": context.skippedOutputs, "missing_output_labels": context.missingOutputLabels,
			"skipped_targets": context.skippedTargets},
		"artifacts": map[string]any{"source": context.artifactSource, "staging_dir": context.stagingDir,
			"staged_testlogs_dirs": context.stagedTestlogsDirs, "selected_remote_artifacts": context.selectedRemote,
			"staged_remote_artifacts": context.stagedRemote, "remote_artifacts_ignored": context.remoteIgnored},
		"upload": map[string]any{"attempted": attempted, "dry_run": r.dryRun, "payloads_attempted": payloadsAttempted,
			"payloads_uploaded": payloadsUploaded, "payloads_failed": failedPayloads},
		"payloads": map[string]any{"test_outputs_dirs": context.testOutputsDirs,
			"discovered": map[string]any{"tests": discovered["test"], "coverage": discovered["coverage"],
				"telemetry": discovered["telemetry"]},
			"tests": legacyMap("tests"), "coverage": legacyMap("coverage"), "telemetry": legacyMap("telemetry")},
		"upload_failures": failedPayloads,
	}
	for _, key := range []string{"files", "payload_types", "discovered_by_type", "concurrency", "splitting", "requests",
		"warnings", "failures", "performance", "retention", "batching"} {
		if value, ok := stats[key]; ok {
			document[key] = value
		}
	}
	return document
}

// writeJSONReport is _write_json: the report replaces path once complete.
func writeJSONReport(path string, document map[string]any) error {
	parent := pathParent(path)
	if err := os.MkdirAll(parent, 0o777); err != nil {
		return err
	}
	temporary := joinPath(parent, "."+pathName(path)+".tmp")
	if err := os.WriteFile(temporary, []byte(pyformat.DumpsIndented(document)+"\n"), 0o666); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
