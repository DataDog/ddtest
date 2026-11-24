package bazeldoctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

type doctor struct {
	ctx       context.Context
	request   Request
	config    *pyjson.Object
	out       *output
	cache     *outputs
	report    report
	workers   int
	workspace string
	testlogs  string
}

// errInterrupted is Run's error when its context is cancelled.
var errInterrupted = errors.New("interrupted")

// Run validates the outputs as _run_doctor does, from the expected targets
// on, and writes the requested report and support bundle. It stops, removing
// what it staged, when ctx is cancelled.
func Run(ctx context.Context, request Request, workers int) (result Result, err error) {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	level, _ := logLevel(os.Getenv)
	d := &doctor{ctx: ctx, request: request, out: &output{level: level}, cache: newOutputs(),
		report: newReport(request.Args), workers: workers}
	defer func() {
		recovered := recover()
		switch typed := recovered.(type) {
		case nil:
		case failure:
			d.report.recordFailure(typed.message)
			result = d.finish(1)
		case fallback:
			result = Result{Status: "fallback", Reason: typed.reason}
		case interrupted:
			result, err = Result{}, errInterrupted
		default:
			result = Result{Status: "fallback", Reason: fmt.Sprintf("unexpected error: %v", typed)}
		}
	}()
	if runtime.GOOS == "windows" {
		unsupported("Windows paths")
	}
	if request.Args.SupportBundle != "" && request.SupportBundle == nil {
		// A custom collector or renderer writes this bundle.
		unsupported("support bundle collector")
	}
	config, parseErr := pyjson.Parse(request.Config)
	object, isObject := config.(*pyjson.Object)
	if parseErr != nil || !isObject {
		unsupported("configuration")
	}
	d.config = object
	d.run()
	d.report.recordSuccess()
	return d.finish(0), nil
}

// checkInterrupted stops the run when its context is cancelled.
func (d *doctor) checkInterrupted() {
	if d.ctx.Err() != nil {
		panic(interrupted{})
	}
}

// finish writes the report and returns the result of a run that ended with
// exitCode.
func (d *doctor) finish(exitCode int) Result {
	if fallen := d.writeDiagnostics(); fallen != "" {
		return Result{Status: "fallback", Reason: fallen}
	}
	// The support bundle may have warned.
	stdout, stderr := d.out.stdout.String(), d.out.stderr.String()
	if !utf8.ValidString(stdout) || !utf8.ValidString(stderr) {
		// Python prints such a path with its own error handling.
		return Result{Status: "fallback", Reason: "message that is not UTF-8"}
	}
	return Result{Status: "done", ExitCode: exitCode, Stdout: stdout, Stderr: stderr}
}

// writeDiagnostics is _write_requested_diagnostics. It returns a fallback
// reason when the report cannot be written as Python would write it.
func (d *doctor) writeDiagnostics() (reason string) {
	defer func() {
		if recovered, ok := recover().(fallback); ok {
			reason = recovered.reason
		}
	}()
	if path := d.request.Args.ReportJSON; path != "" {
		target := expandUser(path)
		if err := os.MkdirAll(pathParent(target), 0o777); err != nil {
			unsupported("report directory: " + err.Error())
		}
		if err := os.WriteFile(target, []byte(pyformat.DumpsIndented(map[string]any(d.report))+"\n"), 0o666); err != nil {
			unsupported("report: " + err.Error())
		}
	}
	d.writeSupportBundle()
	return ""
}

