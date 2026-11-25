package bazeldoctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyformat"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// This file is the uploader's discovery.py, expected_targets.py and the
// selection half of freshness.py.

// postError is one of the uploader's controlled errors: a message that it
// logs, and the exit status that it reports.
type postError struct {
	message  string
	exitCode int
}

func (e *postError) Error() string { return e.message }

func failRun(exitCode int, format string, args ...any) error {
	return &postError{fmt.Sprintf(format, args...), exitCode}
}

// payload kinds, in the uploader's PayloadType order.
var payloadKinds = []string{"test", "coverage", "telemetry"}

// postTask is a FileTask: one payload file.
type postTask struct {
	id, sourcePath, displayPath, kind, outputDir, outputKey, label string
}

// postOutput is a DiscoveredOutput.
type postOutput struct {
	path, outputKey, scanRoot string
	staged                    bool
}

// postDiscovery is a DiscoveryResult.
type postDiscovery struct {
	outputs  []postOutput
	tasks    []postTask
	warnings []string
}

func (disc postDiscovery) counts() map[string]int {
	counts := map[string]int{}
	for _, kind := range payloadKinds {
		counts[kind] = 0
	}
	for _, task := range disc.tasks {
		if _, ok := counts[task.kind]; ok {
			counts[task.kind]++
		}
	}
	return counts
}

type scanRoot struct {
	path   string
	staged bool
}

// normalizeScanRoots is _normalize_scan_roots.
func normalizeScanRoots(roots []scanRoot) ([]scanRoot, error) {
	var normalized []scanRoot
	seen := map[string]bool{}
	for _, root := range roots {
		if !isDir(root.path) {
			return nil, failRun(2, "testlogs scan root is not a directory: %s", root.path)
		}
		resolved := resolvePath(root.path)
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		normalized = append(normalized, scanRoot{resolved, root.staged})
	}
	return normalized, nil
}

// discoverTasks is discover_file_tasks_with_ddtest, in process.
func (p *poster) discoverTasks(roots []scanRoot, stagedKeys []string) (postDiscovery, error) {
	if p.cfg.MaxDepth < 0 {
		return postDiscovery{}, failRun(2, "max_depth must be non-negative")
	}
	normalized, err := normalizeScanRoots(roots)
	if err != nil {
		return postDiscovery{}, err
	}
	request := bazelinspect.DiscoverRequest{Version: 1, MaxDepth: p.cfg.MaxDepth, ScanRoots: []bazelinspect.ScanRoot{}}
	for _, root := range normalized {
		request.ScanRoots = append(request.ScanRoots, bazelinspect.ScanRoot{Path: root.path, Staged: root.staged})
	}
	for _, key := range stagedKeys {
		if key != "" {
			request.StagedOutputKeys = append(request.StagedOutputKeys, key)
		}
	}
	report := bazelinspect.Discover(request, p.workers)
	disc := postDiscovery{warnings: report.Warnings}
	for _, output := range report.Outputs {
		disc.outputs = append(disc.outputs, postOutput{output.Path, output.OutputKey, output.ScanRoot, output.Staged})
	}
	for index, source := range report.Sources {
		output := disc.outputs[source.Output]
		disc.tasks = append(disc.tasks, postTask{
			id:          fmt.Sprintf("file-%06d", index+1),
			sourcePath:  output.path + "/payloads/" + source.Directory + "/" + source.Name,
			displayPath: output.outputKey + "/payloads/" + source.Directory + "/" + source.Name,
			kind:        source.PayloadType, outputDir: output.path, outputKey: output.outputKey,
		})
	}
	return disc, nil
}

// latestMtime is payload_latest_mtime.
func latestMtime(disc postDiscovery) time.Time {
	var latest time.Time
	for _, task := range disc.tasks {
		if info, err := os.Stat(task.sourcePath); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}

// testsExecuted is tests_executed: a test.log or test.xml below a root,
// without following directory links.
func testsExecuted(roots []scanRoot) bool {
	normalized, err := normalizeScanRoots(roots)
	if err != nil {
		return false
	}
	found := errors.New("found")
	for _, root := range normalized {
		walkErr := filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if !entry.IsDir() && (entry.Name() == "test.log" || entry.Name() == "test.xml") {
				return found
			}
			return nil
		})
		if walkErr == found {
			return true
		}
	}
	return false
}

