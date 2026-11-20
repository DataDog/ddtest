package bazelupload

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func clearUploadEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"DD_API_KEY", "DD_SITE", "DD_TEST_OPTIMIZATION_AGENT_URL", "DD_TEST_OPTIMIZATION_AGENTLESS_URL",
		"DD_TEST_OPTIMIZATION_KEEP_PAYLOADS", "DD_TEST_OPTIMIZATION_BATCH_TEST_PAYLOADS", "DD_TEST_OPTIMIZATION_GZIP",
		"DD_TEST_OPTIMIZATION_HTTP_KEEP_ALIVE", "DD_TEST_OPTIMIZATION_WORKERS"} {
		t.Setenv(name, "")
	}
}

func TestCommandWritesReport(t *testing.T) {
	clearUploadEnvironment(t)
	directory := t.TempDir()
	writeFixture(t, directory, "plan.json", `{"schema_version":1,"rules_version":"v1","runtime_id":"fixture","sources":[{"id":"fixture","path":"payload.json","kind":"test"}]}`)
	writeFixture(t, directory, "payload.json", `{"version":1,"events":[]}`)
	t.Setenv("DD_TEST_OPTIMIZATION_WORKERS", "2")

	var stdout, stderr bytes.Buffer
	reportPath := filepath.Join(directory, "report.json")
	code := Command(context.Background(), "ddtest instrument", []string{"--dry-run", "--plan", filepath.Join(directory, "plan.json"),
		"--root", directory, "--report-json", reportPath}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "mode=dry-run result=success") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Workers != 1 || report.Files.Skipped != 1 || report.Files.Retained != 1 || report.Requests.Attempted != 0 {
		t.Fatalf("unexpected report: %s", raw)
	}
	if _, err := os.Stat(filepath.Join(directory, "payload.json")); err != nil {
		t.Fatal(err)
	}
}

func TestCommandOptionsOverrideEnvironment(t *testing.T) {
	parse := func(args ...string) (Options, error) {
		t.Helper()
		line := newCommandLine("ddtest instrument")
		if err := line.flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		err := applyEnvironment(line.flags, &line.options)
		return line.options, err
	}
	clearUploadEnvironment(t)
	options, err := parse()
	if err != nil || !options.Gzip || !options.KeepAlive || options.Batch || options.KeepPayloads || options.Workers != 0 {
		t.Fatalf("defaults: %+v, %v", options, err)
	}
	t.Setenv("DD_TEST_OPTIMIZATION_GZIP", "no")
	t.Setenv("DD_TEST_OPTIMIZATION_BATCH_TEST_PAYLOADS", "YES")
	t.Setenv("DD_TEST_OPTIMIZATION_HTTP_KEEP_ALIVE", "0")
	t.Setenv("DD_TEST_OPTIMIZATION_WORKERS", "3")
	options, err = parse()
	if err != nil || options.Gzip || !options.Batch || options.KeepAlive || options.Workers != 3 {
		t.Fatalf("environment: %+v, %v", options, err)
	}
	options, err = parse("--gzip", "--batch-test-payloads=false", "--workers", "5")
	if err != nil || !options.Gzip || options.Batch || options.Workers != 5 {
		t.Fatalf("options over environment: %+v, %v", options, err)
	}
	t.Setenv("DD_TEST_OPTIMIZATION_KEEP_PAYLOADS", "maybe")
	if _, err := parse(); err == nil || !strings.Contains(err.Error(), "DD_TEST_OPTIMIZATION_KEEP_PAYLOADS") {
		t.Fatalf("an invalid boolean: %v", err)
	}
	t.Setenv("DD_TEST_OPTIMIZATION_KEEP_PAYLOADS", "")
	t.Setenv("DD_TEST_OPTIMIZATION_WORKERS", "0")
	if _, err := parse(); err == nil {
		t.Fatal("zero workers accepted")
	}
}

func TestCommandUsage(t *testing.T) {
	clearUploadEnvironment(t)
	var stdout, stderr bytes.Buffer
	if code := Command(context.Background(), "ddtest instrument", []string{"--help"}, &stdout, &stderr); code != 0 ||
		!strings.Contains(stdout.String(), "ddtest instrument bazel upload --plan=FILE") {
		t.Fatalf("--help: exit %d, %q", code, stdout.String())
	}
	for _, args := range [][]string{{}, {"--plan", "p.json", "extra"}, {"--unknown"}} {
		stderr.Reset()
		if code := Command(context.Background(), "ddtest instrument", args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}
