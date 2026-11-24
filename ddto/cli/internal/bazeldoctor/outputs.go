package bazeldoctor

import (
	"os"
	"sort"
	"strings"

	"github.com/DataDog/ddtest/ddto/cli/internal/bazelinspect"
	"github.com/DataDog/ddtest/ddto/cli/internal/pyjson"
)

// outputFiles is _OutputFiles.
type outputFiles struct {
	json, msgpack, metadata []string
}

type payloadFacts struct {
	testEvents   int
	emptySession bool
}

// outputs holds the doctor's run cache: each output is listed and decoded
// once, by ddtest's inspection, and each path resolved once.
type outputs struct {
	files    map[string]outputFiles
	facts    map[string]payloadFacts
	metadata map[string]any
	resolved map[string]string
	keys     map[string]string
}

func newOutputs() *outputs {
	return &outputs{files: map[string]outputFiles{}, facts: map[string]payloadFacts{}, metadata: map[string]any{},
		resolved: map[string]string{}, keys: map[string]string{}}
}

// inspect is _inspect_outputs: it lists and decodes the outputs that the run
// has not seen.
func (d *doctor) inspect(dirs []string) {
	var pending []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		if _, done := d.cache.files[dir]; !done && !seen[dir] {
			seen[dir] = true
			pending = append(pending, dir)
		}
	}
	if len(pending) == 0 {
		return
	}
	report := bazelinspect.Inspect(pending, d.workers)
	for i, entry := range report.Outputs {
		dir := pending[i]
		var files outputFiles
		for _, payload := range entry.JSONPayloads {
			file := joinPath(dir, payload.Path)
			files.json = append(files.json, file)
			if payload.Decoded {
				d.cache.facts[file] = payloadFacts{payload.TestEvents, payload.EmptySession}
			}
		}
		for _, name := range entry.MsgpackPayloads {
			files.msgpack = append(files.msgpack, joinPath(dir, name))
		}
		for _, sidecar := range entry.Metadata {
			file := joinPath(dir, sidecar.Path)
			files.metadata = append(files.metadata, file)
			if len(sidecar.Document) > 0 {
				value, err := pyjson.Parse(sidecar.Document)
				if err != nil {
					unsupported("sidecar " + file)
				}
				d.cache.metadata[file] = value
			}
		}
		for _, list := range [][]string{files.json, files.msgpack, files.metadata} {
			sort.Slice(list, func(a, b int) bool { return lessPath(list[a], list[b]) })
		}
		d.cache.files[dir] = files
	}
}

// outputFilesOf is _output_files.
func (d *doctor) outputFilesOf(dir string) outputFiles {
	if _, done := d.cache.files[dir]; !done {
		d.inspect([]string{dir})
	}
	return d.cache.files[dir]
}

// payloadTestFacts is _payload_test_facts. A payload that ddtest could not
// decode fails in Python with its JSON error, which the Python doctor prints.
func (d *doctor) payloadTestFacts(file string) payloadFacts {
	facts, ok := d.cache.facts[file]
	if !ok {
		unsupported("payload that ddtest could not decode: " + file)
	}
	return facts
}

// loadMetadata is _load_metadata.
func (d *doctor) loadMetadata(file string) any {
	if value, ok := d.cache.metadata[file]; ok {
		return value
	}
	value := d.loadJSON(file)
	d.cache.metadata[file] = value
	return value
}

// reportMetadata is _report_metadata: a sidecar that does not load is nil.
func (d *doctor) reportMetadata(file string) any {
	if value, ok := d.cache.metadata[file]; ok {
		return value
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	return parseDocument(file, data)
}

// resolved is _resolved.
func (d *doctor) resolved(text string) string {
	if value, ok := d.cache.resolved[text]; ok {
		return value
	}
	value := resolvePath(text)
	d.cache.resolved[text] = value
	return value
}

// localOutputKey is _local_test_output_key.
func (d *doctor) localOutputKey(dir string) string {
	if key, ok := d.cache.keys[dir]; ok {
		return key
	}
	key := d.computeLocalOutputKey(dir)
	d.cache.keys[dir] = key
	return key
}

func (d *doctor) computeLocalOutputKey(dir string) string {
	if d.testlogs != "" {
		resolved, root := d.resolved(dir), d.resolved(d.testlogs)
		if relative, ok := relativeTo(resolved, root); ok {
			return testOutputKey(relative)
		}
	}
	return testOutputKey(dir)
}

// relativeTo is str(PurePosixPath(path).relative_to(root)).
func relativeTo(text, root string) (string, bool) {
	x, y := pathParts(text), pathParts(root)
	if len(x) < len(y) {
		return "", false
	}
	for i := range y {
		if x[i] != y[i] {
			return "", false
		}
	}
	if len(x) == len(y) {
		return ".", true
	}
	return strings.Join(x[len(y):], "/"), true
}

// testOutputKey is _bep_test_output_key without an artifact name.
func testOutputKey(value string) string {
	key, err := bazelinspect.BepTestOutputKey(value, "")
	if err != nil {
		unsupported("output key of " + value)
	}
	return key
}

// metadataTargetLabel is _metadata_target_label.
func metadataTargetLabel(document any, fallback string) string {
	if object, ok := document.(*pyjson.Object); ok {
		if target, ok := object.Lookup("bazel.target", nil).(string); ok && target != "" {
			return target
		}
	}
	return fallback
}

// expectedTargetOutputs is _expected_targets_outputs, found with one
// discovery.
func (d *doctor) expectedTargetOutputs(labels []string, allowMissing bool) [][]string {
	roots := make([]string, len(labels))
	for i, label := range labels {
		roots[i] = expectedTargetRoot(d.testlogs, label)
	}
	discovery := bazelinspect.Discover(bazelinspect.DiscoverRequest{Version: 1, TargetRoots: roots}, d.workers)
	result := make([][]string, len(labels))
	for i, target := range discovery.Targets {
		if !target.Exists || len(target.Outputs) == 0 {
			if !allowMissing {
				missingPart := "output root"
				if target.Exists {
					missingPart = "test.outputs directory"
				}
				d.out.fail("expected target " + missingPart + " not found for " + labels[i] + ": " + roots[i] +
					". Run this exact instrumented test target before running the doctor. Do not list build-only, " +
					"wrapper-only, or analysis-only targets in expected_targets. If tests ran with remote execution or " +
					"remote cache, " + remoteTestOutputDownloadHint)
			}
			continue
		}
		for _, output := range target.Outputs {
			result[i] = append(result[i], purePath(output))
		}
	}
	return result
}

// candidateOutputDirs is _discover_candidate_output_dirs.
func (d *doctor) candidateOutputDirs() []string {
	var dirs []string
	for _, output := range bazelinspect.FindTestOutputDirectories(d.testlogs, 0, d.workers, false) {
		dirs = append(dirs, purePath(output))
	}
	d.inspect(dirs)
	var candidates []string
	for _, dir := range dirs {
		files := d.outputFilesOf(dir)
		if len(files.json) > 0 || len(files.msgpack) > 0 || len(files.metadata) > 0 {
			candidates = append(candidates, dir)
		}
	}
	return candidates
}

const remoteTestOutputDownloadHint = "rerun them with --remote_download_minimal " +
	"--remote_download_regex=.*test[.]outputs.* so Bazel downloads test.outputs locally. " +
	"If the test run also uses --zip_undeclared_test_outputs, rerun the doctor/uploader " +
	"with --artifact-source=bep so local outputs.zip carriers are materialized before discovery."