// waitForQuiescence is wait_for_quiescence, which polls every 2 seconds.
func (p *poster) waitForQuiescence(scan func() (postDiscovery, error)) (postDiscovery, string, time.Duration, error) {
	quiescent := time.Duration(p.cfg.QuiescentSec) * time.Second
	maxWait := time.Duration(p.cfg.MaxWaitSec) * time.Second
	if quiescent < 0 || maxWait < 0 {
		return postDiscovery{}, "", 0, failRun(2, "quiescence and maximum wait must be non-negative")
	}
	const poll = 2 * time.Second
	started := time.Now()
	for {
		current, err := scan()
		if err != nil {
			return postDiscovery{}, "", 0, err
		}
		now := time.Now()
		elapsed := now.Sub(started)
		if len(current.tasks) == 0 {
			if maxWait == 0 {
				return current, "no_payload_immediate", elapsed, nil
			}
			if elapsed >= maxWait {
				return current, "max_wait", elapsed, nil
			}
		} else {
			idle := max(0, now.Sub(latestMtime(current)))
			if quiescent == 0 || idle >= quiescent {
				return current, "quiescent", elapsed, nil
			}
			if maxWait == 0 || elapsed >= maxWait {
				return current, "max_wait", elapsed, nil
			}
		}
		sleep := poll
		if maxWait > 0 {
			sleep = min(poll, maxWait-elapsed)
		}
		select {
		case <-p.ctx.Done():
			return postDiscovery{}, "", 0, errPostInterrupted
		case <-time.After(sleep):
		}
	}
}

var (
	attemptDirectory = regexp.MustCompile(`^(?:(?:shard|run)_[0-9]+_of_[0-9]+|attempt_[0-9]+)$`)
	retryDirectory   = regexp.MustCompile(`^attempt_[0-9]+[.]outputs$`)
)

// isRetryOutputDirectory is is_retry_output_directory.
func isRetryOutputDirectory(parent, name string) bool {
	return parent == "test_attempts" && retryDirectory.MatchString(name)
}

