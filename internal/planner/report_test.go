package planner

import (
	"strings"
	"testing"

	"github.com/DataDog/ddtest/civisibility/utils/net"
	"github.com/DataDog/ddtest/internal/runmetadata"
)

func TestPrintPlanReport_AllData(t *testing.T) {
	var output strings.Builder

	printPlanReport(&output, planReport{
		RunInfo: runmetadata.RunInfo{
			Service:    "checkout-api",
			Repository: "https://github.com/acme/checkout.git",
			Commit:     "9f3a1c7d2b4e",
			Branch:     "feature/split-report",
		},
		PlanInfo: PlanInfo{
			Platform:  "ruby",
			Framework: "rspec",
			OSTags: map[string]string{
				"os.platform":     "linux",
				"os.architecture": "amd64",
				"os.version":      "6.8.0",
			},
			RuntimeTags: map[string]string{
				"runtime.name":    "ruby",
				"runtime.version": "3.3.4",
			},
		},
		DatadogSettings: datadogSettingsReport{
			Available:            true,
			TestImpactAnalysis:   true,
			TestSkipping:         true,
			TestImpactCollection: false,
			KnownTests:           true,
			ImpactedTests:        false,
			EarlyFlakeDetection:  true,
			AutoTestRetries:      true,
			FlakyTestManagement:  true,
		},
		KnownTests: knownTestsReport{
			Available: true,
			Modules:   4,
			Suites:    1284,
			Tests:     18921,
		},
		SkippableTestsCount: 312,
		ManagedFlakyTests: managedFlakyTestsReport{
			Available:    true,
			Total:        26,
			Quarantined:  8,
			Disabled:     3,
			AttemptToFix: 5,
		},
		Planning: planningReport{
			TestFilesDiscovered: 642,
			FullySkippedFiles:   118,
			TestFilesToRun:      524,
			DurationSources: durationSourceReport{
				Known:   431,
				Default: 90,
			},
			EstimatedTimeSaved: 38.4,
		},
		Split: splitScore{
			parallelRunners: 6,
			wallTime:        252000,
			imbalance:       11000,
			totalRuntime:    1426000,
		},
	})

	expected := `+++ DDTest: plan report

Run
  Service: checkout-api
  Repository: https://github.com/acme/checkout.git
  Commit: 9f3a1c7d2b4e
  Branch: feature/split-report
  Platform: ruby / rspec
  OS tags: os.platform=linux, os.architecture=amd64, os.version=6.8.0
  Runtime tags: runtime.name=ruby, runtime.version=3.3.4

Datadog
  Test Impact Analysis: enabled
    Test skipping: enabled
    Test impact collection: disabled
  Known tests: enabled
  Impacted tests: disabled
  Early flake detection: enabled
  Auto test retries: enabled
  Flaky test management: enabled

Backend data
  Known tests: 4 modules, 1,284 suites, 18,921 tests
  Skippable tests for this run: 312
  Managed flaky tests: 26 total, 8 quarantined, 3 disabled, 5 attempt-to-fix

Planning
  Test files discovered: 642
  Fully skipped files: 118
  Test files to run: 524
  Duration source: 431 known, 90 default
  Estimated time saved: 38.40%

Split
  Runners: 6
  Expected wall time: 4m12s
  Imbalance: 11s
  Total estimated runtime: 23m46s
`
	if output.String() != expected {
		t.Errorf("unexpected plan report:\n%s", output.String())
	}
}

func TestPrintPlanReport_MissingSettingsAndData(t *testing.T) {
	var output strings.Builder

	printPlanReport(&output, planReport{})

	report := output.String()
	if !strings.Contains(report, "  Settings: not available") {
		t.Errorf("expected missing settings message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Known tests: not available") {
		t.Errorf("expected missing known tests message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Managed flaky tests: not available") {
		t.Errorf("expected missing managed flaky tests message, got:\n%s", report)
	}
}

func TestPrintPlanReport_DisabledFeatures(t *testing.T) {
	var output strings.Builder

	printPlanReport(&output, planReport{
		DatadogSettings: datadogSettingsReport{
			Available: true,
		},
	})

	report := output.String()
	if !strings.Contains(report, "  Known tests: disabled") {
		t.Errorf("expected disabled known tests, got:\n%s", report)
	}
	if !strings.Contains(report, "  Skippable tests for this run: disabled") {
		t.Errorf("expected disabled skippable tests, got:\n%s", report)
	}
	if !strings.Contains(report, "  Managed flaky tests: disabled") {
		t.Errorf("expected disabled managed flaky tests, got:\n%s", report)
	}
}

func TestReportSummaries(t *testing.T) {
	known := newKnownTestsReport(&net.KnownTestsResponseData{
		Tests: net.KnownTestsResponseDataModules{
			"module-a": net.KnownTestsResponseDataSuites{
				"suite-a": []string{"test-a", "test-b"},
			},
			"module-b": net.KnownTestsResponseDataSuites{
				"suite-b": []string{"test-c"},
				"suite-c": []string{"test-d", "test-e"},
			},
		},
	})
	if known.Modules != 2 || known.Suites != 3 || known.Tests != 5 {
		t.Errorf("unexpected known test summary: %+v", known)
	}

	managed := newManagedFlakyTestsReport(&net.TestManagementTestsResponseDataModules{
		Modules: map[string]net.TestManagementTestsResponseDataSuites{
			"module-a": {
				Suites: map[string]net.TestManagementTestsResponseDataTests{
					"suite-a": {
						Tests: map[string]net.TestManagementTestsResponseDataTestProperties{
							"test-a": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
							"test-b": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							"test-c": {Properties: net.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
						},
					},
				},
			},
		},
	})
	if managed.Total != 3 || managed.Quarantined != 1 || managed.Disabled != 1 || managed.AttemptToFix != 1 {
		t.Errorf("unexpected managed flaky test summary: %+v", managed)
	}
}
