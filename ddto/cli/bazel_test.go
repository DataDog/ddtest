package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ddtest instrument bazel upload belongs to the bazel command: its help names
// it, and the word upload hands the remaining arguments to the uploader.
func TestBazelUpload(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := bazel("ddtest instrument", []string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "bazel upload --plan=FILE") {
		t.Fatalf("bazel --help: exit %d, %q", code, stdout.String())
	}
	stdout.Reset()
	if code := bazel("ddtest instrument", []string{"upload", "--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "--report-json") {
		t.Fatalf("bazel upload --help: exit %d, %q", code, stdout.String())
	}
	for _, name := range []string{"DD_API_KEY", "DD_TEST_OPTIMIZATION_AGENT_URL", "DD_TEST_OPTIMIZATION_AGENTLESS_URL", "DD_TEST_OPTIMIZATION_WORKERS"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	plan := filepath.Join(dir, "plan.json")
	if err := os.WriteFile(plan, []byte(`{"schema_version":1,"rules_version":"test","runtime_id":"fixture","sources":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := bazel("ddtest instrument", []string{"upload", "--dry-run", "--plan", plan, "--root", dir}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "mode=dry-run result=success") {
		t.Fatalf("bazel upload: exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	stderr.Reset()
	if code := bazel("ddtest instrument", []string{"--mini"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "give exactly one output directory") {
		t.Fatalf("bazel without upload no longer reaches RunBazel: exit %d, %q", code, stderr.String())
	}
}
