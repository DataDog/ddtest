package bazeldoctor

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// The support bundle is the zip that create_support_bundle.py writes for
// the doctor: its report and command flags, redacted and bounded, a summary
// of each BEP file, the runtime metadata and a summary. The files hold what
// the collector writes; the archive's compressed bytes may differ.

// SupportBundle is what the Python doctor resolves for the support bundle.
type SupportBundle struct {
	// CollectorMissing is true when the doctor found no collector; it then
	// warns and writes no bundle.
	CollectorMissing bool `json:"collector_missing"`
	// Runtime is the collector's _runtime_metadata("bazel"), whose values
	// are strings.
	Runtime map[string]string `json:"runtime"`
	// Renderer is true when the collector loaded render_report_summary.py.
	Renderer bool `json:"renderer"`
	// OutputBase is _support_bundle_output_base(config_path), or empty.
	OutputBase string `json:"output_base"`
}

const (
	maxBepLabels      = 100
	maxJSONListItems  = 100
	maxJSONStringRune = 20000
	redacted          = "<redacted>"
)

// The collector's patterns, as its redaction manifest records them.
const (
	secretKeyPattern        = `(api[_-]?key|token|secret|password|credential|authorization)`
	secretAssignmentPattern = `(?P<prefix>(?:^|[\s,;])(?:--(?:test_env|repo_env|action_env|client_env)=)?` +
		`(?=[A-Za-z_][A-Za-z0-9_]*=)` +
		`(?=[A-Za-z0-9_]*(?:API[_-]?KEY|TOKEN|SECRET|PASSWORD|CREDENTIAL|AUTHORIZATION)[A-Za-z0-9_]*=)` +
		`[A-Za-z_][A-Za-z0-9_]*=)` +
		`(?P<value>.*?)(?=(?:[\s,;](?:--(?:test_env|repo_env|action_env|client_env)=)?` +
		`(?=[A-Za-z_][A-Za-z0-9_]*=)[A-Za-z_][A-Za-z0-9_]*=)|$)`
	httpURLPattern        = `https?://[^\s\"'<>]+`
	sensitiveQueryPattern = `([?&])([^=\s\"']*(?:sig|signature|token|credential|expires|x-amz-)[^=\s\"']*)=[^&\s\"']*`
)

// pythonSpaceClass is the characters that \s matches in a Python str pattern.
const pythonSpaceClass = `\t\n\v\f\r \x{1c}-\x{1f}\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`

var (
	secretKey      = regexp.MustCompile(`(?i)` + secretKeyPattern)
	sensitiveQuery = regexp.MustCompile(`(?i)([?&])([^=` + pythonSpaceClass + `"']*(?:sig|signature|token|credential|expires|x-amz-)[^=` +
		pythonSpaceClass + `"']*)=[^&` + pythonSpaceClass + `"']*`)
	remoteScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://`)
	casDigest    = regexp.MustCompile(`^[0-9a-f]{32,}/[0-9]+\n?$`)
	unsafeName   = regexp.MustCompile(`[^A-Za-z0-9_.-]`)
)

