package planner

import (
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
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
		DDTestSettings: &settings.Config{
			Platform:               "ruby",
			Framework:              "rspec",
			MinParallelism:         2,
			MaxParallelism:         8,
			ParallelRunnerOverhead: 25 * time.Second,
			WorkerEnv:              "RAILS_ENV=test;DATABASE_PASSWORD=secret",
			CiNode:                 -1,
			CiNodeWorkers:          2,
			Command:                "bundle exec rspec",
			TestsLocation:          "spec/**/*_spec.rb",
			RuntimeTags:            `{"runtime.version":"3.3.4"}`,
			ReportEnabled:          true,
		},
		DatadogSettings: datadogSettingsReport{
			Available:            true,
			TestImpactAnalysis:   true,
			TestSkipping:         true,
			TestImpactCollection: false,
			KnownTests:           true,
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
		LongSeparateRunnerSuites: []testSuiteTimingReport{
			{
				Runner:            0,
				Module:            "rspec",
				Suite:             "Checkout::Slow",
				SourceFile:        "spec/slow_spec.rb",
				TotalDuration:     2 * time.Minute,
				EstimatedDuration: 100 * time.Second,
				DurationSource:    testFileDurationSourceKnown,
			},
		},
		SlowestTestSuitesOverall: []testSuiteTimingReport{
			{
				Module:            "rspec",
				Suite:             "Checkout::VerySlow",
				SourceFile:        "spec/very_slow_spec.rb",
				TotalDuration:     3 * time.Minute,
				EstimatedDuration: 3 * time.Minute,
				DurationSource:    testFileDurationSourceKnown,
			},
			{
				Module:            "rspec",
				Suite:             "Checkout::Slow",
				SourceFile:        "spec/slow_spec.rb",
				TotalDuration:     2 * time.Minute,
				EstimatedDuration: 100 * time.Second,
				DurationSource:    testFileDurationSourceKnown,
			},
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

DDTest settings
  Platform: ruby
  Framework: rspec
  Min parallelism: 2
  Max parallelism: 8
  CI job overhead: 25s
  Worker env: DATABASE_PASSWORD, RAILS_ENV
  CI node: -1
  CI node workers: 2
  Command: bundle exec rspec
  Tests location: spec/**/*_spec.rb
  Runtime tags: {"runtime.version":"3.3.4"}
  Report enabled: true

Datadog settings
  Test Impact Analysis: enabled
    Test skipping: enabled
    Test impact collection: disabled
  Known tests: enabled
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

Slow suites on dedicated runners
  ATTENTION: 1 dedicated runner
  1. runner 0, rspec / Checkout::Slow (spec/slow_spec.rb): historical duration 2m0s, estimated runtime 1m40s

10 slowest test suites overall
  1. rspec / Checkout::VerySlow (spec/very_slow_spec.rb): historical duration 3m0s, estimated runtime 3m0s
  2. rspec / Checkout::Slow (spec/slow_spec.rb): historical duration 2m0s, estimated runtime 1m40s
`
	if output.String() != expected {
		t.Errorf("unexpected plan report:\n%s", output.String())
	}
}

func TestPrintPlanReport_MissingSettingsAndData(t *testing.T) {
	var output strings.Builder

	printPlanReport(&output, planReport{})

	report := output.String()
	if !strings.Contains(report, "DDTest settings\n  Settings: not available") {
		t.Errorf("expected missing ddtest settings message, got:\n%s", report)
	}
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
	known := newKnownTestsReport(&api.KnownTestsResponseData{
		Tests: api.KnownTestsResponseDataModules{
			"module-a": api.KnownTestsResponseDataSuites{
				"suite-a": []string{"test-a", "test-b"},
			},
			"module-b": api.KnownTestsResponseDataSuites{
				"suite-b": []string{"test-c"},
				"suite-c": []string{"test-d", "test-e"},
			},
		},
	})
	if known.Modules != 2 || known.Suites != 3 || known.Tests != 5 {
		t.Errorf("unexpected known test summary: %+v", known)
	}

	managed := newManagedFlakyTestsReport(&api.TestManagementTestsResponseDataModules{
		Modules: map[string]api.TestManagementTestsResponseDataSuites{
			"module-a": {
				Suites: map[string]api.TestManagementTestsResponseDataTests{
					"suite-a": {
						Tests: map[string]api.TestManagementTestsResponseDataTestProperties{
							"test-a": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Quarantined: true}},
							"test-b": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{Disabled: true}},
							"test-c": {Properties: api.TestManagementTestsResponseDataTestPropertiesAttributes{AttemptToFix: true}},
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

func TestFormatWorkerEnvKeys(t *testing.T) {
	report := formatWorkerEnvKeys("TOKEN=secret; RAILS_ENV = test ;BAD_NO_EQUALS;=NO_KEY;TOKEN=other")

	if report != "RAILS_ENV, TOKEN" {
		t.Fatalf("unexpected worker env report: %s", report)
	}
	if strings.Contains(report, "secret") || strings.Contains(report, "test") || strings.Contains(report, "other") {
		t.Fatalf("worker env report leaked values: %s", report)
	}
}

func TestTestPlanner_LongSeparateRunnerSuitesReport(t *testing.T) {
	planner := newTestPlannerWithDefaults()
	planner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "SlowSuite"}: {
			Module:            "rspec",
			Suite:             "SlowSuite",
			SourceFile:        "spec/slow_spec.rb",
			TotalDuration:     float64(2 * time.Minute),
			EstimatedDuration: float64(2 * time.Minute),
			DurationSource:    testFileDurationSourceKnown,
		},
		{Module: "rspec", Suite: "ShortSuiteInSlowFile"}: {
			Module:            "rspec",
			Suite:             "ShortSuiteInSlowFile",
			SourceFile:        "spec/slow_spec.rb",
			TotalDuration:     float64(time.Second),
			EstimatedDuration: float64(time.Second),
			DurationSource:    testFileDurationSourceKnown,
		},
		{Module: "rspec", Suite: "MediumSuite"}: {
			Module:            "rspec",
			Suite:             "MediumSuite",
			SourceFile:        "spec/medium_spec.rb",
			TotalDuration:     float64(30 * time.Second),
			EstimatedDuration: float64(30 * time.Second),
			DurationSource:    testFileDurationSourceKnown,
		},
		{Module: "rspec", Suite: "FastSuite"}: {
			Module:            "rspec",
			Suite:             "FastSuite",
			SourceFile:        "spec/fast_spec.rb",
			TotalDuration:     float64(time.Second),
			EstimatedDuration: float64(time.Second),
			DurationSource:    testFileDurationSourceDefault,
		},
	}
	planner.suitesBySourceFile = indexSuitesBySourceFile(planner.suiteAggregates)
	planner.testFileWeights = map[string]int{
		"spec/slow_spec.rb":   int((121 * time.Second) / time.Millisecond),
		"spec/medium_spec.rb": int((30 * time.Second) / time.Millisecond),
		"spec/fast_spec.rb":   int(time.Second / time.Millisecond),
	}

	report := planner.longSeparateRunnerSuitesReport(2, splitScore{
		parallelRunners: 2,
		totalRuntime:    int((152 * time.Second) / time.Millisecond),
	})

	if len(report) != 1 {
		t.Fatalf("expected one suite scheduled in a separate runner, got %+v", report)
	}
	if report[0].Runner != 0 ||
		report[0].Module != "rspec" ||
		report[0].Suite != "SlowSuite" ||
		report[0].SourceFile != "spec/slow_spec.rb" ||
		report[0].EstimatedDuration != 2*time.Minute {
		t.Fatalf("unexpected separate runner suite report: %+v", report[0])
	}
}

func TestTestPlanner_LongSeparateRunnerSuitesReport_SkipsSingletonRunnersThatAreNotLong(t *testing.T) {
	planner := newTestPlannerWithDefaults()
	planner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{
		{Module: "rspec", Suite: "SuiteA"}: {
			Module:            "rspec",
			Suite:             "SuiteA",
			SourceFile:        "spec/a_spec.rb",
			TotalDuration:     float64(10 * time.Second),
			EstimatedDuration: float64(10 * time.Second),
			DurationSource:    testFileDurationSourceKnown,
		},
		{Module: "rspec", Suite: "SuiteB"}: {
			Module:            "rspec",
			Suite:             "SuiteB",
			SourceFile:        "spec/b_spec.rb",
			TotalDuration:     float64(10 * time.Second),
			EstimatedDuration: float64(10 * time.Second),
			DurationSource:    testFileDurationSourceKnown,
		},
		{Module: "rspec", Suite: "SuiteC"}: {
			Module:            "rspec",
			Suite:             "SuiteC",
			SourceFile:        "spec/c_spec.rb",
			TotalDuration:     float64(10 * time.Second),
			EstimatedDuration: float64(10 * time.Second),
			DurationSource:    testFileDurationSourceKnown,
		},
	}
	planner.suitesBySourceFile = indexSuitesBySourceFile(planner.suiteAggregates)
	planner.testFileWeights = map[string]int{
		"spec/a_spec.rb": int((10 * time.Second) / time.Millisecond),
		"spec/b_spec.rb": int((10 * time.Second) / time.Millisecond),
		"spec/c_spec.rb": int((10 * time.Second) / time.Millisecond),
	}

	report := planner.longSeparateRunnerSuitesReport(3, splitScore{
		parallelRunners: 3,
		totalRuntime:    int((30 * time.Second) / time.Millisecond),
	})

	if len(report) != 0 {
		t.Fatalf("expected equal singleton runners not to be reported as long, got %+v", report)
	}
}

func TestTestPlanner_SlowestTestSuitesOverallReport(t *testing.T) {
	planner := newTestPlannerWithDefaults()
	planner.suiteAggregates = map[testSuiteKey]testSuiteAggregate{}
	for i := range 12 {
		suite := "Suite" + string(rune('A'+i))
		planner.suiteAggregates[testSuiteKey{Module: "rspec", Suite: suite}] = testSuiteAggregate{
			Module:            "rspec",
			Suite:             suite,
			SourceFile:        "spec/" + suite + "_spec.rb",
			TotalDuration:     float64(time.Duration(i+1) * time.Second),
			EstimatedDuration: float64(time.Duration(i+1) * time.Second),
			DurationSource:    testFileDurationSourceKnown,
		}
	}

	report := planner.slowestTestSuitesOverallReport(10)
	if len(report) != 10 {
		t.Fatalf("expected 10 slowest suites, got %d", len(report))
	}
	if report[0].Suite != "SuiteL" || report[0].TotalDuration != 12*time.Second {
		t.Fatalf("expected slowest suite first, got %+v", report[0])
	}
	if report[9].Suite != "SuiteC" || report[9].TotalDuration != 3*time.Second {
		t.Fatalf("expected 10th slowest suite to be SuiteC, got %+v", report[9])
	}
}