func targetOutputParts(label string) string {
	pkg, target, _ := strings.Cut(label[2:], ":")
	var parts []string
	for _, part := range strings.Split(pkg, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(append(parts, strings.Split(target, "/")...), "/")
}

// targetIndex is _TargetOutputIndex.
type targetIndex map[string][]string

func newTargetIndex(targets []string) targetIndex {
	index := targetIndex{}
	for _, label := range targets {
		key := targetOutputParts(label)
		index[key] = append(index[key], label)
	}
	return index
}

func (index targetIndex) matches(outputKey string, allowRetryPairs bool) []string {
	parts := pathParts(strings.ReplaceAll(outputKey, `\`, "/"))
	if len(parts) == 0 || parts[len(parts)-1] != "test.outputs" {
		return nil
	}
	length := len(parts) - 1
	for length > 0 {
		if labels := index[strings.Join(parts[:length], "/")]; len(labels) > 0 {
			return labels
		}
		switch {
		case attemptDirectory.MatchString(parts[length-1]):
			length--
		case allowRetryPairs && length >= 2 && isRetryOutputDirectory(parts[length-2], parts[length-1]):
			length -= 2
		default:
			return nil
		}
	}
	return nil
}

// selectExpectedOutputs is select_expected_outputs.
func selectExpectedOutputs(disc postDiscovery, targets []string, allowMissing bool, bepOutputs map[pair]bool,
	targetLabel func(string) string) (postDiscovery, error) {
	if len(targets) == 0 {
		return disc, nil
	}
	found := map[string]bool{}
	targetByKey := map[string]string{}
	index := newTargetIndex(targets)
	bepLabels := map[string]map[string]bool{}
	for p := range bepOutputs {
		if bepLabels[p.key] == nil {
			bepLabels[p.key] = map[string]bool{}
		}
		bepLabels[p.key][p.label] = true
	}
	for _, output := range disc.outputs {
		physicalRetry := isRetryOutputDirectory(pathName(pathParent(output.path)), pathName(output.path))
		labels := bepLabels[output.outputKey]
		matches := index.matches(output.outputKey, physicalRetry || len(labels) > 0)
		if len(matches) > 1 {
			return postDiscovery{}, failRun(2, "test.outputs path matches multiple expected targets: %s", output.outputKey)
		}
		if len(matches) == 0 {
			continue
		}
		target := matches[0]
		if len(labels) == 1 && !labels[target] {
			return postDiscovery{}, failRun(2, "BEP output target conflicts with expected target: %s", output.outputKey)
		}
		if physicalRetry || len(labels) > 0 && strings.Contains(output.outputKey, "/test_attempts/") {
			if declared := targetLabel(output.path); declared != "" && declared != target {
				return postDiscovery{}, failRun(2, "bazel.target metadata conflicts with expected target: %s", output.outputKey)
			}
		}
		found[target] = true
		targetByKey[output.outputKey] = target
	}
	var missing []string
	for _, target := range targets {
		if !found[target] {
			missing = append(missing, target)
		}
	}
	if len(missing) > 0 && !allowMissing {
		return postDiscovery{}, failRun(2, "expected targets have no local test.outputs: %s", strings.Join(missing, ", "))
	}
	selected := postDiscovery{warnings: disc.warnings}
	for _, output := range disc.outputs {
		if _, ok := targetByKey[output.outputKey]; ok {
			selected.outputs = append(selected.outputs, output)
		}
	}
	for _, task := range disc.tasks {
		if target, ok := targetByKey[task.outputKey]; ok {
			task.label = target
			selected.tasks = append(selected.tasks, task)
		}
	}
	return selected, nil
}

// freshnessPlan is FreshnessPlan.
type freshnessPlan struct {
	selectedSource          string
	eligibilityEnabled      bool
	eligible, cached        map[pair]bool
	remoteOnly              []remoteOutput
	missingOutputLabels     map[string]bool
	skippedTargets          map[string]bool
	blockedLabels           map[string]bool
	selectedArtifactOutputs map[pair]bool
	stagedOutputs           map[pair]bool
	warnings                []string
	executions              []execution
	requireTestResults      bool
	requireRemoteArtifacts  bool
	completenessErrors      []pair
	ambiguousOutputs        map[string]bool
	ambiguousLabels         map[string]bool
}

func emptyPlan() freshnessPlan {
	return freshnessPlan{selectedSource: "none", eligible: map[pair]bool{}, cached: map[pair]bool{},
		missingOutputLabels: map[string]bool{}, skippedTargets: map[string]bool{}, blockedLabels: map[string]bool{},
		selectedArtifactOutputs: map[pair]bool{}, stagedOutputs: map[pair]bool{}, ambiguousOutputs: map[string]bool{},
		ambiguousLabels: map[string]bool{}}
}

func (plan freshnessPlan) firstCompletenessError(labels map[string]bool) string {
	for _, e := range plan.completenessErrors {
		if e.label == "" || labels[e.label] {
			return e.key
		}
	}
	return ""
}

// sourceRequired and strictBep are resolve_freshness_policy.
func sourceRequired(mode string, ci bool) bool { return mode == "required" || mode == "auto" && ci }

func strictBepPolicy(mode, source string) bool {
	return source == "bep" && (mode == "required" || mode == "auto")
}

func uniqueCodes(codes []string) []string {
	var unique []string
	seen := map[string]bool{}
	for _, code := range codes {
		if code != "" && !seen[code] {
			seen[code] = true
			unique = append(unique, code)
		}
	}
	return unique
}

// selectSource is _select_source.
func (p *poster) selectSource(bepConfigured bool, warnings *[]string) (string, error) {
	cfg := p.cfg
	if cfg.FreshnessMode == "disabled" {
		if bepConfigured {
			*warnings = append(*warnings, "freshness_disabled_bep_ignored")
		}
		if cfg.ExecutionLogJSON != "" {
			*warnings = append(*warnings, "freshness_disabled_execution_log_ignored")
		}
		return "none", nil
	}
	required := sourceRequired(cfg.FreshnessMode, cfg.CI)
	switch cfg.FreshnessSource {
	case "bep":
		if bepConfigured {
			return "bep", nil
		}
		if required {
			return "", failRun(2, "BEP freshness filtering is required but no BEP JSON file was configured")
		}
		*warnings = append(*warnings, "bep_freshness_not_configured")
		return "none", nil
	case "execution_log":
		if cfg.ExecutionLogJSON != "" {
			return "execution_log", nil
		}
		if required {
			return "", failRun(2, "execution-log freshness filtering is required but no execution log was configured")
		}
		*warnings = append(*warnings, "execution_log_freshness_not_configured")
		return "none", nil
	}
	if bepConfigured {
		return "bep", nil
	}
	if cfg.ExecutionLogJSON != "" {
		return "execution_log", nil
	}
	if required {
		return "", failRun(2, "freshness filtering is required in CI or required mode, but no BEP or execution log was configured")
	}
	*warnings = append(*warnings, "freshness_not_configured")
	return "none", nil
}

// resolveRuntimeFile is _resolve_runtime_file.
func (p *poster) resolveRuntimeFile(path string) (string, error) {
	expanded := expandUser(purePath(path))
	candidates := []string{expanded, expanded}
	if !filepath.IsAbs(expanded) {
		cwd, _ := os.Getwd()
		candidates = []string{joinPath(p.cfg.Workspace, expanded), joinPath(cwd, expanded)}
	}
	for _, candidate := range candidates {
		if isFile(candidate) {
			return resolvePath(candidate), nil
		}
	}
	return "", failRun(2, "configured freshness file not found: %s", purePath(path))
}

// buildPlan is _build_plan.
func (p *poster) buildPlan(source string, f *freshness, selectedArtifacts map[pair]bool, blocked map[string]bool,
	expected map[string]bool, warnings []string, staged map[pair]bool) (freshnessPlan, error) {
	plan := emptyPlan()
	cfg := p.cfg
	if source == "execution_log" {
		path, err := p.resolveRuntimeFile(cfg.ExecutionLogJSON)
		if err != nil {
			return plan, err
		}
		eligible, err := parseExecutionLog(path)
		if err != nil {
			return plan, err
		}
		for pr := range eligible {
			if len(expected) == 0 || expected[pr.label] {
				plan.eligible[pr] = true
			}
		}
		plan.selectedSource, plan.eligibilityEnabled, plan.warnings = "execution_log", true, uniqueCodes(warnings)
		return plan, nil
	}
	if f == nil {
		plan.selectedArtifactOutputs, plan.blockedLabels, plan.warnings = selectedArtifacts, blocked, uniqueCodes(warnings)
		return plan, nil
	}
	keep := func(label string) bool { return len(expected) == 0 || expected[label] }
	for pr := range f.eligible {
		if keep(pr.label) {
			plan.eligible[pr] = true
		}
	}
	for pr := range f.cached {
		if keep(pr.label) {
			plan.cached[pr] = true
		}
	}
	for _, remote := range f.remoteOnly {
		if !f.ambiguousOutputs[remote.outputKey] && !f.ambiguousLabels[remote.label] && keep(remote.label) {
			plan.remoteOnly = append(plan.remoteOnly, remote)
		}
	}
	for label := range f.missingMappings {
		if keep(label) {
			plan.missingOutputLabels[label] = true
		}
	}
	for label := range f.skipped {
		if keep(label) {
			plan.skippedTargets[label] = true
		}
	}
	if len(plan.remoteOnly) > 0 && !strictBepPolicy(cfg.FreshnessMode, source) && cfg.RemoteArtifacts != "required" {
		warnings = append(warnings, "bep_remote_only_outputs_skipped")
	}
	plan.selectedSource = source
	plan.executions = f.executions
	plan.requireTestResults = strictBepPolicy(cfg.FreshnessMode, source)
	plan.requireRemoteArtifacts = cfg.RemoteArtifacts == "required"
	plan.completenessErrors = f.completenessError
	plan.ambiguousOutputs, plan.ambiguousLabels = f.ambiguousOutputs, f.ambiguousLabels
	plan.eligibilityEnabled = source == "bep"
	plan.blockedLabels, plan.selectedArtifactOutputs = blocked, selectedArtifacts
	for pr := range plan.eligible {
		if staged[pr] {
			plan.stagedOutputs[pr] = true
		}
	}
	plan.warnings = uniqueCodes(warnings)
	return plan, nil
}

// parseExecutionLog is _parse_execution_log.
func parseExecutionLog(path string) (map[pair]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, failRun(2, "failed to read execution log JSON %s: %v", path, err)
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	eligible := map[pair]bool{}
	for number, line := range strings.Split(text, "\n") {
		if pythonStrip(line) == "" {
			continue
		}
		value, err := pyjson.Parse([]byte(line))
		if err != nil {
			return nil, failRun(2, "invalid execution log JSON in %s:%d", path, number+1)
		}
		record, ok := value.(*pyjson.Object)
		if !ok || record.Lookup("mnemonic", "") != "TestRunner" {
			continue
		}
		runner, _ := record.Lookup("runner", "").(string)
		if record.Lookup("cacheHit", false) == true || strings.Contains(strings.ToLower(runner), "cache hit") {
			continue
		}
		label, _ := record.Lookup("targetLabel", nil).(string)
		if label == "" {
			continue
		}
		var outputs []any
		if listed, ok := record.Lookup("listedOutputs", []any{}).([]any); ok {
			outputs = append(outputs, listed...)
		}
		if actual, ok := record.Lookup("actualOutputs", []any{}).([]any); ok {
			for _, item := range actual {
				if object, ok := item.(*pyjson.Object); ok {
					outputs = append(outputs, object.Lookup("path", ""))
				}
			}
		}
		for _, raw := range outputs {
			if text, ok := raw.(string); ok {
				if key := executionOutputKey(text); key != "" {
					eligible[pair{label, key}] = true
				}
			}
		}
	}
	return eligible, nil
}

// executionOutputKey is _execution_output_key.
func executionOutputKey(raw string) string {
	normalized := strings.ReplaceAll(raw, `\`, "/")
	if index := strings.LastIndex(normalized, "/testlogs/"); index >= 0 {
		normalized = normalized[index+len("/testlogs/"):]
	}
	if before, _, found := strings.Cut(normalized, "/test.outputs/"); found {
		normalized = before + "/test.outputs"
	} else if !strings.HasSuffix(normalized, "/test.outputs") {
		return ""
	}
	return strings.TrimLeft(strings.TrimPrefix(normalized, "./"), "/")
}

// noExpectedPayloads is _no_expected_payloads.
func noExpectedPayloads(expected []string, plan freshnessPlan) bool {
	if len(expected) == 0 || len(plan.eligible) > 0 || len(plan.remoteOnly) > 0 || len(plan.missingOutputLabels) > 0 {
		return false
	}
	expectedSet := map[string]bool{}
	for _, label := range expected {
		expectedSet[label] = true
	}
	cachedLabels := exclusivelyCachedLabels(plan.executions)
	if len(plan.executions) == 0 {
		cachedLabels = labelsOf(plan.cached)
	}
	for label := range expectedSet {
		if !(cachedLabels[label] || plan.skippedTargets[label]) || plan.ambiguousLabels[label] {
			return false
		}
	}
	for label := range plan.skippedTargets {
		if !expectedSet[label] {
			return false
		}
	}
	for _, e := range plan.executions {
		if !e.cached && expectedSet[e.label] {
			return false
		}
	}
	return true
}

// filterResult is FreshnessFilterResult.
type filterResult struct {
	discovery  postDiscovery
	skipped    []string
	identified []string
	ambiguous  []string
	// checked are the outputs that the doctor's checks validate: the
	// selected ones and those that the selection would keep but that have
	// nothing to upload.
	checked []postOutput
}

// recognizedTestSource is _recognized_test_source.
func recognizedTestSource(task postTask) bool {
	payload := bazelinspect.DecodePayload(task.sourcePath)
	return payload.Decoded && (payload.TestEvents > 0 || payload.EmptySession)
}

// filterForFreshness is filter_discovery_for_freshness.
func filterForFreshness(disc postDiscovery, plan freshnessPlan, mode string, expected []string,
	targetLabel func(string) string, sidecarState func(string) (bool, bool)) (filterResult, error) {
	tasksByOutput := map[string][]postTask{}
	for _, task := range disc.tasks {
		tasksByOutput[task.outputKey] = append(tasksByOutput[task.outputKey], task)
	}
	labelsByKey := map[string]map[string]bool{}
	for pr := range plan.eligible {
		if labelsByKey[pr.key] == nil {
			labelsByKey[pr.key] = map[string]bool{}
		}
		labelsByKey[pr.key][pr.label] = true
	}
	coreTestKeys := map[string]bool{}
	knownCore := map[string]bool{}
	for _, label := range expected {
		knownCore[label] = true
	}
	metadataExists, metadataSymlink := map[string]bool{}, map[string]bool{}
	for _, output := range disc.outputs {
		metadataExists[output.outputKey], metadataSymlink[output.outputKey] = sidecarState(output.path)
		candidates := labelsByKey[output.outputKey]
		if plan.eligibilityEnabled && len(candidates) == 1 && !metadataExists[output.outputKey] && !metadataSymlink[output.outputKey] {
			for _, task := range tasksByOutput[output.outputKey] {
				if task.kind == "test" && recognizedTestSource(task) {
					coreTestKeys[output.outputKey] = true
					for label := range candidates {
						knownCore[label] = true
					}
					break
				}
			}
		}
	}
	strict := plan.requireTestResults || mode == "required"
	var selected, checked []postOutput
	labelByOutput := map[string]string{}
	var skipped []string
	ambiguous, identified := map[string]bool{}, map[string]bool{}
	for _, output := range disc.outputs {
		tasks := tasksByOutput[output.outputKey]
		explicit := map[string]bool{}
		for _, task := range tasks {
			if task.label != "" {
				explicit[task.label] = true
			}
		}
		label := ""
		if len(explicit) == 1 {
			label = sortedKeys(explicit)[0]
		} else {
			label = targetLabel(output.path)
		}
		sidecarLabel := label
		if plan.eligibilityEnabled && (metadataSymlink[output.outputKey] || metadataExists[output.outputKey] && label == "") {
			if strict {
				return filterResult{}, failRun(2, "BEP required freshness cannot authorize output because bazel.target metadata is missing or unsafe")
			}
			skipped = append(skipped, output.outputKey)
			continue
		}
		candidates := labelsByKey[output.outputKey]
		recognizedCore := false
		if label == "" && len(candidates) == 1 {
			recognizedCore = coreTestKeys[output.outputKey]
			for candidate := range candidates {
				if knownCore[candidate] {
					label = candidate
				}
			}
		}
		if (sidecarLabel != "" || recognizedCore) && plan.eligible[pair{label, output.outputKey}] {
			identified[label] = true
		}
		if label != "" {
			labelByOutput[output.outputKey] = label
		}
		if plan.ambiguousOutputs[output.outputKey] {
			ambiguous[output.outputKey] = true
			skipped = append(skipped, output.outputKey)
			continue
		}
		// An output with nothing to upload is neither selected nor skipped,
		// but the doctor still checks it when the selection would keep it.
		uploads := len(tasks) > 0
		keep := func() {
			checked = append(checked, output)
			if uploads {
				selected = append(selected, output)
			}
		}
		skip := func() {
			if uploads {
				skipped = append(skipped, output.outputKey)
			}
		}
		if label != "" && (plan.blockedLabels[label] || plan.ambiguousLabels[label]) {
			skip()
			continue
		}
		if !plan.eligibilityEnabled {
			keep()
			continue
		}
		if label == "" {
			if uploads && plan.selectedSource == "bep" && strict {
				return filterResult{}, failRun(2, "BEP required freshness cannot authorize %s because bazel.target metadata is missing", output.path)
			}
			skip()
			continue
		}
		if plan.eligible[pair{label, output.outputKey}] {
			keep()
			continue
		}
		skip()
	}
	selectedKeys := map[string]bool{}
	for _, output := range selected {
		selectedKeys[output.outputKey] = true
	}
	filtered := postDiscovery{outputs: selected}
	for _, task := range disc.tasks {
		if selectedKeys[task.outputKey] {
			if task.label == "" {
				task.label = labelByOutput[task.outputKey]
			}
			filtered.tasks = append(filtered.tasks, task)
		}
	}
	warnings := append(append([]string{}, disc.warnings...), plan.warnings...)
	if len(skipped) > 0 {
		warnings = append(warnings, "freshness_outputs_skipped")
	}
	filtered.warnings = uniqueCodes(warnings)
	skippedSet := map[string]bool{}
	for _, key := range skipped {
		skippedSet[key] = true
	}
	return filterResult{filtered, sortedKeys(skippedSet), sortedKeys(identified), sortedKeys(ambiguous), checked}, nil
}

// testEvidence is collect_test_result_evidence and
// collect_empty_session_evidence over the upload's results.
func testEvidence(disc postDiscovery, results map[string]postResult) (proven map[pair]bool, labels map[string]bool, empty map[pair]bool) {
	proven, labels, empty = map[pair]bool{}, map[string]bool{}, map[pair]bool{}
	for _, task := range disc.tasks {
		result, ok := results[task.id]
		if !ok || task.kind != "test" {
			continue
		}
		delivered := result.Status == "succeeded" || result.Requests.Attempted > 0
		if result.TestEvents > 0 && task.label != "" {
			labels[task.label] = true
			if task.outputKey != "" && delivered {
				proven[pair{task.label, task.outputKey}] = true
			}
		}
		if result.EmptyTestSession && task.label != "" && task.outputKey != "" && delivered {
			empty[pair{task.label, task.outputKey}] = true
		}
	}
	return proven, labels, empty
}

// representedTargets is represented_test_targets.
func representedTargets(plan freshnessPlan) map[string]bool {
	represented := union(labelsOf(plan.eligible), labelsOf(plan.cached), plan.skippedTargets,
		exclusivelyCachedLabels(plan.executions))
	for _, remote := range plan.remoteOnly {
		represented[remote.label] = true
	}
	return represented
}

// validateAccounted is validate_fresh_outputs_accounted. It returns a
// warning, or an error that fails the run.
func validateAccounted(plan freshnessPlan, disc postDiscovery, results map[string]postResult, expected []string,
	failOnError bool, identified, relevantAmbiguous []string) (string, error) {
	if plan.selectedSource != "bep" {
		return "", nil
	}
	expectedSet := map[string]bool{}
	for _, label := range expected {
		expectedSet[label] = true
	}
	selectedTargets := union(expectedSet)
	for _, label := range identified {
		selectedTargets[label] = true
	}
	ambiguous := map[string]bool{}
	for _, key := range relevantAmbiguous {
		ambiguous[key] = true
	}
	for label := range plan.ambiguousLabels {
		if selectedTargets[label] {
			return "", failRun(2, "BEP freshness is ambiguous: target reported as both platform-skipped and executed")
		}
	}
	if len(expectedSet) > 0 {
		for _, e := range plan.executions {
			if expectedSet[e.label] {
				for _, key := range e.outputKeys {
					if plan.ambiguousOutputs[key] {
						ambiguous[key] = true
					}
				}
			}
		}
	}
	if len(ambiguous) > 0 {
		return "", failRun(2, "BEP freshness has ambiguous execution provenance for %s", sortedKeys(ambiguous)[0])
	}
	if len(expectedSet) > 0 {
		represented := representedTargets(plan)
		var missing []string
		for label := range expectedSet {
			if !represented[label] {
				missing = append(missing, label)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			detail := "no TestResult matched this target"
			if plan.missingOutputLabels[missing[0]] {
				detail = "the fresh TestResult did not contain a mappable test.outputs reference"
			}
			return "", failRun(1, "expected target output is neither fresh nor exclusively cached in BEP: %s (%s)", missing[0], detail)
		}
	}
	if plan.requireTestResults {
		if message := plan.firstCompletenessError(selectedTargets); message != "" {
			return "", failRun(1, "%s", message)
		}
	}
	enforce := failOnError || plan.requireTestResults
	var remote []remoteOutput
	for _, output := range plan.remoteOnly {
		if selectedTargets[output.label] || (plan.requireTestResults || plan.requireRemoteArtifacts) && len(expectedSet) == 0 {
			remote = append(remote, output)
		}
	}
	if len(remote) > 0 && (enforce || plan.requireRemoteArtifacts) {
		code := 2
		if plan.requireTestResults {
			code = 1
		}
		return "", failRun(code, "BEP references remote-only test outputs for %s, but local test.outputs was not found", remote[0].label)
	}
	if len(plan.executions) > 0 {
		proven, observed, empty := testEvidence(disc, results)
		selectedLabels := union(selectedTargets, observed)
		if shard, missing := missingTestShards(plan.executions, proven, selectedLabels, empty); missing {
			message := "fresh shard produced no test-result evidence: " + shard.label + " configuration=" +
				pyformat.String(shard.configuration) + " run=" + itoa(int(shard.run)) + " shard=" + itoa(int(shard.shard)) +
				" invocation=" + shard.invocation
			if enforce {
				return "", failRun(1, "%s", message)
			}
			return message, nil
		}
		for _, task := range disc.tasks {
			result, ok := results[task.id]
			if task.kind == "test" && selectedLabels[task.label] && ok && result.Status == "failed" && result.Requests.Attempted == 0 {
				message := "fresh test payload failed before delivery: " + result.Path
				if enforce {
					return "", failRun(1, "%s", message)
				}
				return message, nil
			}
		}
		return "", nil
	}
	if !enforce {
		return "", nil
	}
	handled := map[pair]bool{}
	for _, task := range disc.tasks {
		if result, ok := results[task.id]; ok && result.Status != "skipped" && task.label != "" && task.outputKey != "" {
			handled[pair{task.label, task.outputKey}] = true
		}
	}
	if len(expectedSet) > 0 {
		handledLabels := labelsOf(handled)
		var unhandled []pair
		for pr := range plan.eligible {
			if !handledLabels[pr.label] {
				unhandled = append(unhandled, pr)
			}
		}
		if len(unhandled) > 0 {
			sort.Slice(unhandled, func(a, b int) bool { return lessPair(unhandled[a], unhandled[b]) })
			return "", failRun(1, "fresh expected test output produced no uploadable payloads: %s %s", unhandled[0].label, unhandled[0].key)
		}
		for _, output := range plan.remoteOnly {
			if expectedSet[output.label] {
				return "", failRun(2, "BEP references remote-only test outputs for %s, but local test.outputs was not found", output.label)
			}
		}
		return "", nil
	}
	if len(plan.eligible) > 0 && len(handled) == 0 {
		return "", failRun(1, "BEP reported %d fresh test output(s), but none produced uploadable payloads", len(plan.eligible))
	}
	return "", nil
}