// writeSupportBundle is _write_doctor_support_bundle with the default
// collector, create_support_bundle.py.
func (d *doctor) writeSupportBundle() {
	args := d.request.Args
	if args.SupportBundle == "" {
		return
	}
	bundle := d.request.SupportBundle
	if bundle == nil {
		// A custom collector or renderer.
		unsupported("support bundle collector")
	}
	if bundle.CollectorMissing {
		d.out.warn("support bundle collector not found")
		return
	}
	output := expandUser(purePath(args.SupportBundle))
	tmp, err := os.MkdirTemp("", "dd-topt-doctor-support-")
	if err != nil {
		unsupported("support bundle directory: " + err.Error())
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	tmp = purePath(tmp)
	workspace := d.workspace
	if workspace == "" {
		workspace = workspaceRoot()
	}
	expected := []any{}
	if targets, ok := d.report["targets"].(map[string]any); ok {
		if list, ok := targets["expected"].([]any); ok {
			expected = list
		}
	}
	reportPath := joinPath(tmp, "doctor-report.json")
	manifest := map[string]any{
		"source":                              "doctor",
		"config":                              d.request.ConfigPath,
		"workspace_root":                      workspace,
		"doctor_report_json":                  reportPath,
		"uploader_report_json":                "",
		"upload_report_json":                  "",
		"upload_mode":                         "doctor_only_no_uploader",
		"bep_files":                           reportList(d.request.BepFiles),
		"freshness_source":                    args.FreshnessSource,
		"freshness_mode":                      args.FreshnessMode,
		"artifact_source":                     args.ArtifactSource,
		"remote_artifacts":                    args.RemoteArtifacts,
		"artifact_staging_dir":                args.ArtifactStagingDir,
		"bep_artifact_downloader":             args.BepArtifactDownloader,
		"bep_artifact_downloader_timeout_sec": args.BepArtifactDownloaderTimeout,
		"targets":                             expected,
	}
	data := createBundle(bundleInput{
		report: map[string]any(d.report), reportPath: reportPath, manifest: manifest,
		bepFiles: d.request.BepFiles, runtime: bundle.Runtime, renderer: bundle.Renderer,
		redaction: newRedaction(workspace, bundle.OutputBase, tmp), outputBase: bundle.OutputBase != "",
	})
	if err := os.MkdirAll(pathParent(output), 0o777); err != nil {
		unsupported("support bundle directory: " + err.Error())
	}
	if err := os.WriteFile(output, data, 0o666); err != nil {
		unsupported("support bundle: " + err.Error())
	}
}

// workspaceRoot is _workspace_root.
func workspaceRoot() string {
	workspace := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			unsupported("working directory")
		}
		workspace = cwd
	}
	return resolvePath(workspace)
}

type bundleInput struct {
	report     map[string]any
	reportPath string
	manifest   map[string]any
	bepFiles   []string
	runtime    map[string]string
	renderer   bool
	redaction  redaction
	outputBase bool
}

// createBundle is create_bundle with the doctor's collector arguments, and
// returns the zip.
func createBundle(input bundleInput) []byte {
	files := map[string]string{}
	bounded := boundJSON(input.redaction.json(plainJSON(input.report))).(map[string]any)
	archivePath := "reports/" + safeArchiveName(pathName(input.reportPath))
	records := []any{reportRecord(archivePath, bounded)}
	reports := []map[string]any{bounded}
	files[archivePath] = pyformat.DumpsIndented(bounded) + "\n"

	var bepSummaries []map[string]any
	var warnings []any
	for i, path := range input.bepFiles {
		index := i + 1
		archivePath := bepSummaryArchivePath(index, path)
		if !exists(path) {
			warnings = append(warnings, "BEP file "+itoa(index)+" was missing or unreadable and was skipped: "+
				safeArchiveName(pathName(path)))
			continue
		}
		summary := summarizeBep(path)
		summary["path"] = archivePath
		summary = boundJSON(summary).(map[string]any)
		bepSummaries = append(bepSummaries, summary)
		files[archivePath] = pyformat.DumpsIndented(summary) + "\n"
	}
	command := boundJSON(input.redaction.json(plainJSON(input.manifest)))
	files["command/flags.json"] = pyformat.DumpsIndented(command) + "\n"
	runtimeMetadata := map[string]any{}
	for key, value := range input.runtime {
		runtimeMetadata[key] = value
	}
	files["environment/runtime.json"] = pyformat.DumpsIndented(boundJSON(input.redaction.json(runtimeMetadata))) + "\n"
	outputBase := ""
	if input.outputBase {
		outputBase = "<output_base>"
	}
	files["redaction-manifest.json"] = pyformat.DumpsIndented(&pyformat.Ordered{
		Keys: []string{"workspace_root", "output_base", "tmp_root", "secret_keys", "secret_assignments",
			"http_url_redaction", "sensitive_query_parameters", "raw_payloads_included", "raw_bep_included"},
		Values: map[string]any{"workspace_root": "<workspace>", "output_base": outputBase, "tmp_root": "<tmp>",
			"secret_keys": secretKeyPattern, "secret_assignments": secretAssignmentPattern,
			"http_url_redaction": httpURLPattern, "sensitive_query_parameters": sensitiveQueryPattern,
			"raw_payloads_included": false, "raw_bep_included": false},
	}) + "\n"
	names := []string{"diagnostics.json", "summary.md"}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	diagnostics := bundleDiagnostics(records, reports, warnings, bepSummaries, names)
	files["summary.md"] = renderBundleSummary(input.renderer, reports, diagnostics)
	files["diagnostics.json"] = pyformat.DumpsIndented(diagnostics) + "\n"
	return writeBundleZip(files)
}

