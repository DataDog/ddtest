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

func printPlanReport(w io.Writer, tp *TestPlanner, selection splitSelection) {
	printPlanReportData(w, tp.newPlanReportData(selection.selected).withSplitSelection(selection))
}

func printPlanReportData(w io.Writer, report PlanReportData) {
	reportFprintln(w, "+++ DDTest: plan report")
	reportFprintln(w)
	printRunInfoReport(w, report.RunInfo, report.PlanMetadata)
	reportFprintln(w)
	printDDTestSettingsReport(w, report.DDTestSettings)
	reportFprintln(w)
	printDatadogSettingsReport(w, report.DatadogSettings)
	reportFprintln(w)
	printBackendDataReport(w, report)
	reportFprintln(w)
	printPlanningReport(w, report)
	reportFprintln(w)
	printLongSeparateRunnerSuitesReport(w, report.LongSeparateRunnerSuites)
	reportFprintln(w)
	printSlowestTestSuitesOverallReport(w, report.SlowestTestSuitesOverall)
}

func printRunInfoReport(w io.Writer, runInfo runmetadata.RunInfo, planMetadata PlanMetadata) {
	reportFprintln(w, "Run")
	reportFprintf(w, "  Service: %s\n", valueOrNotAvailable(runInfo.Service))
	reportFprintf(w, "  Repository: %s\n", valueOrNotAvailable(runInfo.Repository))
	reportFprintf(w, "  Commit: %s\n", valueOrNotAvailable(runInfo.Commit))
	reportFprintf(w, "  Branch: %s\n", valueOrNotAvailable(runInfo.Branch))
	reportFprintf(w, "  Platform: %s\n", formatPlatform(planMetadata.Platform, planMetadata.Framework))
	reportFprintf(w, "  OS tags: %s\n", formatTagList(planMetadata.OSTags, constants.OSPlatform, constants.OSArchitecture, constants.OSVersion))
	reportFprintf(w, "  Runtime tags: %s\n", formatTagList(planMetadata.RuntimeTags, constants.RuntimeName, constants.RuntimeVersion))
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
	defaults := defaultDDTestSettings()
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
	key := configFieldKey(field)
	if key == "" {
		return field.Name
	}
	if key == "parallel_runner_overhead" {
		return "CI job overhead"
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

func defaultDDTestSettings() settings.Config {
	return settings.Config{
		Platform:               "ruby",
		Framework:              "rspec",
		MinParallelism:         settings.DefaultParallelism(),
		MaxParallelism:         settings.DefaultParallelism(),
		ParallelRunnerOverhead: settings.DefaultParallelRunnerOverhead(),
		TargetTime:             settings.DefaultTargetTime(),
		CiNode:                 -1,
		CiNodeWorkers:          1,
		TestSkippingLevel:      settings.TestSkippingLevelTest,
		ReportEnabled:          true,
	}
}

func printDatadogSettingsReport(w io.Writer, report datadogSettingsReport) {
	reportFprintln(w, "Datadog settings")
	if !report.Available {
		reportFprintln(w, "  Settings: not available")
		printFetchDuration(w, report.FetchDuration)
		return
	}

	printFetchDuration(w, report.FetchDuration)
	reportFprintf(w, "  Test Impact Analysis: %s\n", enabledWord(report.TestImpactAnalysis))
	reportFprintf(w, "    Test skipping: %s\n", enabledWord(report.TestSkipping))
	reportFprintf(w, "    Test impact collection: %s\n", enabledWord(report.TestImpactCollection))
	reportFprintf(w, "  Known tests: %s\n", enabledWord(report.KnownTests))
	reportFprintf(w, "  Early flake detection: %s\n", enabledWord(report.EarlyFlakeDetection))
	reportFprintf(w, "  Auto test retries: %s\n", enabledWord(report.AutoTestRetries))
	reportFprintf(w, "  Flaky test management: %s\n", enabledWord(report.FlakyTestManagement))
}

func printBackendDataReport(w io.Writer, report PlanReportData) {
	reportFprintln(w, "Backend data")
	reportFprintf(w, "  Known tests: %s\n", formatBackendDataValue(formatKnownTests(report.DatadogSettings, report.KnownTests), report.KnownTests.FetchDuration))
	reportFprintf(w, "  TIA skippables returned: %s\n", formatBackendDataValue(formatTIASkippables(report.DatadogSettings, report.Skippables), report.Skippables.FetchDuration))
	reportFprintf(w, "  Managed flaky tests: %s\n", formatBackendDataValue(formatManagedFlakyTests(report.DatadogSettings, report.ManagedFlakyTests), report.ManagedFlakyTests.FetchDuration))
	reportFprintf(w, "  Test suite durations: %s\n", formatBackendDataValue(formatTestSuiteDurations(report.TestSuiteDurations), report.TestSuiteDurations.FetchDuration))
}

func printPlanningReport(w io.Writer, report PlanReportData) {
	reportFprintln(w, "Planning")
	printDiscoveryPlanningReport(w, report.Planning.Discovery)
	printDurationEstimatesPlanningReport(w, report.Planning.Durations)
	printSkippingPlanningReport(w, report.DatadogSettings, report.Skippables, report.ManagedFlakyTests, report.Planning.Skipping)
	printRunSetPlanningReport(w, report.Planning)
	printRunnerSplitPlanningReport(w, report)
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

func printDiscoveryPlanningReport(w io.Writer, discovery discoveryReport) {
	if !discovery.Available {
		reportFprintln(w, "  Discovery: not available")
		return
	}

	reportFprintln(w, "  Discovery")
	reportFprintf(w, "    Method: %s\n", valueOrNotAvailable(string(discovery.Mode)))
	reportFprintf(w, "    Test files: %s\n", formatCount(discovery.TestFiles))
	if discovery.Cache.Configured || discovery.Cache.Used || discovery.Cache.NotUsedReason != "" {
		reportFprintf(w, "    Cache: %s\n", formatDiscoveryCache(discovery.Cache))
	}
	reportFprintf(w, "    Duration: %s\n", formatOptionalDuration(discovery.Duration))
	switch discovery.Mode {
	case discoveryModeFull:
		reportFprintf(w, "    Suites discovered: %s\n", formatCount(discovery.Suites))
		reportFprintf(w, "    Tests discovered: %s\n", formatCount(discovery.Tests))
	}
}

func formatDiscoveryCache(cache discoveryCacheResult) string {
	if cache.Used {
		return "used"
	}
	if !cache.Configured {
		return "not configured"
	}
	if cache.NotUsedReason == "" {
		return "not used"
	}
	return "not used (" + cache.NotUsedReason + ")"
}

func printFetchDuration(w io.Writer, duration time.Duration) {
	if duration > 0 {
		reportFprintf(w, "  Fetch duration: %s\n", formatDuration(duration))
	}
}

func formatBackendDataValue(value string, duration time.Duration) string {
	if duration <= 0 {
		return value
	}
	return fmt.Sprintf("%s (fetched in %s)", value, formatDuration(duration))
}

func formatOptionalDuration(duration time.Duration) string {
	if duration <= 0 {
		return "not available"
	}
	return formatDuration(duration)
}

func printDurationEstimatesPlanningReport(w io.Writer, durations durationApplicationReport) {
	if !durations.Available {
		reportFprintln(w, "  Duration estimates: not available")
		return
	}

	reportFprintln(w, "  Duration estimates")
	reportFprintf(w, "    Backend durations used: %s\n", formatCountWithUnit(durations.BackendDurationsApplied, "suite", "suites"))
	reportFprintf(w, "    Default durations used: %s\n", formatCountWithUnit(durations.SuitesWithoutDurations, "suite", "suites"))
	reportFprintf(w, "    Backend-only suites added: %s\n", formatCount(durations.BackendSuitesAdded))
}

func printSkippingPlanningReport(
	w io.Writer,
	datadogSettings datadogSettingsReport,
	skippables skippablesReport,
	managed managedFlakyTestsReport,
	skipping skippingApplicationReport,
) {
	if !skipping.Available {
		reportFprintln(w, "  Skipping: not available")
		return
	}

	reportFprintln(w, "  Skipping")
	reportFprintf(w, "    TIA skippables applied: %s\n", formatAppliedTIASkippables(datadogSettings, skippables, skipping))
	reportFprintf(w, "    Disabled tests applied: %s\n", formatAppliedDisabledTests(datadogSettings, managed, skipping))
	reportFprintf(w, "    Suites marked unskippable: %s\n", formatCount(skipping.UnskippableMarkerSuitesForced))
	reportFprintf(w, "    Files fully skipped: %s\n", formatCount(skipping.FullySkippedFiles))
}

func printRunSetPlanningReport(w io.Writer, planning planningReport) {
	if !planning.Discovery.Available {
		reportFprintln(w, "  Run set: not available")
		return
	}

	reportFprintln(w, "  Run set")
	reportFprintf(w, "    Test files to run: %s\n", formatCount(planning.TestFilesToRun))
	reportFprintf(w, "    Estimated time saved: %.2f%%\n", planning.EstimatedTimeSaved)
}

func printRunnerSplitPlanningReport(w io.Writer, report PlanReportData) {
	planning := report.Planning
	selection := effectiveSplitSelection(report)
	split := selection.selected
	if split.parallelRunners <= 0 {
		reportFprintln(w, "  Split selection: not available")
		return
	}

	fullDuration := "not available"
	if planning.Durations.ExpectedFullDuration > 0 {
		fullDuration = formatDuration(planning.Durations.ExpectedFullDuration)
	}

	reportFprintln(w, "  Split selection")
	reportFprintf(w, "    Full test suite time without TIA: %s\n", fullDuration)
	reportFprintf(w, "    Selected: %s\n", formatRunnerCount(split.parallelRunners))
	if selection.available {
		reportFprintf(w, "    Reason: %s\n", formatSplitSelectionReason(selection))
		printTargetTimeReport(w, selection)
	}
	reportFprintf(w, "    Estimated runtime with TIA: %s\n", formatDuration(split.totalRuntimeDuration()))
	reportFprintf(w, "    Expected wall time: %s\n", formatDuration(split.wallTimeDuration()))
	if selection.available {
		reportFprintf(w, "    Modeled CI overhead: %s (%s x configured CI job overhead %s)\n",
			formatDuration(selection.overheadDuration(split)),
			formatRunnerCount(split.parallelRunners),
			formatDuration(selection.parallelRunnerOverhead))
		reportFprintf(w, "    Selection score: %s (wall time + modeled CI overhead)\n", formatDuration(selection.scoreDuration(split)))
	}
	reportFprintf(w, "    Imbalance: %s\n", formatDuration(split.imbalanceDuration()))
	if selection.available && selection.targetTime > 0 {
		printWithoutTargetTimeReport(w, selection)
	}
	if selection.available && len(selection.candidates) > 0 {
		printSplitCandidatesReport(w, selection)
	}
}

func effectiveSplitSelection(report PlanReportData) splitSelection {
	if report.SplitSelection.available {
		return report.SplitSelection
	}
	return splitSelection{
		selected:          report.Split,
		bestWithoutTarget: report.Split,
	}
}

func formatSplitSelectionReason(selection splitSelection) string {
	if selection.targetTime <= 0 {
		return "lowest selection score"
	}
	if selection.meetsTargetTime(selection.selected) {
		if sameSplitScore(selection.selected, selection.bestWithoutTarget) {
			return "lowest selection score; target time satisfied"
		}
		return "lowest selection score among splits that meet target time"
	}
	return "no split met target time; selected lowest score from all candidates"
}

func printTargetTimeReport(w io.Writer, selection splitSelection) {
	if selection.targetTime <= 0 {
		return
	}
	if selection.meetsTargetTime(selection.selected) {
		reportFprintf(w, "    Target time: %s, satisfied\n", formatDuration(selection.targetTime))
		return
	}
	reportFprintf(w, "    Target time: %s, not reachable (best wall time %s)\n",
		formatDuration(selection.targetTime),
		formatDuration(selection.bestWallTime()))
}

func printWithoutTargetTimeReport(w io.Writer, selection splitSelection) {
	best := selection.bestWithoutTarget
	if sameSplitScore(selection.selected, best) {
		reportFprintln(w)
		reportFprintln(w, "    Without target time: same as selected")
		return
	}

	reportFprintln(w)
	reportFprintf(w, "    Without target time: %s (wall %s, overhead %s, score %s)\n",
		formatRunnerCount(best.parallelRunners),
		formatDuration(best.wallTimeDuration()),
		formatDuration(selection.overheadDuration(best)),
		formatDuration(selection.scoreDuration(best)))
	reportFprintf(w, "      Selected vs without target: %s\n", formatSelectedVsWithoutTarget(selection))
}

func printSplitCandidatesReport(w io.Writer, selection splitSelection) {
	candidates := selection.sortedCandidatesByScore()
	limit := 5
	if len(candidates) < limit {
		limit = len(candidates)
	}

	reportFprintln(w)
	if len(candidates) > limit {
		reportFprintf(w, "    Candidates (best %d of %d by score)\n", limit, len(candidates))
	} else {
		reportFprintln(w, "    Candidates")
	}
	for _, candidate := range candidates[:limit] {
		reason := formatSplitCandidateReason(selection, candidate)
		if reason != "" {
			reason = ", " + reason
		}
		reportFprintf(w, "      %s: wall %s, overhead %s, score %s%s\n",
			formatRunnerCount(candidate.parallelRunners),
			formatDuration(candidate.wallTimeDuration()),
			formatDuration(selection.overheadDuration(candidate)),
			formatDuration(selection.scoreDuration(candidate)),
			reason)
	}
}

func formatSplitCandidateReason(selection splitSelection, candidate splitScore) string {
	parts := make([]string, 0, 3)
	if selection.targetTime > 0 {
		if selection.meetsTargetTime(candidate) {
			parts = append(parts, "met target")
		} else {
			parts = append(parts, "missed target by "+formatDuration(candidate.wallTimeDuration()-selection.targetTime))
		}
	}
	if selection.targetTime > 0 && sameSplitScore(candidate, selection.bestWithoutTarget) && !sameSplitScore(candidate, selection.selected) {
		parts = append(parts, "would choose without target time")
	}
	if sameSplitScore(candidate, selection.selected) {
		parts = append(parts, "selected")
	}
	return strings.Join(parts, "; ")
}

func formatSelectedVsWithoutTarget(selection splitSelection) string {
	return formatWallTimeDifference(selection.selected, selection.bestWithoutTarget) + ", " +
		formatOverheadDifference(selection.overheadDuration(selection.selected), selection.overheadDuration(selection.bestWithoutTarget))
}

func formatWallTimeDifference(selected, withoutTarget splitScore) string {
	switch {
	case selected.wallTime < withoutTarget.wallTime:
		return formatDuration(withoutTarget.wallTimeDuration()-selected.wallTimeDuration()) + " faster wall time"
	case selected.wallTime > withoutTarget.wallTime:
		return formatDuration(selected.wallTimeDuration()-withoutTarget.wallTimeDuration()) + " slower wall time"
	default:
		return "same wall time"
	}
}

func formatOverheadDifference(selected, withoutTarget time.Duration) string {
	switch {
	case selected > withoutTarget:
		return formatDuration(selected-withoutTarget) + " more CI overhead"
	case selected < withoutTarget:
		return formatDuration(withoutTarget-selected) + " less CI overhead"
	default:
		return "same CI overhead"
	}
}

func formatRunnerCount(count int) string {
	return formatCountWithUnit(count, "runner", "runners")
}

func sameSplitScore(a, b splitScore) bool {
	return a.parallelRunners == b.parallelRunners &&
		a.wallTime == b.wallTime &&
		a.imbalance == b.imbalance &&
		a.totalRuntime == b.totalRuntime
}

func (s splitSelection) overheadDuration(score splitScore) time.Duration {
	if s.parallelRunnerOverhead <= 0 {
		return 0
	}
	return time.Duration(score.parallelRunners) * s.parallelRunnerOverhead
}

func (s splitSelection) scoreDuration(score splitScore) time.Duration {
	return score.wallTimeDuration() + s.overheadDuration(score)
}

func (s splitSelection) meetsTargetTime(score splitScore) bool {
	return s.targetTime > 0 && score.wallTimeDuration() <= s.targetTime
}

func (s splitSelection) bestWallTime() time.Duration {
	if len(s.candidates) == 0 {
		return s.selected.wallTimeDuration()
	}
	best := s.candidates[0].wallTimeDuration()
	for _, candidate := range s.candidates[1:] {
		if candidate.wallTimeDuration() < best {
			best = candidate.wallTimeDuration()
		}
	}
	return best
}

func (s splitSelection) sortedCandidatesByScore() []splitScore {
	candidates := slices.Clone(s.candidates)
	selector := splitSelector{parallelRunnerOverhead: s.parallelRunnerOverhead}
	slices.SortFunc(candidates, func(a, b splitScore) int {
		switch {
		case selector.better(a, b):
			return -1
		case selector.better(b, a):
			return 1
		default:
			return 0
		}
	})
	return candidates
}

func formatAppliedTIASkippables(datadogSettings datadogSettingsReport, skippables skippablesReport, skipping skippingApplicationReport) string {
	if datadogSettings.Available && !datadogSettings.TestSkipping {
		return "disabled"
	}
	if !skippables.Available {
		return "not available"
	}

	switch skippables.TestSkippingLevel {
	case settings.TestSkippingLevelTest:
		return formatCountWithUnit(skipping.TIATests, "test", "tests")
	case settings.TestSkippingLevelSuite:
		return formatCountWithUnit(skipping.TIASuites, "suite", "suites")
	case "":
		return "mode not available"
	default:
		return fmt.Sprintf("%s, %s",
			formatCountWithUnit(skipping.TIATests, "test", "tests"),
			formatCountWithUnit(skipping.TIASuites, "suite", "suites"))
	}
}

func formatAppliedDisabledTests(datadogSettings datadogSettingsReport, managed managedFlakyTestsReport, skipping skippingApplicationReport) string {
	if datadogSettings.Available && !datadogSettings.FlakyTestManagement {
		return "disabled"
	}
	if !managed.Available {
		return "not available"
	}
	return formatCountWithUnit(skipping.DisabledTests, "test", "tests")
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

func formatTIASkippables(datadogSettings datadogSettingsReport, skippables skippablesReport) string {
	if datadogSettings.Available && !datadogSettings.TestSkipping {
		return "disabled"
	}
	if !skippables.Available {
		return "not available"
	}
	switch skippables.TestSkippingLevel {
	case settings.TestSkippingLevelTest:
		return formatCountWithUnit(skippables.TIATests, "test", "tests")
	case settings.TestSkippingLevelSuite:
		return formatCountWithUnit(skippables.TIASuites, "suite", "suites")
	case "":
		return "skipping mode not available"
	default:
		return fmt.Sprintf("%s, %s",
			formatCountWithUnit(skippables.TIATests, "test", "tests"),
			formatCountWithUnit(skippables.TIASuites, "suite", "suites"))
	}
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

func formatCountWithUnit(count int, singular string, plural string) string {
	unit := plural
	if count == 1 {
		unit = singular
	}
	return formatCount(count) + " " + unit
}

func formatDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return duration.String()
	}
	return duration.Round(time.Millisecond).String()
}
