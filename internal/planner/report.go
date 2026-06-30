package planner

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/ddtest/internal/constants"
	"github.com/DataDog/ddtest/internal/runmetadata"
	"github.com/DataDog/ddtest/internal/settings"
)

func printPlanReport(w io.Writer, report planReport) {
	reportFprintln(w, "+++ DDTest: plan report")
	reportFprintln(w)
	printRunInfoReport(w, report.RunInfo, report.PlanInfo)
	reportFprintln(w)
	printDDTestSettingsReport(w, report.DDTestSettings)
	reportFprintln(w)
	printDatadogSettingsReport(w, report.DatadogSettings)
	reportFprintln(w)
	printBackendDataReport(w, report)
	reportFprintln(w)
	printPlanningReport(w, report.Planning)
	reportFprintln(w)
	printSplitReport(w, report.Split)
	reportFprintln(w)
	printLongSeparateRunnerSuitesReport(w, report.LongSeparateRunnerSuites)
	reportFprintln(w)
	printSlowestTestSuitesOverallReport(w, report.SlowestTestSuitesOverall)
}

func printRunInfoReport(w io.Writer, runInfo runmetadata.RunInfo, planInfo PlanInfo) {
	reportFprintln(w, "Run")
	reportFprintf(w, "  Service: %s\n", valueOrNotAvailable(runInfo.Service))
	reportFprintf(w, "  Repository: %s\n", valueOrNotAvailable(runInfo.Repository))
	reportFprintf(w, "  Commit: %s\n", valueOrNotAvailable(runInfo.Commit))
	reportFprintf(w, "  Branch: %s\n", valueOrNotAvailable(runInfo.Branch))
	reportFprintf(w, "  Platform: %s\n", formatPlatform(planInfo.Platform, planInfo.Framework))
	reportFprintf(w, "  OS tags: %s\n", formatTagList(planInfo.OSTags, constants.OSPlatform, constants.OSArchitecture, constants.OSVersion))
	reportFprintf(w, "  Runtime tags: %s\n", formatTagList(planInfo.RuntimeTags, constants.RuntimeName, constants.RuntimeVersion))
}

func printDDTestSettingsReport(w io.Writer, config *settings.Config) {
	reportFprintln(w, "DDTest settings")
	if config == nil {
		reportFprintln(w, "  Settings: not available")
		return
	}

	if !printChangedDDTestSettings(w, config) {
		reportFprintln(w, "  Settings: defaults")
	}
}

func printChangedDDTestSettings(w io.Writer, config *settings.Config) bool {
	defaults := settings.DefaultConfig()
	configValue := reflect.ValueOf(*config)
	defaultsValue := reflect.ValueOf(defaults)
	configType := configValue.Type()
	printed := false

	for i := range configValue.NumField() {
		value := configValue.Field(i)
		if reflect.DeepEqual(value.Interface(), defaultsValue.Field(i).Interface()) {
			continue
		}

		field := configType.Field(i)
		reportFprintf(w, "  %s: %s\n", formatDDTestSettingName(field), formatDDTestSettingValue(field, value))
		printed = true
	}

	return printed
}