// plainJSON converts the report's values to the types that json.loads
// returns for them.
func plainJSON(value any) any {
	switch typed := value.(type) {
	case report:
		return plainJSON(map[string]any(typed))
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = plainJSON(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			result[i] = plainJSON(child)
		}
		return result
	case []string:
		result := make([]any, len(typed))
		for i, child := range typed {
			result[i] = child
		}
		return result
	case string, bool, nil, int, int64, float64:
		return typed
	}
	unsupported("report value of an unexpected type")
	return nil
}

// boundJSON is bound_json.
func boundJSON(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = boundJSON(child)
		}
		return result
	case []any:
		count := len(typed)
		kept := typed
		if count > maxJSONListItems {
			kept = typed[:maxJSONListItems]
		}
		result := make([]any, 0, len(kept)+1)
		for _, child := range kept {
			result = append(result, boundJSON(child))
		}
		if count > maxJSONListItems {
			result = append(result, map[string]any{"truncated": true, "total_items": count,
				"omitted_items": count - maxJSONListItems})
		}
		return result
	case string:
		if length := utf8.RuneCountInString(typed); length > maxJSONStringRune {
			cut := 0
			for i := 0; i < maxJSONStringRune; i++ {
				_, size := utf8.DecodeRuneInString(typed[cut:])
				cut += size
			}
			return typed[:cut] + "...<truncated " + itoa(length-maxJSONStringRune) + " chars>"
		}
	}
	return value
}

func safeArchiveName(name string) string {
	safe := unsafeName.ReplaceAllString(name, "_")
	if safe == "" {
		return "file"
	}
	return safe
}

// bepSummaryArchivePath is _bep_summary_archive_path.
func bepSummaryArchivePath(index int, path string) string {
	name := strings.TrimSuffix(pathName(path), ".json")
	return "bep/" + itoa(index) + "_" + safeArchiveName(name) + ".summary.json"
}

func getOr(object map[string]any, key string, fallback any) any {
	if value, ok := object[key]; ok {
		return value
	}
	return fallback
}

// pyStr is str(value) for the report's values.
func pyStr(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return pyformat.Repr(value)
}

// reportRecord is _report_record.
func reportRecord(archivePath string, report map[string]any) map[string]any {
	result, ok := getOr(report, "result", map[string]any{}).(map[string]any)
	if !ok {
		unsupported("report result")
	}
	return map[string]any{"path": archivePath, "tool": getOr(report, "tool", "unknown"),
		"status":      getOr(result, "status", getOr(report, "status", "unknown")),
		"reason_code": getOr(result, "reason_code", "unknown")}
}

// summarizeBep is summarize_bep.
func summarizeBep(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		// The collector reports the exception's type, which a Go error does
		// not name.
		unsupported("BEP summary: " + err.Error())
	}
	digest := sha256.Sum256(data)
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !utf8.ValidString(text) {
		unsupported("BEP that is not UTF-8")
	}
	// Text mode reads \r\n and \r as \n.
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	labels := map[string]bool{}
	events, cached, uploadable, remoteOnly := 0, 0, 0, 0
	byName := map[string]any{}
	for _, line := range strings.SplitAfter(text, "\n") {
		if pythonStrip(line) == "" {
			continue
		}
		event, err := pyjson.Parse([]byte(line))
		if err != nil {
			unsupported("BEP summary line")
		}
		var eventID any = &pyjson.Object{}
		if object, ok := event.(*pyjson.Object); ok {
			eventID = object.Lookup("id", nil)
		}
		resultID, ok := pyjson.Coalesced(eventID, "testResult", "test_result", nil).(*pyjson.Object)
		if !ok {
			continue
		}
		result, ok := pyjson.Coalesced(event, "testResult", "test_result", nil).(*pyjson.Object)
		if !ok {
			continue
		}
		events++
		if label, ok := resultID.Lookup("label", nil).(string); ok {
			labels[label] = true
		}
		executionInfo := pyjson.Coalesced(result, "executionInfo", "execution_info", &pyjson.Object{})
		isCached := pyjson.Truthy(pyjson.Coalesced(result, "cachedLocally", "cached_locally", false)) ||
			pyjson.Truthy(pyjson.Coalesced(executionInfo, "cachedRemotely", "cached_remotely", false))
		outputs, _ := pyjson.Coalesced(result, "testActionOutput", "test_action_output", []any{}).([]any)
		for _, output := range outputs {
			candidates := bepFileCandidates(output)
			name := uploadableOutputName(output, candidates)
			if name == "" {
				continue
			}
			uploadable++
			count, _ := byName[name].(int)
			byName[name] = count + 1
			if isCached {
				cached++
				continue
			}
			for _, candidate := range candidates {
				if isRemoteURI(candidate) {
					remoteOnly++
					break
				}
			}
		}
	}
	sorted := sortedKeys(labels)
	kept := sorted
	if len(kept) > maxBepLabels {
		kept = kept[:maxBepLabels]
	}
	return map[string]any{"source_sha256": hex.EncodeToString(digest[:]), "test_result_events": events,
		"labels": reportList(kept), "labels_total": len(sorted), "labels_truncated": len(sorted) > maxBepLabels,
		"cached_outputs": cached, "uploadable_outputs": uploadable, "remote_only_outputs": remoteOnly,
		"outputs_by_name": byName}
}

