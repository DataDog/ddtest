package planner

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

func TestPrintPlanReport_AllData(t *testing.T) {
	var output strings.Builder
	minParallelism := settings.DefaultParallelism() + 1
	maxParallelism := settings.DefaultParallelism() + 2

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
			Platform:               "python",
			Framework:              "pytest",
			MinParallelism:         minParallelism,
			MaxParallelism:         maxParallelism,
			ParallelRunnerOverhead: 30 * time.Second,
			WorkerEnv:              "RAILS_ENV=test;DATABASE_PASSWORD=secret",
			CiNode:                 0,
			CiNodeWorkers:          2,
			Command:                "pytest -q",
			TestsLocation:          "spec/**/*_spec.rb",
			TestsExcludePattern:    "spec/system/**/*_spec.rb",
			TestDiscoveryCache:     ".ddtest-cache/tests.json",
			TestSkippingLevel:      settings.TestSkippingLevelSuite,
			ForceFullTestDiscovery: true,
			StrictDiscovery:        true,
			RuntimeTags:            `{"runtime.version":"3.3.4"}`,
			ReportEnabled:          false,
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
		Skippables: skippablesReport{
			Available:         true,
			TestSkippingLevel: settings.TestSkippingLevelSuite,
			TIASuites:         312,
			DisabledTests:     3,
		},
		ManagedFlakyTests: managedFlakyTestsReport{
			Available:    true,
			Total:        26,
			Quarantined:  8,
			Disabled:     3,
			AttemptToFix: 5,
		},
		TestSuiteDurations: testSuiteDurationsReport{
			Available: true,
			Modules:   3,
			Suites:    1491,
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

	expected := fmt.Sprintf(`+++ DDTest: plan report

Run
  Service: checkout-api
  Repository: https://github.com/acme/checkout.git
  Commit: 9f3a1c7d2b4e
  Branch: feature/split-report
  Platform: ruby / rspec
  OS tags: os.platform=linux, os.architecture=amd64, os.version=6.8.0
  Runtime tags: runtime.name=ruby, runtime.version=3.3.4

DDTest settings
  Platform: python
  Framework: pytest
  Min parallelism: %s
  Max parallelism: %s
  CI job overhead: 30s
  Worker env: DATABASE_PASSWORD, RAILS_ENV
  CI node: 0
  CI node workers: 2
  Command: pytest -q
  Tests location: spec/**/*_spec.rb
  Tests exclude pattern: spec/system/**/*_spec.rb
  Test discovery cache: .ddtest-cache/tests.json
  Test skipping mode: suite
  Force full test discovery: true
  Strict discovery: true
  Runtime tags: {"runtime.version":"3.3.4"}
  Report enabled: false

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
  TIA skippables returned: 312 suites
  Managed flaky tests: 26 total, 8 quarantined, 3 disabled, 5 attempt-to-fix
  Test suite durations: 3 modules, 1,491 suites

Planning
  Test files discovered: 642
  Fully skipped files: 118
  Test Management disabled tests applied: 3
  Test files to run: 524
  Duration source: 431 known, 90 default
  Estimated time saved: 38.40%%

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
`, formatCount(minParallelism), formatCount(maxParallelism))
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
	if !strings.Contains(report, "  TIA skippables returned: not available") {
		t.Errorf("expected missing TIA skippables message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Test Management disabled tests applied: not available") {
		t.Errorf("expected missing disabled tests message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Managed flaky tests: not available") {
		t.Errorf("expected missing managed flaky tests message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Test suite durations: not available") {
		t.Errorf("expected missing test suite durations message, got:\n%s", report)
	}
}

func TestPrintPlanReport_DefaultSettings(t *testing.T) {
	var output strings.Builder
	defaults := settings.DefaultConfig()

	printPlanReport(&output, planReport{
		DDTestSettings: &defaults,
	})

	report := output.String()
	if !strings.Contains(report, "DDTest settings\n  Settings: defaults") {
		t.Errorf("expected default ddtest settings message, got:\n%s", report)
	}
}

func TestPrintDDTestSettingsReport_AllSupportedSettings(t *testing.T) {
	config := settings.DefaultConfig()
	config.Platform = "python"
	config.Framework = "pytest"
	config.Command = "pytest -q"
	config.MinParallelism++
	config.MaxParallelism += 2
	config.ParallelRunnerOverhead += time.Second
	config.CiNode = 0
	config.CiNodeWorkers = 2
	config.WorkerEnv = "TOKEN=secret"
	config.TestsLocation = "tests/**/*_test.py"
	config.TestsExcludePattern = "tests/system/**/*_test.py"
	config.TestDiscoveryCache = ".ddtest-cache/tests.json"
	config.TestSkippingLevel = settings.TestSkippingLevelSuite
	config.ForceFullTestDiscovery = true
	config.StrictDiscovery = true
	config.RuntimeTags = `{"runtime.version":"3.3.4"}`
	config.ReportEnabled = false

	var output strings.Builder
	printDDTestSettingsReport(&output, &config)

	names := make([]string, 0)
	for _, line := range strings.Split(output.String(), "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		name, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if ok {
			names = append(names, name)
		}
	}
	if len(names) != reflect.TypeOf(settings.Config{}).NumField() {
		t.Fatalf("expected every supported setting to be reported, got %d names from:\n%s", len(names), output.String())
	}

	expectedNames := []string{
		"Platform",
		"Framework",
		"Min parallelism",
		"Max parallelism",
		"CI job overhead",
		"Worker env",
		"CI node",
		"CI node workers",
		"Command",
		"Tests location",
		"Tests exclude pattern",
		"Test discovery cache",
		"Test skipping mode",
		"Force full test discovery",
		"Strict discovery",
		"Runtime tags",
		"Report enabled",
	}
	if !reflect.DeepEqual(names, expectedNames) {
		t.Fatalf("unexpected changed setting names:\ngot:  %v\nwant: %v", names, expectedNames)
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
	if !strings.Contains(report, "  TIA skippables returned: disabled") {
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

	durations := newTestSuiteDurationsReport(&api.TestSuiteDurationsResponseData{
		TestSuites: map[string]map[string]api.TestSuiteDurationInfo{
			"module-a": {
				"suite-a": {},
				"suite-b": {},
			},
			"module-b": {
				"suite-c": {},
			},
		},
	})
	if durations.Modules != 2 || durations.Suites != 3 {
		t.Errorf("unexpected test suite durations summary: %+v", durations)
	}
}

func TestFormatTIASkippablesReportsOnlyActiveCount(t *testing.T) {
	settingsReport := datadogSettingsReport{Available: true, TestSkipping: true}

	testLevel := formatTIASkippables(settingsReport, skippablesReport{
		Available:         true,
		TestSkippingLevel: settings.TestSkippingLevelTest,
		TIATests:          123,
		TIASuites:         456,
	})
	if testLevel != "123 tests" {
		t.Fatalf("unexpected test-level TIA skippables report: %s", testLevel)
	}

	suiteLevel := formatTIASkippables(settingsReport, skippablesReport{
		Available:         true,
		TestSkippingLevel: settings.TestSkippingLevelSuite,
		TIATests:          123,
		TIASuites:         456,
	})
	if suiteLevel != "456 suites" {
		t.Fatalf("unexpected suite-level TIA skippables report: %s", suiteLevel)
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

func TestAverageRunnerRuntimeDuration(t *testing.T) {
	tests := []struct {
		name  string
		split splitScore
		want  time.Duration
	}{
		{name: "no runners", split: splitScore{parallelRunners: 0, totalRuntime: 1000}, want: 0},
		{name: "no runtime", split: splitScore{parallelRunners: 2, totalRuntime: 0}, want: 0},
		{name: "average", split: splitScore{parallelRunners: 4, totalRuntime: 10_000}, want: 2500 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := averageRunnerRuntimeDuration(tt.split); got != tt.want {
				t.Fatalf("averageRunnerRuntimeDuration() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCompareSeparateRunnerSuiteTiming(t *testing.T) {
	suite := func(runner int, sourceFile string, estimated, total time.Duration) testSuiteTimingReport {
		return testSuiteTimingReport{
			Runner:            runner,
			Module:            "rspec",
			Suite:             "Suite",
			SourceFile:        sourceFile,
			EstimatedDuration: estimated,
			TotalDuration:     total,
		}
	}

	tests := []struct {
		name string
		a    testSuiteTimingReport
		b    testSuiteTimingReport
		want int
	}{
		{name: "runner ascending", a: suite(0, "spec/a_spec.rb", time.Second, time.Second), b: suite(1, "spec/a_spec.rb", time.Second, time.Second), want: -1},
		{name: "runner descending", a: suite(1, "spec/a_spec.rb", time.Second, time.Second), b: suite(0, "spec/a_spec.rb", time.Second, time.Second), want: 1},
		{name: "estimated duration descending", a: suite(0, "spec/a_spec.rb", 2*time.Second, time.Second), b: suite(0, "spec/a_spec.rb", time.Second, time.Second), want: -1},
		{name: "total duration descending", a: suite(0, "spec/a_spec.rb", time.Second, 2*time.Second), b: suite(0, "spec/a_spec.rb", time.Second, time.Second), want: -1},
		{name: "source file tie breaker", a: suite(0, "spec/a_spec.rb", time.Second, time.Second), b: suite(0, "spec/b_spec.rb", time.Second, time.Second), want: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareSeparateRunnerSuiteTiming(tt.a, tt.b); got != tt.want {
				t.Fatalf("compareSeparateRunnerSuiteTiming() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCompareSuiteIdentity(t *testing.T) {
	base := testSuiteTimingReport{
		Module:     "rspec",
		Suite:      "CheckoutSuite",
		SourceFile: "spec/checkout_spec.rb",
	}

	tests := []struct {
		name string
		a    testSuiteTimingReport
		b    testSuiteTimingReport
		want int
	}{
		{name: "source file before", a: testSuiteTimingReport{SourceFile: "spec/a_spec.rb", Module: base.Module, Suite: base.Suite}, b: base, want: -1},
		{name: "source file after", a: testSuiteTimingReport{SourceFile: "spec/z_spec.rb", Module: base.Module, Suite: base.Suite}, b: base, want: 1},
		{name: "module before", a: testSuiteTimingReport{SourceFile: base.SourceFile, Module: "minitest", Suite: base.Suite}, b: base, want: -1},
		{name: "module after", a: testSuiteTimingReport{SourceFile: base.SourceFile, Module: "testunit", Suite: base.Suite}, b: base, want: 1},
		{name: "suite before", a: testSuiteTimingReport{SourceFile: base.SourceFile, Module: base.Module, Suite: "CartSuite"}, b: base, want: -1},
		{name: "suite after", a: testSuiteTimingReport{SourceFile: base.SourceFile, Module: base.Module, Suite: "OrderSuite"}, b: base, want: 1},
		{name: "equal", a: base, b: base, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareSuiteIdentity(tt.a, tt.b); got != tt.want {
				t.Fatalf("compareSuiteIdentity() = %d, want %d", got, tt.want)
			}
		})
	}
}
