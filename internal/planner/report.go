package planner

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	ciConstants "github.com/DataDog/ddtest/civisibility/constants"
	"github.com/DataDog/ddtest/civisibility/utils/net"
	"github.com/DataDog/ddtest/internal/runmetadata"
)

type datadogSettingsReport struct {
	Available            bool
	TestImpactAnalysis   bool
	TestSkipping         bool
	TestImpactCollection bool
	KnownTests           bool
	ImpactedTests        bool
	EarlyFlakeDetection  bool
	AutoTestRetries      bool
	FlakyTestManagement  bool
}

func newDatadogSettingsReport(settings *net.SettingsResponseData) datadogSettingsReport {
	if settings == nil {
		return datadogSettingsReport{}
	}
	return datadogSettingsReport{
		Available:            true,
		TestImpactAnalysis:   settings.ItrEnabled,
		TestSkipping:         settings.TestsSkipping,
		TestImpactCollection: settings.CodeCoverage,
		KnownTests:           settings.KnownTestsEnabled,
		ImpactedTests:        settings.ImpactedTestsEnabled,
		EarlyFlakeDetection:  settings.EarlyFlakeDetection.Enabled,
		AutoTestRetries:      settings.FlakyTestRetriesEnabled,
		FlakyTestManagement:  settings.TestManagement.Enabled,
	}
}

type knownTestsReport struct {
	Available bool
	Modules   int
	Suites    int
	Tests     int
}

func newKnownTestsReport(knownTests *net.KnownTestsResponseData) knownTestsReport {
	if knownTests == nil {
		return knownTestsReport{}
	}

	report := knownTestsReport{
		Available: true,
		Modules:   len(knownTests.Tests),
	}
	for _, suites := range knownTests.Tests {
		report.Suites += len(suites)
		for _, tests := range suites {
			report.Tests += len(tests)
		}
	}
	return report
}

type managedFlakyTestsReport struct {
	Available    bool
	Total        int
	Quarantined  int
	Disabled     int
	AttemptToFix int
}

func newManagedFlakyTestsReport(testManagementTests *net.TestManagementTestsResponseDataModules) managedFlakyTestsReport {
	if testManagementTests == nil {
		return managedFlakyTestsReport{}
	}

	report := managedFlakyTestsReport{Available: true}
	for _, suites := range testManagementTests.Modules {
		for _, tests := range suites.Suites {
			for _, test := range tests.Tests {
				report.Total++
				if test.Properties.Quarantined {
					report.Quarantined++
				}
				if test.Properties.Disabled {
					report.Disabled++
				}
				if test.Properties.AttemptToFix {
					report.AttemptToFix++
				}
			}
		}
	}
	return report
}

type durationSourceReport struct {
	Known   int
	Default int
}

type planningReport struct {
	TestFilesDiscovered int
	FullySkippedFiles   int
	TestFilesToRun      int
	DurationSources     durationSourceReport
	EstimatedTimeSaved  float64
}

type planReport struct {
	RunInfo             runmetadata.RunInfo
	PlanInfo            PlanInfo
	DatadogSettings     datadogSettingsReport
	KnownTests          knownTestsReport
	SkippableTestsCount int
	ManagedFlakyTests   managedFlakyTestsReport
	Planning            planningReport
	Split               splitScore
}

func (tp *TestPlanner) newPlanningReport() planningReport {
	fullySkippedFiles := len(tp.testFiles) - len(tp.testFileWeights)
	if fullySkippedFiles < 0 {
		fullySkippedFiles = 0
	}

	return planningReport{
		TestFilesDiscovered: len(tp.testFiles),
		FullySkippedFiles:   fullySkippedFiles,
		TestFilesToRun:      len(tp.testFileWeights),
		DurationSources:     tp.durationSourceReport(),
		EstimatedTimeSaved:  tp.skippablePercentage,
	}
}

func (tp *TestPlanner) durationSourceReport() durationSourceReport {
	var report durationSourceReport
	for _, source := range tp.testFileDurationSources {
		switch source {
		case testFileDurationSourceKnown:
			report.Known++
		default:
			report.Default++
		}
	}
	return report
}

func printPlanReport(w io.Writer, report planReport) {
	reportFprintln(w, "+++ DDTest: plan report")
	reportFprintln(w)
	printRunInfoReport(w, report.RunInfo, report.PlanInfo)
	reportFprintln(w)
	printDatadogSettingsReport(w, report.DatadogSettings)
	reportFprintln(w)
	printBackendDataReport(w, report)
	reportFprintln(w)
	printPlanningReport(w, report.Planning)
	reportFprintln(w)
	printSplitReport(w, report.Split)
}

func printRunInfoReport(w io.Writer, runInfo runmetadata.RunInfo, planInfo PlanInfo) {
	reportFprintln(w, "Run")
	reportFprintf(w, "  Service: %s\n", valueOrNotAvailable(runInfo.Service))
	reportFprintf(w, "  Repository: %s\n", valueOrNotAvailable(runInfo.Repository))
	reportFprintf(w, "  Commit: %s\n", valueOrNotAvailable(runInfo.Commit))
	reportFprintf(w, "  Branch: %s\n", valueOrNotAvailable(runInfo.Branch))
	reportFprintf(w, "  Platform: %s\n", formatPlatform(planInfo.Platform, planInfo.Framework))
	reportFprintf(w, "  OS tags: %s\n", formatTagList(planInfo.OSTags, ciConstants.OSPlatform, ciConstants.OSArchitecture, ciConstants.OSVersion))
	reportFprintf(w, "  Runtime tags: %s\n", formatTagList(planInfo.RuntimeTags, ciConstants.RuntimeName, ciConstants.RuntimeVersion))
}

