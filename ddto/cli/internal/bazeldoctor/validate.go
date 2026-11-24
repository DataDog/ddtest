package bazeldoctor

import (
	"sort"
	"strings"

	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

var validSelections = map[string]bool{"module": true, "module_override": true, "full_bundle_disabled": true,
	"full_bundle_importpath_miss": true, "full_bundle_no_match": true}

var defaultSelections = []string{"module", "module_override", "full_bundle_disabled", "full_bundle_importpath_miss"}

const validSelectionsText = "full_bundle_disabled, full_bundle_importpath_miss, full_bundle_no_match, module, module_override"

// allowedSelections is _effective_allowed_payload_selections.
func (d *doctor) allowedSelections(configured []string, forbidNoMatch bool) map[string]bool {
	allowed := map[string]bool{}
	source := configured
	if len(configured) == 0 {
		source = defaultSelections
	}
	for _, selection := range source {
		allowed[selection] = true
	}
	var invalid []string
	for selection := range allowed {
		if !validSelections[selection] {
			invalid = append(invalid, selection)
		}
	}
	if len(invalid) > 0 {
		sort.Strings(invalid)
		d.out.fail("allowed_payload_selections contains unsupported value(s): " + strings.Join(invalid, ", ") +
			"; expected one of: " + validSelectionsText)
	}
	if len(configured) == 0 && !forbidNoMatch {
		allowed["full_bundle_no_match"] = true
	}
	if forbidNoMatch && allowed["full_bundle_no_match"] {
		d.out.fail("allowed_payload_selections includes full_bundle_no_match while forbid_full_bundle_no_match=True")
	}
	return allowed
}

type validation struct {
	requireJSON, requireMetadata, forbidNoMatch, forbidMsgpack bool
	allowed                                                    []string
	expected                                                   *pyjson.Object
	targets                                                    map[string]string
	testResults, emptySessions                                 map[string]string
}

// validateOutputs is _validate_outputs.
func (d *doctor) validateOutputs(dirs []string, v validation) map[string]int {
	allowed := d.allowedSelections(v.allowed, v.forbidNoMatch)
	for i, target := range v.expected.Keys {
		if unhashable(v.expected.Values[i]) {
			unsupported("unhashable expected payload selection")
		}
		selection, ok := v.expected.Values[i].(string)
		if !ok || !validSelections[selection] {
			d.out.fail("expected_payload_selection_by_target[" + repr(target) + "] has unsupported value " +
				repr(v.expected.Values[i]) + "; expected one of: " + validSelectionsText)
		}
	}
	payloadCount, metadataCount := 0, 0
	summary := map[string]int{}
	for _, dir := range dirs {
		hasTestEvents, hasEmptySession := false, false
		outputLabel := v.targets[d.resolved(dir)]
		files := d.outputFilesOf(dir)
		payloadCount += len(files.json)
		metadataCount += len(files.metadata)
		if v.forbidMsgpack && len(files.msgpack) > 0 {
			d.out.fail("raw msgpack payloads are not supported in Bazel file mode under " + dir + ": " +
				strings.Join(files.msgpack, ", ") + ". Tests must write JSON payloads to TEST_UNDECLARED_OUTPUTS_DIR; " +
				"check that Go tests are built by the ddtest instrument SDK.")
		}
		if v.requireJSON && len(files.json) == 0 {
			d.out.fail("missing JSON payloads under " + dir)
		}
		testsDir := joinPath(dir, "payloads", "tests")
		for _, payload := range files.json {
			facts := d.payloadTestFacts(payload)
			if isBelow(payload, testsDir) {
				hasTestEvents = hasTestEvents || facts.testEvents > 0
				hasEmptySession = hasEmptySession || facts.emptySession
			}
		}
		if v.requireMetadata && len(files.metadata) == 0 {
			d.out.fail("missing bazel_target_metadata.json under " + dir)
		}
		for _, file := range files.metadata {
			document, isObject := d.loadMetadata(file).(*pyjson.Object)
			if !isObject {
				unsupported("sidecar " + file + " is not an object")
			}
			selection, present := document.Get("bazel.go.payload_selection")
			if !present {
				selection = nil
			}
			text, isText := selection.(string)
			if isText && text == "full_bundle_importpath_miss" {
				importpath, okImport := document.Lookup("bazel.go.importpath", nil).(string)
				root, okRoot := document.Lookup("bazel.go.runtime_module_path", nil).(string)
				if !(okImport && okRoot && root != "" && (importpath == root || strings.HasPrefix(importpath, root+"/"))) {
					d.out.fail(file + " reports full_bundle_importpath_miss without a canonical Go module boundary")
				}
			}
			if selection != nil {
				if unhashable(selection) {
					unsupported("unhashable payload selection in " + file)
				}
				if !isText || !validSelections[text] {
					d.out.fail(file + " has unsupported bazel.go.payload_selection=" + repr(selection) +
						"; expected one of: " + validSelectionsText)
				}
				if !allowed[text] {
					names := make([]string, 0, len(allowed))
					for name := range allowed {
						names = append(names, name)
					}
					sort.Strings(names)
					d.out.fail(file + " has bazel.go.payload_selection=" + repr(selection) +
						"; allowed values for this doctor target are: " + strings.Join(names, ", "))
				}
				summary[text]++
			}
			label := metadataTargetLabel(document, v.targets[d.resolved(dir)])
			if outputLabel == "" {
				outputLabel = label
			}
			if label != "" {
				if expected, ok := v.expected.Get(label); ok && !(isText && stringEquals(expected, text)) {
					d.out.fail(file + " has bazel.go.payload_selection=" + repr(selection) + " for " + label +
						"; expected " + repr(expected))
				}
			}
		}
		if v.emptySessions != nil && hasEmptySession && outputLabel != "" {
			v.emptySessions[d.resolved(dir)] = outputLabel
		}
		if v.testResults != nil && hasTestEvents && outputLabel != "" {
			v.testResults[d.resolved(dir)] = outputLabel
		}
	}
	if v.requireJSON && payloadCount == 0 {
		d.out.fail("no JSON payload files were found under selected test.outputs directories")
	}
	if v.requireMetadata && metadataCount == 0 {
		d.out.fail("no bazel_target_metadata.json files were found under selected test.outputs directories")
	}
	return summary
}

// unhashable is true for values that a Python set lookup rejects.
func unhashable(value any) bool {
	switch value.(type) {
	case []any, *pyjson.Object:
		return true
	}
	return false
}

// isBelow is `directory in Path(file).parents`.
func isBelow(file, directory string) bool {
	x, y := pathParts(file), pathParts(directory)
	if len(x) <= len(y) {
		return false
	}
	for i := range y {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// formatSelectionSummary is _format_selection_summary.
func formatSelectionSummary(summary map[string]int) string {
	if len(summary) == 0 {
		return "none"
	}
	names := make([]string, 0, len(summary))
	for name := range summary {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + "=" + itoa(summary[name])
	}
	return strings.Join(parts, ", ")
}

// validateExpectedFreshness is _validate_expected_target_bep_freshness.
func (d *doctor) validateExpectedFreshness(dirs []string, expected map[string]bool, f *freshness, required bool,
	keyByDir, labelByDir map[string]string, requireMetadata bool) []string {
	if !required {
		return dirs
	}
	for _, remote := range f.remoteOnly {
		if len(expected) == 0 || expected[remote.label] {
			d.out.fail("BEP references remote-only test outputs for " + remote.label + ": " +
				displayArtifactReference(remote.artifact) + ". Rerun bazel test with --build_event_json_file and " +
				remoteTestOutputDownloadHint)
		}
	}
	for label := range expected {
		f.identifiedLabels[label] = true
	}
	freshLabels, cachedLabels := labelsOf(f.eligible), labelsOf(f.cached)
	cachedOnly := exclusivelyCachedLabels(f.executions)
	if len(f.executions) == 0 {
		cachedOnly = map[string]bool{}
		for label := range cachedLabels {
			if !freshLabels[label] {
				cachedOnly[label] = true
			}
		}
	}
	index := newOutputIndex(f.eligible)
	type candidate struct {
		dir    string
		labels map[string]bool
	}
	var candidates []candidate
	for _, dir := range dirs {
		if labels := index.labelsFor(dir, keyByDir[d.resolved(dir)]); len(labels) > 0 {
			candidates = append(candidates, candidate{dir, labels})
		}
	}
	inspected := make([]string, len(candidates))
	for i, c := range candidates {
		inspected[i] = c.dir
	}
	d.inspect(inspected)
	var freshDirs []string
	matchedLabels := map[string]bool{}
	for _, c := range candidates {
		files := d.outputFilesOf(c.dir)
		if len(files.json) == 0 && len(files.msgpack) == 0 {
			continue
		}
		label := ""
		if len(files.metadata) == 0 && requireMetadata {
			d.out.fail("BEP required freshness cannot authorize " + c.dir + " because bazel_target_metadata.json is missing.")
		}
		var document any = &pyjson.Object{}
		if len(files.metadata) > 0 {
			document = d.loadMetadata(files.metadata[0])
		}
		if _, isObject := document.(*pyjson.Object); isObject {
			label = metadataTargetLabel(document, "")
		}
		if explicit, ok := labelByDir[d.resolved(c.dir)]; ok {
			label = explicit
		}
		if label == "" && !requireMetadata && len(c.labels) == 1 {
			for only := range c.labels {
				label = only
			}
			labelByDir[d.resolved(c.dir)] = label
		}
		if label == "" {
			d.out.fail("BEP required freshness cannot authorize " + c.dir +
				" because bazel_target_metadata.json does not contain bazel.target.")
		}
		if c.labels[label] {
			freshDirs = append(freshDirs, c.dir)
			matchedLabels[label] = true
		}
	}
	var missing []string
	for label := range expected {
		if !matchedLabels[label] && !cachedOnly[label] && !f.skipped[label] {
			missing = append(missing, label)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		label := missing[0]
		reason := "no fresh BEP TestResult matched this target's local test.outputs"
		switch {
		case f.missingMappings[label]:
			reason = "the fresh TestResult did not contain a mappable test.outputs reference"
		case cachedLabels[label]:
			reason = "BEP also reported a fresh result, but no fresh test.outputs could be authorized"
		}
		d.out.fail("expected target output is neither fresh nor exclusively cached in BEP: " + label + " (" + reason +
			"). Rerun bazel test with --build_event_json_file and " + remoteTestOutputDownloadHint +
			" then rerun the doctor with --bep-json and --freshness-source=bep --freshness-mode=required.")
	}
	return freshDirs
}

// validateDiscoveredFreshness is _validate_discovered_bep_freshness.
func (d *doctor) validateDiscoveredFreshness(dirs []string, f *freshness, required bool, keyByDir, labelByDir map[string]string,
	requireMetadata bool) []string {
	if !required {
		return dirs
	}
	localLabels := map[string]bool{}
	for _, dir := range dirs {
		if explicit := labelByDir[d.resolved(dir)]; explicit != "" {
			localLabels[explicit] = true
			continue
		}
		for _, file := range d.outputFilesOf(dir).metadata {
			if label := metadataTargetLabel(d.loadMetadata(file), ""); label != "" {
				localLabels[label] = true
			}
		}
	}
	index := newOutputIndex(f.eligible)
	var freshDirs []string
	for _, dir := range dirs {
		labels := index.labelsFor(dir, keyByDir[d.resolved(dir)])
		if len(labels) == 0 {
			continue
		}
		files := d.outputFilesOf(dir)
		if len(files.json) == 0 && len(files.msgpack) == 0 {
			for _, file := range files.metadata {
				if label := metadataTargetLabel(d.loadMetadata(file), ""); labels[label] && label != "" {
					f.identifiedLabels[label] = true
				}
			}
			continue
		}
		if len(files.metadata) == 0 && requireMetadata {
			d.out.fail("BEP required freshness cannot authorize " + dir + " because bazel_target_metadata.json is missing.")
		}
		var document any = &pyjson.Object{}
		if len(files.metadata) > 0 {
			document = d.loadMetadata(files.metadata[0])
		}
		label := metadataTargetLabel(document, "")
		if explicit, ok := labelByDir[d.resolved(dir)]; ok {
			label = explicit
		}
		if label == "" && !requireMetadata && len(labels) == 1 {
			for only := range labels {
				label = only
			}
			labelByDir[d.resolved(dir)] = label
		}
		if label == "" {
			d.out.fail("BEP required freshness cannot authorize " + dir +
				" because bazel_target_metadata.json does not contain bazel.target.")
		}
		if labels[label] {
			if len(files.metadata) > 0 {
				f.identifiedLabels[label] = true
			}
			freshDirs = append(freshDirs, dir)
		}
	}
	for _, remote := range f.remoteOnly {
		if f.identifiedLabels[remote.label] {
			d.out.fail("BEP references remote-only test outputs for " + remote.label + ": " +
				displayArtifactReference(remote.artifact) + ". Rerun bazel test with --build_event_json_file and " +
				remoteTestOutputDownloadHint)
		}
	}
	var missingLocal []string
	for label := range f.identifiedLabels {
		if f.missingMappings[label] {
			missingLocal = append(missingLocal, label)
		}
	}
	if len(missingLocal) > 0 {
		sort.Strings(missingLocal)
		d.out.fail("BEP required freshness cannot authorize discovered Test Optimization output for " + missingLocal[0] +
			" because the fresh TestResult did not contain a mappable test.outputs reference. Rerun bazel test with " +
			"--build_event_json_file and " + remoteTestOutputDownloadHint)
	}
	if len(freshDirs) == 0 {
		var cachedLocal []string
		for p := range f.cached {
			if localLabels[p.label] {
				cachedLocal = append(cachedLocal, p.label)
			}
		}
		reason := "no fresh BEP TestResult matched local test.outputs"
		if len(cachedLocal) > 0 {
			sort.Strings(cachedLocal)
			reason = "BEP reported only cached results for " + cachedLocal[0]
		}
		d.out.fail("BEP required freshness did not authorize any discovered Test Optimization output directories (" +
			reason + "). Rerun bazel test with --build_event_json_file and " + remoteTestOutputDownloadHint +
			" then rerun the doctor with --bep-json and --freshness-source=bep --freshness-mode=required.")
	}
	return freshDirs
}

// diagnosticEntries is _diagnostic_output_entries.
func (d *doctor) diagnosticEntries(dirs []string, targets map[string]string, staged []stagedArtifact) []any {
	stagedByDir := map[string]stagedArtifact{}
	for _, item := range staged {
		stagedByDir[d.resolved(item.outputDir)] = item
	}
	entries := []any{}
	for _, dir := range dirs {
		resolved := d.resolved(dir)
		item, isStaged := stagedByDir[resolved]
		fallbackLabel := targets[resolved]
		files := d.outputFilesOf(dir)
		metadata := map[string]any{"count": len(files.metadata), "files": reportList(files.metadata),
			"target": fallbackLabel, "payload_selection": ""}
		for _, file := range files.metadata {
			document, isObject := d.reportMetadata(file).(*pyjson.Object)
			if !isObject {
				continue
			}
			if target := metadataTargetLabel(document, fallbackLabel); target != "" {
				metadata["target"] = target
			}
			if selection, ok := document.Lookup("bazel.go.payload_selection", nil).(string); ok {
				metadata["payload_selection"] = selection
			}
			break
		}
		label, _ := metadata["target"].(string)
		if label == "" {
			label = fallbackLabel
		}
		key := ""
		if isStaged {
			key = item.outputKey
		} else {
			key = d.localOutputKey(dir)
		}
		counts := d.payloadCounts(dir)
		entries = append(entries, map[string]any{"path": resolved, "label": label, "source": map[bool]string{true: "staged",
			false: "local"}[isStaged], "output_key": key, "payloads": counts.report(), "metadata": metadata,
			"upload_candidate": counts.json > 0})
	}
	return entries
}

type payloadCounts struct{ json, msgpack, tests, coverage, telemetry int }

func (c payloadCounts) report() map[string]any {
	return map[string]any{"json": c.json, "msgpack": c.msgpack, "tests": c.tests, "coverage": c.coverage,
		"telemetry": c.telemetry}
}

// payloadCounts is _diagnostic_payload_counts.
func (d *doctor) payloadCounts(dir string) payloadCounts {
	files := d.outputFilesOf(dir)
	counts := payloadCounts{json: len(files.json), msgpack: len(files.msgpack)}
	root := joinPath(dir, "payloads")
	for _, payload := range files.json {
		relative, ok := relativeTo(payload, root)
		if !ok || relative == "." {
			continue
		}
		bucket, _, _ := strings.Cut(relative, "/")
		switch bucket {
		case "tests":
			counts.tests++
		case "coverage":
			counts.coverage++
		case "telemetry":
			counts.telemetry++
		}
	}
	return counts
}

// updateOutputSummary is _update_diagnostic_output_summary.
func (r report) updateOutputSummary(entries []any) {
	totals := payloadCounts{}
	metadataCount, candidates := 0, 0
	for _, raw := range entries {
		entry := raw.(map[string]any)
		payloads := entry["payloads"].(map[string]any)
		totals.json += payloads["json"].(int)
		totals.msgpack += payloads["msgpack"].(int)
		totals.tests += payloads["tests"].(int)
		totals.coverage += payloads["coverage"].(int)
		totals.telemetry += payloads["telemetry"].(int)
		metadataCount += entry["metadata"].(map[string]any)["count"].(int)
		if entry["upload_candidate"].(bool) {
			candidates++
		}
	}
	r["outputs"] = entries
	summary := r.section("summary")
	summary["validated_output_dirs"] = len(entries)
	summary["upload_candidates"] = candidates
	summary["payloads"] = totals.report()
	summary["metadata_files"] = metadataCount
}
