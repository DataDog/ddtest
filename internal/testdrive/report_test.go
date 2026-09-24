package testdrive

import (
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func TestStaticReportEscapesFindingsAndDescribesMissingCoverage(t *testing.T) {
	path, err := writeReport(t.TempDir(), t.TempDir(), intake.Facts{TestEventCount: 1, TestCount: 1, FailedTests: []intake.Test{{Name: "<script>alert(1)</script>"}}}, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"&lt;script&gt;", "Not reported", "Failed", "intake/", "test-output.txt"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(string(data), "<script>alert") {
		t.Fatal("unescaped test name")
	}
	model := buildReport(t.TempDir(), intake.Facts{}, false)
	if model.Headline != "No test events received." {
		t.Fatal(model.Headline)
	}
}

func TestReportSurfacesConfigurationErrorsDespiteReceivedTests(t *testing.T) {
	model := buildReport(t.TempDir(), intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{{Name: "fails"}}, ConfigurationErrors: []string{"skippable_tests"}}, false)
	if model.Headline != "Test events received." {
		t.Fatalf("headline overstates verification: %s", model.Headline)
	}
	if !strings.Contains(model.Summary, "Tracer configuration errors: skippable_tests.") {
		t.Fatalf("missing configuration error: %s", model.Summary)
	}
}

func TestReportSurfacesEmptyCoverageAsTracerError(t *testing.T) {
	path, err := writeReport(t.TempDir(), t.TempDir(), intake.Facts{TestEventCount: 1, TestCount: 1, EmptyCoverageEntryCount: 2}, false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Tracer error: empty coverage entries", "2 coverage entries had an empty files list", "Affected payloads were excluded", "Inspect the captured traffic"} {
		if !strings.Contains(string(data), expected) {
			t.Errorf("missing %q in report", expected)
		}
	}
	if strings.Contains(string(data), "No findings.") {
		t.Fatal("report hides tracer error as no findings")
	}
}

func TestAbsoluteFileURLHandlesWindowsPaths(t *testing.T) {
	for path, want := range map[string]string{
		`C:\repo\report.html`:          "file:///C:/repo/report.html",
		`C:\project space\report.html`: "file:///C:/project%20space/report.html",
		`\\server\share\report.html`:   "file://server/share/report.html",
	} {
		if got := absoluteFileURL(path); got != want {
			t.Errorf("absoluteFileURL(%q) = %q, want %q", path, got, want)
		}
	}
}
