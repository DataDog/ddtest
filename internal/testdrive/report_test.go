package testdrive

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/DataDog/ddtest/internal/testdrive/intake"
)

func TestReportCountsIndividualFindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts intake.Facts
		want  string
	}{
		{"single", intake.Facts{TestEventCount: 1, FailedTests: []intake.Test{{Name: "failed"}}}, "1 finding."},
		{"multiple in one card", intake.Facts{TestEventCount: 2, FailedTests: []intake.Test{{Name: "one"}, {Name: "two"}}}, "2 findings."},
		{"empty coverage without events", intake.Facts{EmptyCoverageEntryCount: 2}, "2 findings."},
		{"configuration error without events", intake.Facts{ConfigurationErrors: []string{"skippable_tests"}}, "1 finding."},
		{"combined", intake.Facts{
			TestEventCount: 2, EmptyCoverageEntryCount: 2, ConfigurationErrors: []string{"skippable_tests"},
			FailedTests: []intake.Test{{Name: "failed"}}, FlakyTests: []intake.Test{{Name: "flaky"}},
			SlowTests: []intake.Test{{Name: "slow"}}, BroadCoverage: []intake.CoverageFact{{Name: "broad"}},
		}, "7 findings."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := buildReport(t.TempDir(), tc.facts, false)
			if !strings.HasPrefix(model.Summary, tc.want) {
				t.Fatalf("summary = %q, want prefix %q", model.Summary, tc.want)
			}
			var console bytes.Buffer
			writeFindings(&console, tc.facts)
			if !strings.Contains(console.String(), tc.want+"\n") {
				t.Fatalf("report and terminal counts differ: report %q, terminal %q", model.Summary, console.String())
			}
			if len(tc.facts.ConfigurationErrors) > 0 && !strings.Contains(model.Summary, "Tracer configuration errors: skippable_tests.") {
				t.Fatalf("summary hides configuration errors: %q", model.Summary)
			}
		})
	}
}

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

func TestReportRuntimeFacts(t *testing.T) {
	for _, tc := range []struct {
		name          string
		events        int
		commandFailed bool
		tracer        string
		wantStatus    string
	}{
		{"passing with reused tracer", 1, false, "dd-trace@6.15.0 · reused", "Passed"},
		{"failing with isolated tracer", 1, true, "dd-trace@6.15.0 · isolated", "Failed"},
		{"successful command without events", 0, false, "dd-trace@6.15.0 · reused", "No test results received"},
		{"failed command without events", 0, true, "dd-trace@6.15.0 · isolated", "No test results received"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := buildReport(t.TempDir(), intake.Facts{TestEventCount: tc.events}, tc.commandFailed, reportRuntime{Framework: "Jest", Tracer: tc.tracer})
			facts := make(map[string]string)
			for _, fact := range model.Facts {
				facts[fact.Label] = fact.Value
			}
			if facts["Jest"] != tc.wantStatus || facts["Tracer"] != tc.tracer {
				t.Fatalf("incorrect runtime facts: %v", facts)
			}
			if tc.events == 0 && (model.Headline != "No test events received." || strings.Contains(model.Summary, "No findings.")) {
				t.Fatalf("report implies successful instrumentation without events: %+v", model)
			}
		})
	}
}

func TestAbsoluteFileURL(t *testing.T) {
	for path, want := range map[string]string{
		`C:\repo\report.html`:                "file:///C:/repo/report.html",
		`C:\project space\report.html`:       "file:///C:/project%20space/report.html",
		`\\server\share\report.html`:         "file://server/share/report.html",
		`\\server\share space\report#1.html`: "file://server/share%20space/report%231.html",
		`/repo space/report#1.html`:          "file:///repo%20space/report%231.html",
		`/repo\name/report.html`:             "file:///repo%5Cname/report.html",
	} {
		if got := absoluteFileURL(path); got != want {
			t.Errorf("absoluteFileURL(%q) = %q, want %q", path, got, want)
		}
	}
}
