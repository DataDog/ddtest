package planner

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
	"github.com/DataDog/ddtest/internal/testoptimization"
	"github.com/DataDog/ddtest/internal/testoptimization/api"
)

func TestPrintPlanReport_AllData(t *testing.T) {
	var output strings.Builder
	minParallelism := settings.DefaultParallelism() + 1
	maxParallelism := settings.DefaultParallelism() + 2

	printPlanReportData(&output, PlanReportData{
		RunInfo: runmetadata.RunInfo{
			Service:    "checkout-api",
			Repository: "https://github.com/acme/checkout.git",
			Commit:     "9f3a1c7d2b4e",
			Branch:     "feature/split-report",
		},
		PlanMetadata: PlanMetadata{
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
			TargetTime:             5 * time.Minute,
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
			FetchDuration:        240 * time.Millisecond,
			TestImpactAnalysis:   true,
			TestSkipping:         true,
			TestImpactCollection: false,
			KnownTests:           true,
			EarlyFlakeDetection:  true,
			AutoTestRetries:      true,
			FlakyTestManagement:  true,
		},
		KnownTests: knownTestsReport{
			Available:     true,
			FetchDuration: 80 * time.Millisecond,
			Modules:       4,
			Suites:        1284,
			Tests:         18921,
		},
		Skippables: skippablesReport{
			Available:         true,
			FetchDuration:     110 * time.Millisecond,
			TestSkippingLevel: settings.TestSkippingLevelSuite,
			TIASuites:         312,
		},
		ManagedFlakyTests: managedFlakyTestsReport{
			Available:     true,
			FetchDuration: 90 * time.Millisecond,
			Total:         26,
			Quarantined:   8,
			Disabled:      3,
			AttemptToFix:  5,
		},
		TestSuiteDurations: testSuiteDurationsReport{
			Available:     true,
			FetchDuration: 140 * time.Millisecond,
			Modules:       3,
			Suites:        1491,
		},
		Planning: planningReport{
			Discovery: discoveryReport{
				Available: true,
				Mode:      discoveryModeFull,
				Cache: discoveryCacheResult{
					Configured: true,
					Used:       true,
				},
				Duration:  3 * time.Second,
				TestFiles: 642,
				Suites:    1284,
				Tests:     18921,
			},
			Durations: durationApplicationReport{
				Available:               true,
				BackendDurationsApplied: 431,
				BackendSuitesAdded:      12,
				SuitesWithoutDurations:  90,
				FilesWithoutDurations:   90,
				ExpectedFullDuration:    37*time.Minute + 12*time.Second,
			},
			Skipping: skippingApplicationReport{
				Available:                     true,
				TIASuites:                     312,
				DisabledTests:                 3,
				UnskippableMarkerSuitesForced: 5,
				FullySkippedFiles:             118,
			},
			TestFilesToRun:     524,
			EstimatedTimeSaved: 38.4,
		},
		Split: splitScore{
			parallelRunners: 6,
			wallTime:        252000,
			imbalance:       11000,
			totalRuntime:    1426000,
		},
		SplitSelection: splitSelection{
			selected: splitScore{
				parallelRunners: 6,
				wallTime:        252000,
				imbalance:       11000,
				totalRuntime:    1426000,
			},
			bestWithoutTarget: splitScore{
				parallelRunners: 4,
				wallTime:        305000,
				imbalance:       20000,
				totalRuntime:    1426000,
			},
			candidates: []splitScore{
				{
					parallelRunners: 1,
					wallTime:        1426000,
					imbalance:       0,
					totalRuntime:    1426000,
				},
				{
					parallelRunners: 4,
					wallTime:        305000,
					imbalance:       20000,
					totalRuntime:    1426000,
				},
				{
					parallelRunners: 5,
					wallTime:        290000,
					imbalance:       15000,
					totalRuntime:    1426000,
				},
				{
					parallelRunners: 6,
					wallTime:        252000,
					imbalance:       11000,
					totalRuntime:    1426000,
				},
			},
			parallelRunnerOverhead: 30 * time.Second,
			targetTime:             5 * time.Minute,
			available:              true,
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
  Target time: 5m0s
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
  Fetch duration: 240ms
  Test Impact Analysis: enabled
    Test skipping: enabled
    Test impact collection: disabled
  Known tests: enabled
  Early flake detection: enabled
  Auto test retries: enabled
  Flaky test management: enabled

Backend data
  Known tests: 4 modules, 1,284 suites, 18,921 tests (fetched in 80ms)
  TIA skippables returned: 312 suites (fetched in 110ms)
  Managed flaky tests: 26 total, 8 quarantined, 3 disabled, 5 attempt-to-fix (fetched in 90ms)
  Test suite durations: 3 modules, 1,491 suites (fetched in 140ms)

Planning
  Discovery
    Method: full
    Test files: 642
    Cache: used
    Duration: 3s
    Suites discovered: 1,284
    Tests discovered: 18,921
  Duration estimates
    Backend durations used: 431 suites
    Default durations used: 90 suites
    Backend-only suites added: 12
  Skipping
    TIA skippables applied: 312 suites
    Disabled tests applied: 3 tests
    Suites marked unskippable: 5
    Files fully skipped: 118
  Run set
    Test files to run: 524
    Estimated time saved: 38.40%%
  Split selection
    Full runtime: 37m12s
    Selected: 6 runners
    Reason: lowest selection score among splits that meet target time
    Target time: 5m0s, satisfied
    Estimated test runtime: 23m46s
    Expected wall time: 4m12s
    Modeled CI overhead: 3m0s (6 runners x configured CI job overhead 30s)
    Selection score: 7m12s (wall time + modeled CI overhead)
    Imbalance: 11s

    Without target time: 4 runners (wall 5m5s, overhead 2m0s, score 7m5s)
      Selected vs without target: 53s faster wall time, 1m0s more CI overhead

    Candidates
      4 runners: wall 5m5s, overhead 2m0s, score 7m5s, missed target by 5s; would choose without target time
      6 runners: wall 4m12s, overhead 3m0s, score 7m12s, met target; selected
      5 runners: wall 4m50s, overhead 2m30s, score 7m20s, met target
      1 runner: wall 23m46s, overhead 30s, score 24m16s, missed target by 18m46s

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

func TestPrintPlanReport_FastDiscovery(t *testing.T) {
	var output strings.Builder

	printPlanReportData(&output, PlanReportData{
		RunInfo: runmetadata.RunInfo{
			Service: "checkout-api",
		},
		PlanMetadata: PlanMetadata{
			Platform: "ruby",
		},
		DatadogSettings: datadogSettingsReport{
			Available:            true,
			FetchDuration:        50 * time.Millisecond,
			TestImpactAnalysis:   true,
			TestSkipping:         true,
			TestImpactCollection: true,
			KnownTests:           true,
		},
		KnownTests: knownTestsReport{
			Available:     true,
			FetchDuration: 20 * time.Millisecond,
			Modules:       1,
			Suites:        10,
			Tests:         125,
		},
		Skippables: skippablesReport{
			Available:         true,
			FetchDuration:     30 * time.Millisecond,
			TestSkippingLevel: settings.TestSkippingLevelSuite,
			TIASuites:         8,
		},
		TestSuiteDurations: testSuiteDurationsReport{
			Available:     true,
			FetchDuration: 40 * time.Millisecond,
			Modules:       1,
			Suites:        12,
		},
		Planning: planningReport{
			Discovery: discoveryReport{
				Available: true,
				Mode:      discoveryModeFast,
				Duration:  120 * time.Millisecond,
				TestFiles: 24,
				Suites:    0,
			},
			Durations: durationApplicationReport{
				Available:               true,
				BackendDurationsApplied: 12,
				BackendSuitesAdded:      2,
				SuitesWithoutDurations:  1,
			},
			Skipping: skippingApplicationReport{
				Available:         true,
				TIASuites:         8,
				FullySkippedFiles: 6,
			},
			TestFilesToRun:     18,
			EstimatedTimeSaved: 25,
		},
		Split: splitScore{
			parallelRunners: 3,
			wallTime:        90_000,
			imbalance:       500,
			totalRuntime:    210_000,
		},
		SlowestTestSuitesOverall: []testSuiteTimingReport{
			{
				Module:            "rspec",
				Suite:             "Checkout::Order",
				SourceFile:        "spec/models/order_spec.rb",
				TotalDuration:     2*time.Minute + 30*time.Second,
				EstimatedDuration: 105 * time.Second,
				DurationSource:    testFileDurationSourceKnown,
			},
			{
				Module:            "rspec",
				Suite:             "Checkout::Payment",
				SourceFile:        "spec/models/payment_spec.rb",
				TotalDuration:     time.Minute,
				EstimatedDuration: 30 * time.Second,
				DurationSource:    testFileDurationSourceKnown,
			},
		},
	})

	expected := `+++ DDTest: plan report

Run
  Service: checkout-api
  Repository: not available
  Commit: not available
  Branch: not available
  Platform: ruby
  OS tags: not available
  Runtime tags: not available

DDTest settings
  Settings: not available

Datadog settings
  Fetch duration: 50ms
  Test Impact Analysis: enabled
    Test skipping: enabled
    Test impact collection: enabled
  Known tests: enabled
  Early flake detection: disabled
  Auto test retries: disabled
  Flaky test management: disabled

Backend data
  Known tests: 1 modules, 10 suites, 125 tests (fetched in 20ms)
  TIA skippables returned: 8 suites (fetched in 30ms)
  Managed flaky tests: disabled
  Test suite durations: 1 modules, 12 suites (fetched in 40ms)

Planning
  Discovery
    Method: fast
    Test files: 24
    Duration: 120ms
  Duration estimates
    Backend durations used: 12 suites
    Default durations used: 1 suite
    Backend-only suites added: 2
  Skipping
    TIA skippables applied: 8 suites
    Disabled tests applied: disabled
    Suites marked unskippable: 0
    Files fully skipped: 6
  Run set
    Test files to run: 18
    Estimated time saved: 25.00%
  Split selection
    Full runtime: not available
    Selected: 3 runners
    Estimated test runtime: 3m30s
    Expected wall time: 1m30s
    Imbalance: 500ms

Slow suites on dedicated runners
  None

10 slowest test suites overall
  1. rspec / Checkout::Order (spec/models/order_spec.rb): historical duration 2m30s, estimated runtime 1m45s
  2. rspec / Checkout::Payment (spec/models/payment_spec.rb): historical duration 1m0s, estimated runtime 30s
`
	if output.String() != expected {
		t.Errorf("unexpected fast discovery report:\n%s", output.String())
	}
}

func TestPrintPlanReport_MissingSettingsAndData(t *testing.T) {
	var output strings.Builder

	printPlanReportData(&output, PlanReportData{})

	report := output.String()
	if !strings.Contains(report, "DDTest settings\n  Settings: not available") {
		t.Errorf("expected missing ddtest settings message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Settings: not available") {
		t.Errorf("expected missing settings message, got:\n%s", report)
	}
	if !strings.Contains(report, "Planning\n  Discovery: not available") {
		t.Errorf("expected missing discovery message, got:\n%s", report)
	}
	if !strings.Contains(report, "Backend data\n  Known tests: not available") {
		t.Errorf("expected missing backend data message, got:\n%s", report)
	}
	if !strings.Contains(report, "  TIA skippables returned: not available") {
		t.Errorf("expected missing TIA skippables message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Managed flaky tests: not available") {
		t.Errorf("expected missing managed flaky tests message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Test suite durations: not available") {
		t.Errorf("expected missing test suite durations message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Duration estimates: not available") {
		t.Errorf("expected missing duration estimates message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Skipping: not available") {
		t.Errorf("expected missing skipping message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Run set: not available") {
		t.Errorf("expected missing run set message, got:\n%s", report)
	}
	if !strings.Contains(report, "  Split selection: not available") {
		t.Errorf("expected missing split selection message, got:\n%s", report)
	}
}

func TestPrintSplitCandidatesReport_LimitsAndSortsByScore(t *testing.T) {
	selection := splitSelection{
		selected:               splitScore{parallelRunners: 2, wallTime: 80_000, totalRuntime: 100_000},
		parallelRunnerOverhead: 10 * time.Second,
		available:              true,
		candidates: []splitScore{
			{parallelRunners: 6, wallTime: 59_000, totalRuntime: 100_000},
			{parallelRunners: 5, wallTime: 60_000, totalRuntime: 100_000},
			{parallelRunners: 1, wallTime: 100_000, totalRuntime: 100_000},
			{parallelRunners: 4, wallTime: 65_000, totalRuntime: 100_000},
			{parallelRunners: 3, wallTime: 70_000, totalRuntime: 100_000},
			{parallelRunners: 2, wallTime: 80_000, totalRuntime: 100_000},
		},
	}

	var output strings.Builder
	printSplitCandidatesReport(&output, selection)

	report := output.String()
	for _, want := range []string{
		"Candidates (best 5 of 6 by score)",
		"2 runners: wall 1m20s, overhead 20s, score 1m40s, selected",
		"3 runners: wall 1m10s, overhead 30s, score 1m40s",
		"4 runners: wall 1m5s, overhead 40s, score 1m45s",
		"1 runner: wall 1m40s, overhead 10s, score 1m50s",
		"5 runners: wall 1m0s, overhead 50s, score 1m50s",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("expected candidates report to contain %q, got:\n%s", want, report)
		}
	}
	if strings.Contains(report, "6 runners:") {
		t.Fatalf("expected candidates report to omit sixth candidate, got:\n%s", report)
	}
	orderedCandidates := []string{"2 runners:", "3 runners:", "4 runners:", "1 runner:", "5 runners:"}
	previousIndex := -1
	for _, candidate := range orderedCandidates {
		index := strings.Index(report, candidate)
		if index <= previousIndex {
			t.Fatalf("expected candidates sorted by score, got:\n%s", report)
		}
		previousIndex = index
	}
}

func TestPrintPlanReport_DefaultSettings(t *testing.T) {
	var output strings.Builder
	defaults := defaultDDTestSettings()

	printPlanReportData(&output, PlanReportData{
		DDTestSettings: &defaults,
	})

	report := output.String()
	if !strings.Contains(report, "DDTest settings\n  Settings: defaults") {
		t.Errorf("expected default ddtest settings message, got:\n%s", report)
	}
}

func TestPrintDDTestSettingsReport_AllSupportedSettings(t *testing.T) {
	config := defaultDDTestSettings()
	config.Platform = "python"
	config.Framework = "pytest"
	config.Command = "pytest -q"
	config.MinParallelism++
	config.MaxParallelism += 2
	config.ParallelRunnerOverhead += time.Second
	config.TargetTime = 12 * time.Minute
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
		"Target time",
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

	printPlanReportData(&output, PlanReportData{
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
	client := &MockTestOptimizationClient{
		KnownTests: &api.KnownTestsResponseData{
			Tests: api.KnownTestsResponseDataModules{
				"module-a": api.KnownTestsResponseDataSuites{
					"suite-a": []string{"test-a", "test-b"},
				},
				"module-b": api.KnownTestsResponseDataSuites{
					"suite-b": []string{"test-c"},
					"suite-c": []string{"test-d", "test-e"},
				},
			},
		},
		TestManagementTests: &api.TestManagementTestsResponseDataModules{
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
		},
		BackendRequestTimingValues: testoptimization.BackendRequestTimings{
			KnownTests:          time.Millisecond,
			TestManagementTests: time.Millisecond,
			TestSuiteDurations:  time.Millisecond,
		},
		Durations: map[string]map[string]api.TestSuiteDurationInfo{
			"module-a": {
				"suite-a": {},
				"suite-b": {},
			},
			"module-b": {
				"suite-c": {},
			},
		},
	}

	client.GetTestSuiteDurations()
	timings := client.BackendRequestTimings()

	known := reportKnownTests(client.GetKnownTests(), timings.KnownTests)
	if known.Modules != 2 || known.Suites != 3 || known.Tests != 5 {
		t.Errorf("unexpected known test summary: %+v", known)
	}

	managed := reportManagedFlakyTests(client.GetTestManagementTestsData(), timings.TestManagementTests)
	if managed.Total != 3 || managed.Quarantined != 1 || managed.Disabled != 1 || managed.AttemptToFix != 1 {
		t.Errorf("unexpected managed flaky test summary: %+v", managed)
	}

	durationReport := reportTestSuiteDurations(client.GetTestSuiteDurations(), timings.TestSuiteDurations)
	if durationReport.Modules != 2 || durationReport.Suites != 3 {
		t.Errorf("unexpected test suite durations summary: %+v", durationReport)
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

func TestReportFormattingVariants(t *testing.T) {
	t.Run("suite labels", func(t *testing.T) {
		tests := []struct {
			name  string
			suite testSuiteTimingReport
			want  string
		}{
			{name: "missing", suite: testSuiteTimingReport{}, want: "not available"},
			{name: "module only", suite: testSuiteTimingReport{Module: "rspec"}, want: "rspec"},
			{name: "suite only", suite: testSuiteTimingReport{Suite: "CartSuite"}, want: "CartSuite"},
			{name: "module and suite", suite: testSuiteTimingReport{Module: "rspec", Suite: "CartSuite"}, want: "rspec / CartSuite"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if got := formatSuiteLabel(tt.suite); got != tt.want {
					t.Fatalf("formatSuiteLabel() = %q, want %q", got, tt.want)
				}
			})
		}
	})

	t.Run("basic values", func(t *testing.T) {
		tests := []struct {
			name string
			got  string
			want string
		}{
			{name: "plural dedicated runners", got: formatScheduledTestSuiteCount(2), want: "2 dedicated runners"},
			{name: "empty worker env", got: formatWorkerEnvKeys(" "), want: "not set"},
			{name: "platform only", got: formatPlatform("ruby", ""), want: "ruby"},
			{name: "framework only", got: formatPlatform("", "rspec"), want: "rspec"},
			{name: "empty setting", got: valueOrNotSet(""), want: "not set"},
			{name: "negative count", got: formatCount(-123456), want: "-123,456"},
			{name: "three digit group count", got: formatCount(123456), want: "123,456"},
			{name: "singular count unit", got: formatCountWithUnit(1, "suite", "suites"), want: "1 suite"},
			{name: "sub-millisecond duration", got: formatDuration(500 * time.Microsecond), want: "500µs"},
			{name: "cache not configured", got: formatDiscoveryCache(discoveryCacheResult{}), want: "not configured"},
			{name: "cache used", got: formatDiscoveryCache(discoveryCacheResult{Configured: true, Used: true}), want: "used"},
			{name: "cache configured but not used", got: formatDiscoveryCache(discoveryCacheResult{Configured: true}), want: "not used"},
			{name: "cache configured but skipped with reason", got: formatDiscoveryCache(discoveryCacheResult{Configured: true, NotUsedReason: "full discovery not required"}), want: "not used (full discovery not required)"},
			{name: "backend data without duration", got: formatBackendDataValue("not available", 0), want: "not available"},
			{name: "optional duration missing", got: formatOptionalDuration(0), want: "not available"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				if tt.got != tt.want {
					t.Fatalf("got %q, want %q", tt.got, tt.want)
				}
			})
		}
	})

	t.Run("ddtest setting fallback formatting", func(t *testing.T) {
		unnamedField := reflect.StructField{Name: "CustomSetting"}
		if got := formatDDTestSettingName(unnamedField); got != "CustomSetting" {
			t.Fatalf("formatDDTestSettingName() = %q, want CustomSetting", got)
		}

		field := reflect.StructField{Name: "CustomSetting", Tag: `mapstructure:"custom_setting"`}
		value := reflect.ValueOf(struct{ Enabled bool }{Enabled: true})
		if got := formatDDTestSettingValue(field, value); got != "{true}" {
			t.Fatalf("formatDDTestSettingValue() = %q, want {true}", got)
		}
	})
}

func TestSkippableReportFormattingVariants(t *testing.T) {
	t.Run("applied TIA skippables", func(t *testing.T) {
		tests := []struct {
			name            string
			datadogSettings datadogSettingsReport
			skippables      skippablesReport
			skipping        skippingApplicationReport
			want            string
		}{
			{
				name:            "disabled",
				datadogSettings: datadogSettingsReport{Available: true, TestSkipping: false},
				skippables:      skippablesReport{Available: true, TestSkippingLevel: settings.TestSkippingLevelTest},
				skipping:        skippingApplicationReport{TIATests: 4},
				want:            "disabled",
			},
			{
				name:     "not available",
				skipping: skippingApplicationReport{TIATests: 4},
				want:     "not available",
			},
			{
				name:       "test level",
				skippables: skippablesReport{Available: true, TestSkippingLevel: settings.TestSkippingLevelTest},
				skipping:   skippingApplicationReport{TIATests: 1},
				want:       "1 test",
			},
			{
				name:       "mode not available",
				skippables: skippablesReport{Available: true},
				skipping:   skippingApplicationReport{TIATests: 2},
				want:       "mode not available",
			},
			{
				name:       "mixed mode fallback",
				skippables: skippablesReport{Available: true, TestSkippingLevel: settings.TestSkippingLevel("mixed")},
				skipping:   skippingApplicationReport{TIATests: 2, TIASuites: 3},
				want:       "2 tests, 3 suites",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				got := formatAppliedTIASkippables(tt.datadogSettings, tt.skippables, tt.skipping)
				if got != tt.want {
					t.Fatalf("formatAppliedTIASkippables() = %q, want %q", got, tt.want)
				}
			})
		}
	})

	t.Run("returned TIA skippables", func(t *testing.T) {
		got := formatTIASkippables(
			datadogSettingsReport{Available: true, TestSkipping: true},
			skippablesReport{Available: true},
		)
		if got != "skipping mode not available" {
			t.Fatalf("formatTIASkippables() = %q, want skipping mode not available", got)
		}

		got = formatTIASkippables(
			datadogSettingsReport{Available: true, TestSkipping: true},
			skippablesReport{Available: true, TestSkippingLevel: settings.TestSkippingLevel("mixed"), TIATests: 1, TIASuites: 2},
		)
		if got != "1 test, 2 suites" {
			t.Fatalf("formatTIASkippables() = %q, want mixed fallback", got)
		}
	})

	t.Run("disabled tests", func(t *testing.T) {
		if got := formatAppliedDisabledTests(datadogSettingsReport{}, managedFlakyTestsReport{}, skippingApplicationReport{}); got != "not available" {
			t.Fatalf("formatAppliedDisabledTests() = %q, want not available", got)
		}
		if got := formatAppliedDisabledTests(datadogSettingsReport{Available: true}, managedFlakyTestsReport{}, skippingApplicationReport{}); got != "disabled" {
			t.Fatalf("formatAppliedDisabledTests() = %q, want disabled", got)
		}
	})
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
