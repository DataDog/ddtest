package bazeldoctor

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

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
// the payloads, as the doctor's inspection would list them, and reads the
// sidecars as the doctor reads them. It runs before anything is sent: a
// sidecar that only the Python doctor reads falls back, since its checks
// could not run in Go once the payloads are gone.
func (p *poster) prelistOutputs(disc postDiscovery) error {
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
			value, err := pyjson.Parse(sidecar.Document)
			if len(sidecar.Document) == 0 || err != nil {
				return &postFallback{"doctor input: JSON in " + file}
			}
			p.d.cache.metadata[file] = value
		}
		for _, list := range [][]string{files.json, files.msgpack, files.metadata} {
			sort.Slice(list, func(a, b int) bool { return lessPath(list[a], list[b]) })
		}
		p.d.cache.files[dir] = files
	}
	return nil
}

// prepareValidation reads the doctor's configuration and lists the outputs
// that its checks validate, before anything is sent, and returns those
// outputs. An input that only the Python doctor reads falls back.
func (p *poster) prepareValidation(checked []postOutput, expected []string) ([]postOutput, error) {
	config, err := pyjson.Parse(p.request.Validation.Config)
	object, isObject := config.(*pyjson.Object)
	if err != nil || !isObject {
		return nil, &postFallback{"doctor configuration"}
	}
	p.d.config = object
	if err := p.prelistOutputs(postDiscovery{outputs: checked}); err != nil {
		return nil, err
	}
	return p.doctorOutputs(checked, expected), nil
}

// doctorOutputs are the outputs that the standalone doctor would validate:
// with expected targets, each of their outputs; otherwise only outputs that
// hold a payload or a sidecar, as its candidate directories do.
func (p *poster) doctorOutputs(outputs []postOutput, expected []string) []postOutput {
	if len(expected) > 0 {
		return outputs
	}
	var kept []postOutput
	for _, output := range outputs {
		files := p.d.cache.files[purePath(output.path)]
		if len(files.json) > 0 || len(files.msgpack) > 0 || len(files.metadata) > 0 {
			kept = append(kept, output)
		}
	}
	return kept
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
	p.seedFacts(disc, run)
	resolving := make([]string, 0, len(disc.outputs))
	for _, output := range disc.outputs {
		resolving = append(resolving, purePath(output.path))
	}
	d.resolveAll(resolving)
	targets := map[string]string{}
	for _, task := range disc.tasks {
		if task.label != "" {
			targets[d.resolved(purePath(task.outputDir))] = task.label
		}
	}
	dirs := outputDirs(disc)
	exitCode := 0
	err := p.doctorCall(func() {
		if pyjson.Truthy(d.configGet("forbid_dd_git_test_env", false)) {
			d.validateBazelrc()
		}
		if pyjson.Truthy(d.configGet("require_git_metadata", false)) {
			p.validateContextGit()
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
	case errors.Is(err, errPostInterrupted):
		return 130
	case err != nil:
		// The payloads were sent, so Python cannot run the checks instead:
		// unchecked outputs fail the run.
		reason := err.Error()
		if errors.As(err, &fallen) {
			reason = fallen.reason
		}
		message := "doctor checks could not run: " + reason
		p.log.errorf("%s", message)
		d.report.recordFailure(message)
		exitCode = 1
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

// validateContextGit is the doctor's _validate_git_metadata_contexts, over
// the contexts that the uploader loaded.
func (p *poster) validateContextGit() {
	resources := p.request.Resources
	if len(resources.ContextRecords) == 0 {
		p.d.out.fail("require_git_metadata=True but no context.json was provided in data")
	}
	present := func(context map[string]any, key string) bool {
		switch value := context[key].(type) {
		case nil:
			return false
		case string:
			return value != ""
		case bool:
			return value
		case []any:
			return len(value) > 0
		case map[string]any:
			return len(value) > 0
		case json.Number:
			number, err := value.Float64()
			return err != nil || number != 0
		}
		return true
	}
	for _, record := range resources.ContextRecords {
		context := resources.Contexts[record.Context]
		var missing []string
		if !present(context, "git.repository_url") {
			missing = append(missing, "git.repository_url")
		}
		if !present(context, "git.commit.sha") {
			missing = append(missing, "git.commit.sha")
		}
		if !present(context, "git.branch") && !present(context, "git.tag") {
			missing = append(missing, "git.branch or git.tag")
		}
		if len(missing) > 0 {
			p.d.out.fail("context " + record.RepoKey + " is missing required git metadata: " + strings.Join(missing, ", "))
		}
	}
}