func printDatadogSettingsReport(w io.Writer, report datadogSettingsReport) {
	reportFprintln(w, "Datadog")
	if !report.Available {
		reportFprintln(w, "  Settings: not available")
		return
	}

	reportFprintf(w, "  Test Impact Analysis: %s\n", enabledWord(report.TestImpactAnalysis))
	reportFprintf(w, "    Test skipping: %s\n", enabledWord(report.TestSkipping))
	reportFprintf(w, "    Test impact collection: %s\n", enabledWord(report.TestImpactCollection))
	reportFprintf(w, "  Known tests: %s\n", enabledWord(report.KnownTests))
	reportFprintf(w, "  Impacted tests: %s\n", enabledWord(report.ImpactedTests))
	reportFprintf(w, "  Early flake detection: %s\n", enabledWord(report.EarlyFlakeDetection))
	reportFprintf(w, "  Auto test retries: %s\n", enabledWord(report.AutoTestRetries))
	reportFprintf(w, "  Flaky test management: %s\n", enabledWord(report.FlakyTestManagement))
}

func printBackendDataReport(w io.Writer, report planReport) {
	reportFprintln(w, "Backend data")
	reportFprintf(w, "  Known tests: %s\n", formatKnownTests(report.DatadogSettings, report.KnownTests))
	reportFprintf(w, "  Skippable tests for this run: %s\n", formatSkippableTests(report.DatadogSettings, report.SkippableTestsCount))
	reportFprintf(w, "  Managed flaky tests: %s\n", formatManagedFlakyTests(report.DatadogSettings, report.ManagedFlakyTests))
}

func printPlanningReport(w io.Writer, report planningReport) {
	reportFprintln(w, "Planning")
	reportFprintf(w, "  Test files discovered: %s\n", formatCount(report.TestFilesDiscovered))
	reportFprintf(w, "  Fully skipped files: %s\n", formatCount(report.FullySkippedFiles))
	reportFprintf(w, "  Test files to run: %s\n", formatCount(report.TestFilesToRun))
	reportFprintf(w, "  Duration source: %s known, %s default\n",
		formatCount(report.DurationSources.Known),
		formatCount(report.DurationSources.Default))
	reportFprintf(w, "  Estimated time saved: %.2f%%\n", report.EstimatedTimeSaved)
}

func printSplitReport(w io.Writer, report splitScore) {
	reportFprintln(w, "Split")
	reportFprintf(w, "  Runners: %s\n", formatCount(report.parallelRunners))
	reportFprintf(w, "  Expected wall time: %s\n", formatDuration(report.wallTimeDuration()))
	reportFprintf(w, "  Imbalance: %s\n", formatDuration(report.imbalanceDuration()))
	reportFprintf(w, "  Total estimated runtime: %s\n", formatDuration(report.totalRuntimeDuration()))
}

func reportFprintln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

func reportFprintf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

func formatKnownTests(settings datadogSettingsReport, known knownTestsReport) string {
	if settings.Available && !settings.KnownTests {
		return "disabled"
	}
	if !known.Available {
		return "not available"
	}
	return fmt.Sprintf("%s modules, %s suites, %s tests",
		formatCount(known.Modules),
		formatCount(known.Suites),
		formatCount(known.Tests))
}

func formatSkippableTests(settings datadogSettingsReport, count int) string {
	if settings.Available && !settings.TestSkipping {
		return "disabled"
	}
	return formatCount(count)
}

func formatManagedFlakyTests(settings datadogSettingsReport, managed managedFlakyTestsReport) string {
	if settings.Available && !settings.FlakyTestManagement {
		return "disabled"
	}
	if !managed.Available {
		return "not available"
	}
	return fmt.Sprintf("%s total, %s quarantined, %s disabled, %s attempt-to-fix",
		formatCount(managed.Total),
		formatCount(managed.Quarantined),
		formatCount(managed.Disabled),
		formatCount(managed.AttemptToFix))
}

func formatTagList(tags map[string]string, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value := tags[key]; value != "" {
			parts = append(parts, key+"="+value)
		}
	}
	if len(parts) == 0 {
		return "not available"
	}
	return strings.Join(parts, ", ")
}

func formatPlatform(platformName, frameworkName string) string {
	switch {
	case platformName == "" && frameworkName == "":
		return "not available"
	case platformName == "":
		return frameworkName
	case frameworkName == "":
		return platformName
	default:
		return platformName + " / " + frameworkName
	}
}

func valueOrNotAvailable(value string) string {
	if value == "" {
		return "not available"
	}
	return value
}

func enabledWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func formatCount(count int) string {
	sign := ""
	if count < 0 {
		sign = "-"
		count = -count
	}

	value := strconv.Itoa(count)
	if len(value) <= 3 {
		return sign + value
	}

	prefixLength := len(value) % 3
	if prefixLength == 0 {
		prefixLength = 3
	}

	var builder strings.Builder
	builder.WriteString(sign)
	builder.WriteString(value[:prefixLength])
	for i := prefixLength; i < len(value); i += 3 {
		builder.WriteByte(',')
		builder.WriteString(value[i : i+3])
	}
	return builder.String()
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return duration.String()
	}
	return duration.Round(time.Millisecond).String()
}