func formatDDTestSettingName(field reflect.StructField) string {
	if label := field.Tag.Get("report"); label != "" {
		return label
	}

	key := configFieldKey(field)
	if key == "" {
		return field.Name
	}

	words := strings.Split(key, "_")
	for i, word := range words {
		if word == "ci" {
			words[i] = "CI"
			continue
		}
		if i == 0 {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

func formatDDTestSettingValue(field reflect.StructField, value reflect.Value) string {
	if configFieldKey(field) == "worker_env" {
		return formatWorkerEnvKeys(value.String())
	}

	switch value.Type() {
	case reflect.TypeOf(time.Duration(0)):
		return formatDuration(time.Duration(value.Int()))
	case reflect.TypeOf(settings.TestSkippingLevel("")):
		return valueOrNotSet(value.Interface().(settings.TestSkippingLevel).String())
	}

	switch value.Kind() {
	case reflect.String:
		return valueOrNotSet(value.String())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return formatCount(int(value.Int()))
	case reflect.Bool:
		return strconv.FormatBool(value.Bool())
	default:
		return fmt.Sprint(value.Interface())
	}
}

func configFieldKey(field reflect.StructField) string {
	key, _, _ := strings.Cut(field.Tag.Get("mapstructure"), ",")
	return key
}

func printDatadogSettingsReport(w io.Writer, report datadogSettingsReport) {
	reportFprintln(w, "Datadog settings")
	if !report.Available {
		reportFprintln(w, "  Settings: not available")
		return
	}

	reportFprintf(w, "  Test Impact Analysis: %s\n", enabledWord(report.TestImpactAnalysis))
	reportFprintf(w, "    Test skipping: %s\n", enabledWord(report.TestSkipping))
	reportFprintf(w, "    Test impact collection: %s\n", enabledWord(report.TestImpactCollection))
	reportFprintf(w, "  Known tests: %s\n", enabledWord(report.KnownTests))
	reportFprintf(w, "  Early flake detection: %s\n", enabledWord(report.EarlyFlakeDetection))
	reportFprintf(w, "  Auto test retries: %s\n", enabledWord(report.AutoTestRetries))
	reportFprintf(w, "  Flaky test management: %s\n", enabledWord(report.FlakyTestManagement))
}

func printBackendDataReport(w io.Writer, report planReport) {
	reportFprintln(w, "Backend data")
	reportFprintf(w, "  Known tests: %s\n", formatKnownTests(report.DatadogSettings, report.KnownTests))
	reportFprintf(w, "  Skippable tests for this run: %s\n", formatSkippableTests(report.DatadogSettings, report.SkippableTestsCount))
	reportFprintf(w, "  Managed flaky tests: %s\n", formatManagedFlakyTests(report.DatadogSettings, report.ManagedFlakyTests))
	reportFprintf(w, "  Test suite durations: %s\n", formatTestSuiteDurations(report.TestSuiteDurations))
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

func printLongSeparateRunnerSuitesReport(w io.Writer, suites []testSuiteTimingReport) {
	reportFprintln(w, "Slow suites on dedicated runners")
	if len(suites) == 0 {
		reportFprintln(w, "  None")
		return
	}

	reportFprintf(w, "  ATTENTION: %s\n", formatScheduledTestSuiteCount(len(suites)))
	for i, suite := range suites {
		printTestSuiteTimingReport(w, i+1, suite, true)
	}
}

func printSlowestTestSuitesOverallReport(w io.Writer, suites []testSuiteTimingReport) {
	reportFprintln(w, "10 slowest test suites overall")
	if len(suites) == 0 {
		reportFprintln(w, "  No suite timing data available")
		return
	}

	for i, suite := range suites {
		printTestSuiteTimingReport(w, i+1, suite, false)
	}
}

func printTestSuiteTimingReport(w io.Writer, index int, suite testSuiteTimingReport, includeRunner bool) {
	runnerPrefix := ""
	if includeRunner {
		runnerPrefix = fmt.Sprintf("runner %d, ", suite.Runner)
	}

	reportFprintf(w, "  %d. %s%s (%s): historical duration %s, estimated runtime %s\n",
		index,
		runnerPrefix,
		formatSuiteLabel(suite),
		valueOrNotAvailable(suite.SourceFile),
		formatDuration(suite.TotalDuration),
		formatDuration(suite.EstimatedDuration))
}

func formatSuiteLabel(suite testSuiteTimingReport) string {
	switch {
	case suite.Module == "" && suite.Suite == "":
		return "not available"
	case suite.Module == "":
		return suite.Suite
	case suite.Suite == "":
		return suite.Module
	default:
		return suite.Module + " / " + suite.Suite
	}
}

func formatScheduledTestSuiteCount(count int) string {
	if count == 1 {
		return "1 dedicated runner"
	}
	return formatCount(count) + " dedicated runners"
}

func formatWorkerEnvKeys(workerEnv string) string {
	keys := parseWorkerEnvKeys(workerEnv)
	if len(keys) == 0 {
		return "not set"
	}
	return strings.Join(keys, ", ")
}

func parseWorkerEnvKeys(workerEnv string) []string {
	if strings.TrimSpace(workerEnv) == "" {
		return nil
	}

	seen := make(map[string]struct{})
	keys := make([]string, 0)
	for pair := range strings.SplitSeq(workerEnv, ";") {
		key, _, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}

		seen[key] = struct{}{}
		keys = append(keys, key)
	}

	slices.Sort(keys)
	return keys
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

func formatTestSuiteDurations(durations testSuiteDurationsReport) string {
	if !durations.Available {
		return "not available"
	}
	return fmt.Sprintf("%s modules, %s suites",
		formatCount(durations.Modules),
		formatCount(durations.Suites))
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

func valueOrNotSet(value string) string {
	if value == "" {
		return "not set"
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