func (d *doctor) run() {
	args := d.request.Args
	bepFiles := d.request.BepFiles
	stagingRequested := args.ArtifactSource == "bep" || args.ArtifactSource == "auto" && args.RemoteArtifacts != "disabled"
	strictBep := args.FreshnessMode == "required" || args.FreshnessMode == "auto"
	sourceRequired := args.FreshnessMode == "required"

	runtimeSelection := pyjson.Truthy(d.configGet("runtime_selection", false))
	expectedTargets, expectedSource := d.resolveExpectedTargets(runtimeSelection)
	runtimeContexts := d.loadRuntimeContexts()
	if runtimeSelection && len(runtimeContexts) == 0 {
		d.out.fail("runtime selection requires at least one --context-entry argument")
	}

	workspace := os.Getenv("BUILD_WORKSPACE_DIRECTORY")
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			unsupported("working directory")
		}
		workspace = cwd
	}
	d.workspace = resolvePath(workspace)
	d.testlogs = d.resolveTestlogs(stagingRequested || len(expectedTargets) > 0 && len(bepFiles) > 0)

	if pyjson.Truthy(d.configValue("forbid_dd_git_test_env")) {
		d.validateBazelrc()
	}
	if pyjson.Truthy(d.configValue("require_git_metadata")) {
		if len(runtimeContexts) > 0 {
			d.validateGitMetadata(runtimeContexts)
		} else {
			d.validateGitMetadata(d.manifestContexts())
		}
	}
	configSection := d.report.section("config")
	configSection["config_path"] = d.request.ConfigPath
	configSection["workspace"] = d.workspace
	configSection["testlogs_dir"] = d.testlogs
	configSection["expected_targets"] = reportList(expectedTargets)
	configSection["expected_targets_source"] = expectedSource
	d.report.section("summary")["expected_targets"] = len(expectedTargets)
	d.report.section("bep")["files"] = reportList(bepFiles)
	d.report.section("artifacts")["staging_requested"] = stagingRequested
	d.report.section("targets")["expected"] = reportList(expectedTargets)

	var staged []stagedArtifact
	stagingBase := ""
	defer func() {
		if len(staged) > 0 && stagingBase != "" {
			d.cleanupStagedRuns(staged, stagingBase)
		}
	}()

	var outputDirs []string
	targetByDir := map[string]string{}
	if len(expectedTargets) > 0 {
		allowMissing := stagingRequested || len(bepFiles) > 0
		if d.testlogs == "" {
			if !allowMissing {
				d.out.fail("cannot resolve expected target " + expectedTargets[0] + ": bazel-testlogs directory not found")
			}
		} else {
			for i, dirs := range d.expectedTargetOutputs(expectedTargets, allowMissing) {
				outputDirs = append(outputDirs, dirs...)
				for _, dir := range dirs {
					targetByDir[d.resolved(dir)] = expectedTargets[i]
				}
			}
		}
	} else {
		if d.testlogs != "" {
			outputDirs = d.candidateOutputDirs()
		}
		if len(outputDirs) == 0 && !stagingRequested {
			d.out.fail("no Test Optimization output directories found under " + d.testlogs)
		}
	}
	for _, dir := range outputDirs {
		d.localOutputKey(dir)
	}
	stagingEnabled := stagingRequested
	if !(len(expectedTargets) > 0 && len(bepFiles) > 0 && args.FreshnessMode != "disabled" && strictBep && !stagingEnabled) {
		d.inspect(outputDirs)
	}
	var f *freshness
	if len(bepFiles) > 0 {
		if args.FreshnessMode != "disabled" || stagingEnabled {
			f = d.parseFreshness(bepFiles, stagingEnabled || args.FreshnessMode != "optional", stagingEnabled)
			d.updateBep(f, nil, nil)
		}
	} else if sourceRequired {
		d.out.fail("BEP freshness validation is required but no BEP JSON file was configured. " +
			"Run bazel test with --build_event_json_file=.topt/bazel-bep.json and rerun " +
			"the doctor with --bep-json=.topt/bazel-bep.json --freshness-source=bep " +
			"--freshness-mode=required.")
	} else if args.FreshnessSource == "bep" {
		d.out.warn("BEP freshness source was selected but no BEP JSON file was configured; skipping BEP freshness validation")
	}

	d.updateArtifacts(stagingEnabled, stagingBase, staged)
	if stagingEnabled && f != nil {
		var selectedLabels map[string]bool
		if len(expectedTargets) > 0 {
			selectedLabels = map[string]bool{}
			for _, label := range expectedTargets {
				selectedLabels[label] = true
			}
		}
		selectedOutputs := selectedArtifactOutputs(f, args.RemoteArtifacts)
		blocked := blockedArtifactLabels(f, args.RemoteArtifacts)
		if selectedLabels != nil {
			for p := range selectedOutputs {
				if !selectedLabels[p.label] {
					delete(selectedOutputs, p)
				}
			}
			for label := range blocked {
				if !selectedLabels[label] {
					delete(blocked, label)
				}
			}
		}
		d.updateBep(f, selectedOutputs, blocked)
		stagingBase = d.stagingDir()
		staged = d.stageArtifacts(f, stagingBase, selectedLabels)
		applyStaged(f, staged)
		d.updateBep(f, selectedOutputs, blocked)
		d.updateArtifacts(stagingEnabled, stagingBase, staged)
		outputDirs, targetByDir = d.mergeStaged(outputDirs, staged, selectedOutputs, blocked, targetByDir)
	}

	keyByDir, labelByDir := map[string]string{}, map[string]string{}
	if args.FreshnessMode != "disabled" && f != nil {
		for _, item := range staged {
			keyByDir[d.resolved(item.outputDir)] = item.outputKey
			labelByDir[d.resolved(item.outputDir)] = item.label
		}
		requireMetadata := pyjson.Truthy(d.configValue("require_bazel_metadata"))
		if len(expectedTargets) > 0 {
			expected := map[string]bool{}
			for _, label := range expectedTargets {
				expected[label] = true
			}
			outputDirs = d.validateExpectedFreshness(outputDirs, expected, f, strictBep, keyByDir, labelByDir, requireMetadata)
		} else {
			outputDirs = d.validateDiscoveredFreshness(outputDirs, f, strictBep, keyByDir, labelByDir, requireMetadata)
		}
		for dir, label := range labelByDir {
			targetByDir[dir] = label
		}
	}
	expectedSet := map[string]bool{}
	for _, label := range expectedTargets {
		expectedSet[label] = true
	}
	if f != nil {
		for label := range f.ambiguousLabels {
			if expectedSet[label] {
				d.out.fail("BEP freshness is ambiguous: target reported as both platform-skipped and executed")
			}
		}
	}
	if f != nil && strictBep {
		if message := f.firstCompletenessError(union(f.identifiedLabels, expectedSet)); message != "" {
			d.out.fail(message)
		}
	}
	if len(outputDirs) == 0 {
		if len(expectedTargets) > 0 && f != nil {
			cachedOnly := exclusivelyCachedLabels(f.executions)
			if len(f.executions) == 0 {
				cachedOnly = map[string]bool{}
				fresh := labelsOf(f.eligible)
				for label := range labelsOf(f.cached) {
					if !fresh[label] {
						cachedOnly[label] = true
					}
				}
			}
			covered := union(cachedOnly, f.skipped)
			all := true
			for _, label := range expectedTargets {
				all = all && covered[label]
			}
			if all {
				d.report.updateOutputSummary([]any{})
				d.report.section("summary")["payload_selection"] = map[string]any{}
				d.out.summary("OK: all expected targets were cached " +
					"or skipped as platform-incompatible; no fresh payloads require validation")
				return
			}
		}
		if len(expectedTargets) > 0 {
			d.out.fail("no Test Optimization output directories found for expected targets after BEP artifact staging")
		}
		missingRoot := d.testlogs
		if missingRoot == "" {
			missingRoot = joinPath(d.workspace, "bazel-testlogs")
		}
		d.out.fail("no Test Optimization output directories found under " + missingRoot)
	}

	d.inspect(outputDirs)
	d.report.updateOutputSummary(d.diagnosticEntries(outputDirs, targetByDir, staged))
	allowed := d.configGet("allowed_payload_selections", nil)
	var allowedList []string
	if pyjson.Truthy(allowed) {
		list, isList := allowed.([]any)
		if !isList {
			unsupported("allowed_payload_selections is not a list")
		}
		for _, item := range list {
			text, isText := item.(string)
			if !isText {
				unsupported("allowed_payload_selections has a non-string")
			}
			allowedList = append(allowedList, text)
		}
	}
	expectedSelections, isObject := d.configGet("expected_payload_selection_by_target", &pyjson.Object{}).(*pyjson.Object)
	if !isObject {
		unsupported("expected_payload_selection_by_target is not an object")
	}
	testResults, emptySessions := map[string]string{}, map[string]string{}
	summary := d.validateOutputs(outputDirs, validation{
		requireJSON:     pyjson.Truthy(d.configValue("require_json_payloads")),
		requireMetadata: pyjson.Truthy(d.configValue("require_bazel_metadata")),
		forbidNoMatch:   pyjson.Truthy(d.configValue("forbid_full_bundle_no_match")),
		forbidMsgpack:   pyjson.Truthy(d.configGet("forbid_msgpack_payloads", true)),
		allowed:         allowedList, expected: expectedSelections, targets: targetByDir,
		testResults: testResults, emptySessions: emptySessions,
	})
	if f != nil {
		selected := union(f.identifiedLabels, expectedSet)
		observed := map[string]bool{}
		for _, raw := range d.report["outputs"].([]any) {
			entry := raw.(map[string]any)
			if label := entry["label"].(string); label != "" {
				selected[label] = true
			}
			observed[entry["output_key"].(string)] = true
		}
		var ambiguous []string
		for key := range f.ambiguousOutputs {
			if observed[key] {
				ambiguous = append(ambiguous, key)
			}
		}
		for _, e := range f.executions {
			if selected[e.label] {
				for _, key := range e.outputKeys {
					if f.ambiguousOutputs[key] {
						ambiguous = append(ambiguous, key)
					}
				}
			}
		}
		for label := range f.ambiguousLabels {
			if selected[label] {
				d.out.fail("BEP freshness is ambiguous: target reported as both platform-skipped and executed")
			}
		}
		if len(ambiguous) > 0 {
			sort.Strings(ambiguous)
			d.out.fail("BEP freshness has ambiguous execution provenance for " + ambiguous[0])
		}
		if strictBep {
			keyOf := func(dir string) string {
				if key := keyByDir[dir]; key != "" {
					return key
				}
				return d.localOutputKey(dir)
			}
			proven, empty := map[pair]bool{}, map[pair]bool{}
			for dir, label := range testResults {
				proven[pair{label, keyOf(dir)}] = true
				f.identifiedLabels[label] = true
			}
			for dir, label := range emptySessions {
				f.identifiedLabels[label] = true
				empty[pair{label, keyOf(dir)}] = true
			}
			if message := f.firstCompletenessError(union(f.identifiedLabels, expectedSet)); message != "" {
				d.out.fail(message)
			}
			if shard, missing := missingTestShards(f.executions, proven, union(f.identifiedLabels, expectedSet), empty); missing {
				d.out.fail("fresh shard produced no test-result evidence: " + shard.label + " configuration=" +
					pyformat.String(shard.configuration) + " run=" + strconv.FormatInt(shard.run, 10) + " shard=" +
					strconv.FormatInt(shard.shard, 10) + " invocation=" + shard.invocation)
			}
		}
	}
	selection := map[string]any{}
	for name, count := range summary {
		selection[name] = count
	}
	d.report.section("summary")["payload_selection"] = selection
	d.out.summary("payload selection summary: " + formatSelectionSummary(summary))
	d.out.summary("OK: validated " + itoa(len(outputDirs)) + " test output directorie(s)")
}

