package bazeldoctor

import (
	"errors"
	"sort"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// The doctor's output checks in a post-process run. The outputs are listed
// before the upload deletes what it sends, and each payload's test facts
// come from the upload's own decoding of it: no payload is decoded twice.

func outputDirs(disc postDiscovery) []string {
	var dirs []string
	for _, output := range disc.outputs {
		dirs = append(dirs, purePath(output.path))
	}
	return dirs
}

// prelistOutputs lists the outputs' payloads and sidecars, without decoding
// the payloads, as the doctor's inspection would list them.
func (p *poster) prelistOutputs(disc postDiscovery) {
	dirs := outputDirs(disc)
	report := bazelinspect.List(dirs, p.workers)
	for i, entry := range report.Outputs {
		dir := dirs[i]
		var files outputFiles
		for _, payload := range entry.JSONPayloads {
			files.json = append(files.json, joinPath(dir, payload.Path))
		}
		for _, name := range entry.MsgpackPayloads {
			files.msgpack = append(files.msgpack, joinPath(dir, name))
		}
		for _, sidecar := range entry.Metadata {
			file := joinPath(dir, sidecar.Path)
			files.metadata = append(files.metadata, file)
			if len(sidecar.Document) > 0 {
				if value, err := pyjson.Parse(sidecar.Document); err == nil {
					p.d.cache.metadata[file] = value
				}
			}
		}
		for _, list := range [][]string{files.json, files.msgpack, files.metadata} {
			sort.Slice(list, func(a, b int) bool { return lessPath(list[a], list[b]) })
		}
		p.d.cache.files[dir] = files
	}
}

// seedFacts gives each listed payload its test facts: the upload's when it
// decoded the payload, otherwise one decoding of the file, which the upload
// left in place.
func (p *poster) seedFacts(disc postDiscovery, run uploadRun) {
	results := map[string]postResult{}
	for _, task := range disc.tasks {
		if result, ok := run.byID[task.id]; ok {
			results[purePath(task.sourcePath)] = result
		}
	}
	for _, files := range p.d.cache.files {
		for _, file := range files.json {
			if result, ok := results[file]; ok && (result.Status == "succeeded" || result.Requests.Attempted > 0) {
				p.d.cache.facts[file] = payloadFacts{result.TestEvents, result.EmptyTestSession}
				continue
			}
			if payload := bazelinspect.DecodePayload(file); payload.Decoded {
				p.d.cache.facts[file] = payloadFacts{payload.TestEvents, payload.EmptySession}
			}
		}
	}
}

// validateOutputs applies the doctor's output checks to the selected
// outputs and returns the doctor's exit status. The payloads were sent
// whatever it finds.
func (p *poster) validateOutputs(disc postDiscovery, run uploadRun) int {
	settings := p.request.Validation
	d := p.d
	config, err := pyjson.Parse(settings.Config)
	object, isObject := config.(*pyjson.Object)
	if err != nil || !isObject {
		p.log.warnf("doctor checks skipped: the doctor configuration is not a JSON object")
		return 0
	}
	d.config = object
	p.seedFacts(disc, run)
	targets := map[string]string{}
	for _, task := range disc.tasks {
		if task.label != "" {
			targets[d.resolved(purePath(task.outputDir))] = task.label
		}
	}
	dirs := outputDirs(disc)
	exitCode := 0
	err = p.doctorCall(func() {
		if pyjson.Truthy(d.configGet("forbid_dd_git_test_env", false)) {
			d.validateBazelrc()
		}
		if len(dirs) == 0 {
			d.report.updateOutputSummary([]any{})
			d.out.summary("OK: no fresh payloads require validation")
			return
		}
		d.report.updateOutputSummary(d.diagnosticEntries(dirs, targets, nil))
		var allowed []string
		if list, ok := d.configGet("allowed_payload_selections", nil).([]any); ok {
			for _, item := range list {
				if text, ok := item.(string); ok {
					allowed = append(allowed, text)
				}
			}
		}
		expected, ok := d.configGet("expected_payload_selection_by_target", &pyjson.Object{}).(*pyjson.Object)
		if !ok {
			expected = &pyjson.Object{}
		}
		summary := d.validateOutputs(dirs, validation{
			requireJSON:     pyjson.Truthy(d.configGet("require_json_payloads", true)),
			requireMetadata: pyjson.Truthy(d.configGet("require_bazel_metadata", true)),
			forbidNoMatch:   pyjson.Truthy(d.configGet("forbid_full_bundle_no_match", false)),
			forbidMsgpack:   pyjson.Truthy(d.configGet("forbid_msgpack_payloads", true)),
			allowed:         allowed, expected: expected, targets: targets,
			testResults: map[string]string{}, emptySessions: map[string]string{},
		})
		selection := map[string]any{}
		for name, count := range summary {
			selection[name] = count
		}
		d.report.section("summary")["payload_selection"] = selection
		d.out.summary("payload selection summary: " + formatSelectionSummary(summary))
		d.out.summary("OK: validated " + itoa(len(dirs)) + " test output directorie(s)")
	})
	var failed *postError
	var fallen *postFallback
	switch {
	case errors.As(err, &failed):
		d.report.recordFailure(failed.message)
		exitCode = 1
	case errors.As(err, &fallen):
		p.log.warnf("doctor checks skipped: %s", fallen.reason)
		return 0
	case err != nil:
		return 0
	default:
		d.report.recordSuccess()
	}
	if settings.ReportJSON != "" {
		if err := writeJSONReport(settings.ReportJSON, map[string]any(d.report)); err != nil {
			p.log.warnf("failed to write the doctor report: %v", err)
		}
	}
	return exitCode
}
