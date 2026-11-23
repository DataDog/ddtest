package bazelinspect

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceNames(discovery Discovery) []string {
	var names []string
	for _, source := range discovery.Sources {
		output := discovery.Outputs[source.Output]
		names = append(names, output.OutputKey+"/payloads/"+source.Directory+"/"+source.Name+":"+source.PayloadType)
	}
	return names
}

func discoverRoot(t *testing.T, request DiscoverRequest) Discovery {
	t.Helper()
	for i := range request.ScanRoots {
		resolved, err := filepath.EvalSymlinks(request.ScanRoots[i].Path)
		if err != nil {
			t.Fatal(err)
		}
		request.ScanRoots[i].Path = resolved
	}
	request.Version = 1
	return Discover(request, 2)
}

// Outputs are found and keyed as the uploader keys them: a retry output
// replaces the attempt's test.outputs key, sources follow the payload type
// order, and only .json and .msgpack files count.
func TestDiscoverFindsOutputsAndSourcesInTheUploadersOrder(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "pkg", "t", "test.outputs")
	for _, name := range []string{"payloads/tests/b.json", "payloads/tests/A.JSON", "payloads/tests/c.json.gz",
		"payloads/tests/.json", "payloads/tests/x.", "payloads/coverage/c.msgpack", "payloads/telemetry/t.json"} {
		write(t, filepath.Join(output, name), "{}")
	}
	write(t, filepath.Join(output, metadataName), "{}")
	retry := filepath.Join(root, "pkg", "t", "test_attempts", "attempt_1.outputs")
	write(t, filepath.Join(retry, "payloads", "tests", "r.json"), "{}")
	if err := os.MkdirAll(filepath.Join(root, "pkg", "empty", "test.outputs", "logs"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverRoot(t, DiscoverRequest{ScanRoots: []ScanRoot{{Path: root}}})
	want := []string{
		"pkg/empty/test.outputs",
		"pkg/t/test.outputs",
		"pkg/t/test_attempts/attempt_1.outputs/test.outputs",
	}
	var keys []string
	for _, output := range got.Outputs {
		keys = append(keys, output.OutputKey)
	}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("outputs %v, want %v", keys, want)
	}
	wantSources := []string{
		"pkg/t/test.outputs/payloads/tests/A.JSON:test",
		"pkg/t/test.outputs/payloads/tests/b.json:test",
		"pkg/t/test.outputs/payloads/coverage/c.msgpack:coverage",
		"pkg/t/test.outputs/payloads/telemetry/t.json:telemetry",
		"pkg/t/test_attempts/attempt_1.outputs/test.outputs/payloads/tests/r.json:test",
	}
	if strings.Join(sourceNames(got), ",") != strings.Join(wantSources, ",") || len(got.Warnings) != 0 {
		t.Fatalf("sources %v, warnings %v", sourceNames(got), got.Warnings)
	}
}

// A selected staged output wins over a local one with the same key. A
// selected key that staging did not write drops its stale local output.
func TestDiscoverPrefersSelectedStagedOutputs(t *testing.T) {
	local, staged := t.TempDir(), t.TempDir()
	for _, root := range []string{local, staged} {
		write(t, filepath.Join(root, "pkg", "t", "test.outputs", "payloads", "tests", filepath.Base(root)+".json"), "{}")
	}
	write(t, filepath.Join(local, "pkg", "stale", "test.outputs", "payloads", "tests", "s.json"), "{}")
	got := discoverRoot(t, DiscoverRequest{
		ScanRoots:        []ScanRoot{{Path: staged, Staged: true}, {Path: local}},
		StagedOutputKeys: []string{"/pkg/t/test.outputs", "pkg/stale/test.outputs"},
	})
	if len(got.Outputs) != 1 || !got.Outputs[0].Staged || len(got.Sources) != 1 ||
		strings.Join(got.Warnings, ",") != "selected_staged_output_missing" {
		t.Fatalf("got %+v", got)
	}
}

// Links in payload directories are refused, as the uploader refuses them: a
// linked payload directory, a linked source and a dangling one.
func TestDiscoverRefusesLinkedPayloads(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write(t, filepath.Join(outside, "elsewhere.json"), "{}")
	output := filepath.Join(root, "pkg", "t", "test.outputs")
	write(t, filepath.Join(output, "payloads", "tests", "kept.json"), "{}")
	if err := os.MkdirAll(filepath.Join(output, "payloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, outside, filepath.Join(output, "payloads", "coverage"))
	symlink(t, filepath.Join(outside, "elsewhere.json"), filepath.Join(output, "payloads", "tests", "linked.json"))
	symlink(t, filepath.Join(outside, "missing.json"), filepath.Join(output, "payloads", "tests", "dangling.json"))

	got := discoverRoot(t, DiscoverRequest{ScanRoots: []ScanRoot{{Path: root}}})
	if strings.Join(sourceNames(got), ",") != "pkg/t/test.outputs/payloads/tests/kept.json:test" ||
		strings.Join(got.Warnings, ",") != "payload_symlink_skipped" {
		t.Fatalf("sources %v, warnings %v", sourceNames(got), got.Warnings)
	}
}

// A shallow max_depth that finds nothing says so.
func TestDiscoverReportsAShallowMaxDepth(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b", "c", "test.outputs", "payloads", "tests", "t.json"), "{}")
	got := discoverRoot(t, DiscoverRequest{ScanRoots: []ScanRoot{{Path: root}}, MaxDepth: 2})
	if len(got.Outputs) != 0 || strings.Join(got.Warnings, ",") != "max_depth_may_be_too_shallow" {
		t.Fatalf("got %+v", got)
	}
}

func TestDiscoverCommand(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "pkg", "test.outputs", "payloads", "tests", "t.json"), "{}")
	request, _ := json.Marshal(DiscoverRequest{Version: 1, ScanRoots: []ScanRoot{{Path: root}}})
	var stdout, stderr bytes.Buffer
	if status := DiscoverCommand("ddtest", nil, bytes.NewReader(request), &stdout, &stderr); status != 0 {
		t.Fatalf("status %d: %s", status, stderr.String())
	}
	var got Discovery
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || len(got.Sources) != 1 {
		t.Fatalf("report %s: %v", stdout.String(), err)
	}
	for _, input := range []string{`{"schema_version":2,"scan_roots":[]}`, `{"schema_version":1,"scan_roots":[],"max_depth":-1}`} {
		stderr.Reset()
		if status := DiscoverCommand("ddtest", nil, strings.NewReader(input), &stdout, &stderr); status != 1 {
			t.Fatalf("%s: status %d", input, status)
		}
	}
}