// resolveTestlogs is _resolve_testlogs_dir.
func (d *doctor) resolveTestlogs(allowMissing bool) string {
	if override := os.Getenv("TESTLOGS_DIR"); override != "" {
		path := expandUser(override)
		if !exists(path) {
			d.out.fail("TESTLOGS_DIR is set but path does not exist: " + purePath(path))
		}
		if !isDir(path) {
			d.out.fail("TESTLOGS_DIR is set but is not a directory: " + purePath(path))
		}
		return resolvePath(path)
	}
	cwd, err := os.Getwd()
	if err != nil {
		unsupported("working directory")
	}
	for _, candidate := range []string{joinPath(d.workspace, "bazel-testlogs"), joinPath(cwd, "bazel-testlogs")} {
		if exists(candidate) && isDir(candidate) {
			return resolvePath(candidate)
		}
	}
	if allowMissing {
		return ""
	}
	d.out.fail("could not find bazel-testlogs; set TESTLOGS_DIR or run from the Bazel workspace root")
	return ""
}

func union(sets ...map[string]bool) map[string]bool {
	result := map[string]bool{}
	for _, set := range sets {
		for key := range set {
			result[key] = true
		}
	}
	return result
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// updateBep is _update_diagnostic_bep.
func (d *doctor) updateBep(f *freshness, selected map[pair]bool, blocked map[string]bool) {
	if f == nil {
		return
	}
	seen := union(labelsOf(f.eligible), labelsOf(f.cached), f.referenceLabels, f.skipped)
	remoteLabels := map[string]bool{}
	remoteOutputs := []any{}
	for _, item := range f.remoteOnly {
		seen[item.label] = true
		remoteLabels[item.label] = true
		remoteOutputs = append(remoteOutputs, map[string]any{"label": item.label, "output_key": item.outputKey,
			"artifact": displayArtifactReference(item.artifact), "reason": item.reason})
	}
	pairList := func(set map[pair]bool) []any {
		list := []any{}
		for _, p := range sortedPairs(set) {
			list = append(list, []any{p.label, p.key})
		}
		return list
	}
	errorsList := []any{}
	for _, e := range f.completenessError {
		errorsList = append(errorsList, map[string]any{"label": e.label, "message": e.key})
	}
	bep := d.report.section("bep")
	bep["seen_targets"] = reportList(sortedKeys(seen))
	bep["eligible_outputs"] = len(f.eligible)
	bep["eligible_output_keys"] = pairList(f.eligible)
	bep["cached_outputs"] = len(f.cached)
	bep["cached_output_keys"] = pairList(f.cached)
	bep["remote_only_outputs"] = remoteOutputs
	bep["completeness_errors"] = errorsList
	mode := d.report.section("config")["freshness_mode"]
	bep["strict_completeness"] = mode == "required" || mode == "auto"
	bep["missing_output_mappings"] = reportList(sortedKeys(f.missingMappings))
	bep["skipped_targets"] = reportList(sortedKeys(f.skipped))
	bep["artifact_references"] = f.referenceCount
	bep["selected_artifact_outputs"] = pairList(selected)
	bep["blocked_artifact_labels"] = reportList(sortedKeys(blocked))
	expected := map[string]bool{}
	for _, label := range d.report.section("targets")["expected"].([]any) {
		expected[label.(string)] = true
	}
	var missing []string
	for label := range expected {
		if !seen[label] {
			missing = append(missing, label)
		}
	}
	sort.Strings(missing)
	targets := d.report.section("targets")
	targets["seen_in_bep"] = reportList(sortedKeys(seen))
	targets["fresh"] = reportList(sortedKeys(labelsOf(f.eligible)))
	targets["cached"] = reportList(sortedKeys(labelsOf(f.cached)))
	targets["missing"] = reportList(missing)
	targets["remote_only"] = reportList(sortedKeys(remoteLabels))
	targets["skipped"] = reportList(sortedKeys(f.skipped))
}

// updateArtifacts is _update_diagnostic_artifacts.
func (d *doctor) updateArtifacts(enabled bool, base string, staged []stagedArtifact) {
	items := []any{}
	for _, item := range staged {
		items = append(items, map[string]any{"label": item.label, "output_key": item.outputKey, "path": item.outputDir,
			"downloaded": item.downloaded, "remote_only": item.remoteOnly,
			"fetch_value": displayArtifactReference(item.fetchValue), "carrier": stagedCarrier(item)})
	}
	artifacts := d.report.section("artifacts")
	artifacts["staging_enabled"] = enabled
	artifacts["staging_dir"] = base
	artifacts["staged_count"] = len(staged)
	artifacts["staged"] = items
}

// stagedCarrier is _diagnostic_staged_carrier.
func stagedCarrier(item stagedArtifact) string {
	if item.downloaded {
		return "downloaded_outputs_zip"
	}
	stripped, err := stripFileURI(item.fetchValue)
	if err != nil {
		unsupported("fetch value")
	}
	normalized := strings.TrimRight(strings.ToLower(strings.ReplaceAll(stripped, `\`, "/")), "/")
	switch {
	case normalized == "outputs.zip" || strings.HasSuffix(normalized, "/outputs.zip"):
		return "outputs_zip"
	case normalized == "test.outputs" || strings.HasSuffix(normalized, "/test.outputs"):
		return "test_outputs"
	}
	return "unknown"
}