// bepFileCandidates is _bep_file_candidates.
func bepFileCandidates(output any) []string {
	if text, ok := output.(string); ok {
		return []string{text}
	}
	object, ok := output.(*pyjson.Object)
	if !ok {
		return nil
	}
	var values []string
	for _, key := range []string{"uri", "path", "name"} {
		if value, ok := object.Lookup(key, nil).(string); ok && value != "" {
			values = append(values, value)
		}
	}
	prefix, isList := pyjson.Coalesced(object, "pathPrefix", "path_prefix", []any{}).([]any)
	name, isName := object.Lookup("name", nil).(string)
	if isList && isName && name != "" {
		var parts []string
		for _, part := range prefix {
			if text, ok := part.(string); ok && text != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			values = append(values, strings.Join(append(parts, name), "/"))
		}
	}
	return values
}

// pythonLower is str.lower() where it matches strings.ToLower.
func pythonLower(text string) string {
	if strings.ContainsRune(text, '\u0130') {
		unsupported("dotted capital I")
	}
	return strings.ToLower(text)
}

// uploadableOutputName is _uploadable_output_name.
func uploadableOutputName(output any, candidates []string) string {
	if object, ok := output.(*pyjson.Object); ok {
		if name, ok := object.Lookup("name", nil).(string); ok && (name == "test.outputs" || name == "outputs.zip") {
			return name
		}
	}
	for _, candidate := range candidates {
		stripped, err := stripFileURI(candidate)
		if err != nil {
			unsupported("BEP file URI")
		}
		normalized := strings.TrimRight(pythonLower(strings.ReplaceAll(stripped, `\`, "/")), "/")
		if normalized == "outputs.zip" || strings.HasSuffix(normalized, "/outputs.zip") {
			return "outputs.zip"
		}
		if normalized == "test.outputs" || strings.Contains(normalized, "/test.outputs/") ||
			strings.HasSuffix(normalized, "/test.outputs") || strings.HasSuffix(normalized, "/test.log") ||
			strings.HasSuffix(normalized, "/test.xml") {
			return "test.outputs"
		}
	}
	return ""
}

// isRemoteURI is _is_remote_uri.
func isRemoteURI(value string) bool {
	if value == "" {
		return false
	}
	lowered := pythonLower(value)
	if strings.HasPrefix(lowered, "file://") {
		return false
	}
	return remoteScheme.MatchString(lowered) || strings.HasPrefix(lowered, "blobs/") || casDigest.MatchString(lowered)
}

// primaryReason is _primary_reason.
func primaryReason(reports []map[string]any) string {
	for _, report := range reports {
		result, _ := getOr(report, "result", map[string]any{}).(map[string]any)
		status := getOr(result, "status", report["status"])
		reasonCode := pyStr(getOr(result, "reason_code", "unknown"))
		if status != "ok" && status != "success" && reasonCode != "ok" {
			return reasonCode
		}
	}
	for i := len(reports) - 1; i >= 0; i-- {
		result, _ := getOr(reports[i], "result", map[string]any{}).(map[string]any)
		if reasonCode := pyStr(getOr(result, "reason_code", "unknown")); reasonCode != "unknown" {
			return reasonCode
		}
	}
	return "unknown"
}

// pyInt is int(value) for the counts that the doctor's report holds; false
// when Python raises TypeError.
func pyInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case nil, []any, map[string]any:
		return 0, false
	}
	// Text and fractions convert in Python's own ways.
	unsupported("count of an unexpected type")
	return 0, false
}

// truthy is bool(value) for the report's values.
func truthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case int:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return typed != ""
	case []any:
		return len(typed) > 0
	case map[string]any:
		return len(typed) > 0
	}
	return true
}

// bundleDiagnostics is _diagnostics.
func bundleDiagnostics(records []any, reports []map[string]any, warnings []any, bepSummaries []map[string]any,
	files []string) map[string]any {
	payloads := map[string]int{"tests": 0, "coverage": 0, "telemetry": 0}
	for _, report := range reports {
		top, ok := getOr(report, "payloads", map[string]any{}).(map[string]any)
		if !ok {
			unsupported("report payloads")
		}
		discovered := top["discovered"]
		if !truthy(discovered) {
			summary, ok := getOr(report, "summary", map[string]any{}).(map[string]any)
			if !ok {
				unsupported("report summary")
			}
			discovered = summary["payloads"]
		}
		if !truthy(discovered) {
			discovered = map[string]any{}
		}
		counts, ok := discovered.(map[string]any)
		if !ok {
			unsupported("report payload counts")
		}
		for _, key := range []string{"tests", "coverage", "telemetry"} {
			if count, ok := pyInt(getOr(counts, key, 0)); ok && count > payloads[key] {
				payloads[key] = count
			}
		}
	}
	allWarnings := append([]any{}, warnings...)
	if len(records) == 0 {
		allWarnings = append(allWarnings, "No usable doctor or uploader reports were included.")
	}
	summaries := []any{}
	for i, summary := range bepSummaries {
		remote, ok := pyInt(getOr(summary, "remote_only_outputs", 0))
		if !ok {
			unsupported("remote-only count")
		}
		if remote != 0 {
			allWarnings = append(allWarnings, "BEP file "+itoa(i+1)+" contained "+itoa(remote)+
				" remote-only uploadable outputs.")
		}
		summaries = append(summaries, summary)
	}
	failed := len(records) == 0
	for _, record := range records {
		status := record.(map[string]any)["status"]
		failed = failed || status != "ok" && status != "success"
	}
	status, reason := "ok", primaryReason(reports)
	if failed {
		status = "fail"
	}
	if len(records) == 0 {
		reason = "missing_reports"
	}
	return map[string]any{
		"schema_version": 1,
		"tool":           "dd-test-optimization-support-bundle",
		"summary": map[string]any{"status": status, "primary_reason_code": reason, "report_count": len(reports),
			"bep_summary_count": len(bepSummaries),
			"payloads": map[string]any{"tests": payloads["tests"], "coverage": payloads["coverage"],
				"telemetry": payloads["telemetry"]},
			"warnings": allWarnings},
		"reports": records,
		"bep":     summaries,
		"bundle": map[string]any{"files": reportList(files),
			"created_by": "tools/test_optimization/create_support_bundle.py"},
	}
}

const pythonWhitespace = "\t\n\v\f\r \x1c\x1d\x1e\x1f\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006" +
	"\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"

// renderBundleSummary is _render_summary.
func renderBundleSummary(renderer bool, reports []map[string]any, diagnostics map[string]any) string {
	text := "# Datadog Test Optimization Upload Diagnostics\n"
	if renderer && len(reports) > 0 {
		text = renderReportSummary(reports)
	}
	summary := diagnostics["summary"].(map[string]any)
	return strings.TrimRight(text, pythonWhitespace) + "\n\n" + "## Support bundle\n\n" +
		"- Reports included: " + pyStr(summary["report_count"]) + "\n" +
		"- BEP summaries included: " + pyStr(summary["bep_summary_count"]) + "\n" +
		"- Primary reason: " + pyStr(summary["primary_reason_code"]) + "\n"
}

// renderReportSummary is render_report_summary.render_summary_from_reports.
func renderReportSummary(reports []map[string]any) string {
	lines := []string{"# Datadog Test Optimization Upload Diagnostics", ""}
	for _, report := range reports {
		result, ok := getOr(report, "result", map[string]any{}).(map[string]any)
		if !ok {
			unsupported("report result")
		}
		reason := getOr(result, "reason", "")
		counts := renderPayloadCounts(report)
		lines = append(lines, "## "+pyStr(getOr(report, "tool", "unknown")), "",
			"Result: "+pyStr(getOr(result, "status", getOr(report, "status", "unknown"))),
			"Reason: "+pyStr(getOr(result, "reason_code", "unknown")))
		if truthy(reason) {
			lines = append(lines, "Details: "+pyStr(reason))
		}
		lines = append(lines, "Payloads discovered: tests="+itoa(counts[0])+", coverage="+itoa(counts[1])+
			", telemetry="+itoa(counts[2]))
		if bep, ok := getOr(report, "bep", map[string]any{}).(map[string]any); ok && len(bep) > 0 {
			lines = append(lines, "BEP fresh outputs: "+pyStr(getOr(bep, "eligible_outputs", 0)),
				"BEP cached outputs: "+pyStr(getOr(bep, "cached_outputs", 0)),
				"BEP remote-only outputs: "+itoa(remoteOnlyCount(getOr(bep, "remote_only_outputs", 0))))
		}
		if artifacts, ok := getOr(report, "artifacts", map[string]any{}).(map[string]any); ok && len(artifacts) > 0 {
			lines = append(lines, "Artifacts staged: "+pyStr(getOr(artifacts, "staged_count",
				getOr(artifacts, "staged_remote_artifacts", 0))))
		}
		if upload, ok := getOr(report, "upload", map[string]any{}).(map[string]any); ok && len(upload) > 0 {
			attempted := "no"
			if truthy(upload["attempted"]) {
				attempted = "yes"
			}
			lines = append(lines, "Upload attempted: "+attempted)
		}
		if steps := getOr(result, "next_steps", nil); truthy(steps) {
			list, ok := steps.([]any)
			if !ok {
				unsupported("next steps")
			}
			lines = append(lines, "Next steps:")
			for _, step := range list {
				lines = append(lines, "- "+pyStr(step))
			}
		}
		lines = append(lines, "")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), pythonWhitespace) + "\n"
}

// renderPayloadCounts is render_report_summary._payload_counts.
func renderPayloadCounts(report map[string]any) [3]int {
	payloads := getOr(report, "payloads", nil)
	if !truthy(payloads) {
		summary, ok := getOr(report, "summary", map[string]any{}).(map[string]any)
		if !ok {
			unsupported("report summary")
		}
		payloads = summary["payloads"]
	}
	discovered := map[string]any{}
	if object, ok := payloads.(map[string]any); ok {
		discovered = object
		if inner, present := object["discovered"]; present {
			if discovered, ok = inner.(map[string]any); !ok {
				unsupported("discovered payloads")
			}
		}
	} else if truthy(payloads) {
		unsupported("payloads of an unexpected type")
	}
	var counts [3]int
	for i, key := range []string{"tests", "coverage", "telemetry"} {
		count, ok := pyInt(getOr(discovered, key, 0))
		if !ok {
			unsupported("payload count")
		}
		counts[i] = count
	}
	return counts
}

// remoteOnlyCount is render_report_summary._remote_only_count.
func remoteOnlyCount(value any) int {
	if list, ok := value.([]any); ok {
		return len(list)
	}
	count, _ := pyInt(value)
	return count
}

// writeBundleZip writes the files in name order, deflated and dated
// 1980-01-01, as _write_zip_text does.
func writeBundleZip(files map[string]string) []byte {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		// Modified would add an extended timestamp that zipfile does not
		// write; the MS-DOS fields hold 1980-01-01 00:00.
		header.ModifiedDate, header.ModifiedTime = 1<<5|1, 0 //nolint:staticcheck
		file, err := writer.CreateHeader(header)
		if err != nil {
			unsupported("support bundle zip: " + err.Error())
		}
		if _, err := file.Write([]byte(files[name])); err != nil {
			unsupported("support bundle zip: " + err.Error())
		}
	}
	if err := writer.Close(); err != nil {
		unsupported("support bundle zip: " + err.Error())
	}
	return buffer.Bytes()
}

// redaction is redact_text's path replacements: the workspace, output base
// and temporary directory, longest alias first.
type redaction struct {
	replacements [][2]string
}

func newRedaction(workspace, outputBase, tmp string) redaction {
	var replacements [][2]string
	for _, root := range [][2]string{{workspace, "<workspace>"}, {outputBase, "<output_base>"}, {tmp, "<tmp>"}} {
		if root[0] == "" {
			continue
		}
		for _, alias := range pathAliases(root[0]) {
			replacements = append(replacements, [2]string{alias, root[1]})
		}
	}
	sort.SliceStable(replacements, func(a, b int) bool {
		return utf8.RuneCountInString(replacements[a][0]) > utf8.RuneCountInString(replacements[b][0])
	})
	return redaction{replacements}
}

// pathAliases is _path_aliases for an absolute path.
func pathAliases(path string) []string {
	var values []string
	add := func(value string) {
		for _, existing := range values {
			if existing == value {
				return
			}
		}
		values = append(values, value)
	}
	for _, text := range []string{path, path, resolvePath(path)} {
		if text != "" {
			add(text)
		}
		if strings.HasPrefix(text, "/private/") {
			if alias := text[len("/private"):]; alias != "" {
				add(alias)
			}
		} else if strings.HasPrefix(text, "/var/") {
			add("/private" + text)
		}
	}
	return values
}

// json is redact_json.
func (r redaction) json(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			if secretKey.MatchString(key) {
				result[key] = redacted
			} else {
				result[key] = r.json(child)
			}
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for i, child := range typed {
			result[i] = r.json(child)
		}
		return result
	case string:
		return r.text(typed)
	}
	return value
}

// text is redact_text.
func (r redaction) text(value string) string {
	if strings.ContainsAny(value, "\u0130\u0131\u017f\u212a") {
		// Python's re.IGNORECASE matches these to ASCII letters.
		unsupported("text with case-folding letters")
	}
	text := value
	for _, replacement := range r.replacements {
		text = strings.ReplaceAll(text, replacement[0], replacement[1])
	}
	text = redactHTTPURLs(text)
	text = sensitiveQuery.ReplaceAllString(text, "${1}${2}=<redacted>")
	return redactSecretAssignments(text)
}

func isURLChar(r rune) bool {
	return !isPythonSpace(r) && r != '"' && r != '\'' && r != '<' && r != '>'
}

// redactHTTPURLs is HTTP_URL_RE.sub(_redact_http_url, text).
func redactHTTPURLs(text string) string {
	runes := []rune(text)
	var builder strings.Builder
	last := 0
	for i := 0; i < len(runes); {
		end := 0
		if hasFoldPrefix(runes[i:], "https://") {
			end = i + len("https://")
		} else if hasFoldPrefix(runes[i:], "http://") {
			end = i + len("http://")
		}
		if end == 0 || end >= len(runes) || !isURLChar(runes[end]) {
			i++
			continue
		}
		for end < len(runes) && isURLChar(runes[end]) {
			end++
		}
		builder.WriteString(string(runes[last:i]))
		builder.WriteString(redactHTTPURL(string(runes[i:end])))
		last, i = end, end
	}
	builder.WriteString(string(runes[last:]))
	return builder.String()
}

// hasFoldPrefix compares an ASCII prefix without case.
func hasFoldPrefix(runes []rune, prefix string) bool {
	if len(runes) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		r := runes[i]
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		if r != rune(prefix[i]) {
			return false
		}
	}
	return true
}

// redactHTTPURL is _redact_http_url: the URL without credentials, query and
// fragment.
func redactHTTPURL(value string) string {
	scheme, rest, _ := strings.Cut(value, ":")
	scheme = strings.ToLower(scheme)
	rest = rest[2:]
	end := len(rest)
	if index := strings.IndexAny(rest, "/?#"); index >= 0 {
		end = index
	}
	netloc, path := rest[:end], rest[end:]
	if strings.ContainsAny(netloc, "[]") || !plainASCII(netloc) {
		// urlsplit validates bracketed and non-ASCII hosts.
		unsupported("URL host")
	}
	path, _, _ = strings.Cut(path, "#")
	path, _, _ = strings.Cut(path, "?")
	hostInfo := netloc[strings.LastIndex(netloc, "@")+1:]
	host, port, _ := strings.Cut(hostInfo, ":")
	if host == "" {
		return scheme + "://redacted-invalid-url"
	}
	name, zone, hasZone := strings.Cut(host, "%")
	host = strings.ToLower(name)
	if hasZone {
		host += "%" + zone
	}
	if port != "" && strings.Trim(port, "0123456789") == "" {
		if number, err := strconv.Atoi(port); err == nil && number <= 65535 {
			host += ":" + strconv.Itoa(number)
		}
	}
	return scheme + "://" + host + path
}

var envFlags = []string{"--test_env=", "--repo_env=", "--action_env=", "--client_env="}

func isSecretSeparator(r rune) bool { return r == ',' || r == ';' || isPythonSpace(r) }

func isIdentifierStart(r rune) bool { return r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' }

func isIdentifierChar(r rune) bool { return isIdentifierStart(r) || r >= '0' && r <= '9' }

// envFlagAt returns the length of a Bazel environment flag at i, or 0.
func envFlagAt(runes []rune, i int) int {
	for _, flag := range envFlags {
		if i <= len(runes) && hasFoldPrefix(runes[i:], flag) {
			return len(flag)
		}
	}
	return 0
}

// assignmentAt returns the index after NAME= at i.
func assignmentAt(runes []rune, i int) (int, bool) {
	if i >= len(runes) || !isIdentifierStart(runes[i]) {
		return 0, false
	}
	end := i + 1
	for end < len(runes) && isIdentifierChar(runes[end]) {
		end++
	}
	if end < len(runes) && runes[end] == '=' {
		return end + 1, true
	}
	return 0, false
}

func isSecretName(name string) bool {
	upper := strings.ToUpper(name)
	for _, word := range []string{"APIKEY", "API_KEY", "TOKEN", "SECRET", "PASSWORD", "CREDENTIAL", "AUTHORIZATION"} {
		if strings.Contains(upper, word) {
			return true
		}
	}
	return false
}

// valueEndsAt is the lookahead that ends a secret value: another
// assignment after a separator, or the end of the text.
func valueEndsAt(runes []rune, p int) bool {
	if p == len(runes) || p == len(runes)-1 && runes[p] == '\n' {
		return true
	}
	if !isSecretSeparator(runes[p]) {
		return false
	}
	if flag := envFlagAt(runes, p+1); flag > 0 {
		if _, ok := assignmentAt(runes, p+1+flag); ok {
			return true
		}
	}
	_, ok := assignmentAt(runes, p+1)
	return ok
}

// secretAssignmentAt matches SECRET_ASSIGNMENT_RE at i, trying its
// alternatives in the regular expression's order, and returns the end of
// its prefix group and of the whole match.
func secretAssignmentAt(runes []rune, i int) (int, int, bool) {
	var starts []int
	if i == 0 {
		starts = append(starts, i)
	}
	if i < len(runes) && isSecretSeparator(runes[i]) {
		starts = append(starts, i+1)
	}
	for _, start := range starts {
		var flags []int
		if flag := envFlagAt(runes, start); flag > 0 {
			flags = append(flags, flag)
		}
		flags = append(flags, 0)
		for _, flag := range flags {
			nameStart := start + flag
			prefixEnd, ok := assignmentAt(runes, nameStart)
			if !ok || !isSecretName(string(runes[nameStart:prefixEnd-1])) {
				continue
			}
			// The value is lazy and stops at a newline it cannot match.
			for p := prefixEnd; ; p++ {
				if valueEndsAt(runes, p) {
					return prefixEnd, p, true
				}
				if runes[p] == '\n' {
					break
				}
			}
		}
	}
	return 0, 0, false
}

// redactSecretAssignments is SECRET_ASSIGNMENT_RE.sub(prefix + "<redacted>").
func redactSecretAssignments(text string) string {
	runes := []rune(text)
	var builder strings.Builder
	last := 0
	for i := 0; i < len(runes); {
		prefixEnd, end, ok := secretAssignmentAt(runes, i)
		if !ok {
			i++
			continue
		}
		builder.WriteString(string(runes[last:prefixEnd]))
		builder.WriteString(redacted)
		last, i = end, end
	}
	builder.WriteString(string(runes[last:]))
	return builder.String()
}
