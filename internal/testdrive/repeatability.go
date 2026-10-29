package testdrive

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Retain recurring per-test differences even when other tests change between
// repeats. Two pairs cannot rule out coincidental flakiness or establish a cause.
func compareRepeatedJavaScript(baseline, instrumented, baselineAgain, instrumentedAgain validationRun) verdict {
	return compareRepeatedRuns(baseline, instrumented, baselineAgain, instrumentedAgain, verifyJestTelemetry)
}

// compareRepeatedRuns compares two repeated pairs with compareRuns.
func compareRepeatedRuns(baseline, instrumented, baselineAgain, instrumentedAgain validationRun, telemetry func(validationRun) verdict) verdict {
	result := compareRuns(baseline, instrumented, telemetry)
	if reason := pairTimeout(baselineAgain, instrumentedAgain); reason != "" {
		return verdict{Status: "inconclusive", Code: "command_timeout", Reason: reason}
	}
	if slices.Equal(outcomeKeys(baseline), outcomeKeys(baselineAgain)) && slices.Equal(outcomeKeys(instrumented), outcomeKeys(instrumentedAgain)) {
		result.Reason += " Only two pairs were observed; repeatable flakiness can still produce this observation."
		return result
	}
	result.Status, result.Code = "inconclusive", "varying_outcomes"
	result.Reason = "Outcomes changed between repeated runs; flakiness or changing setup prevents attributing the difference to instrumentation."
	for _, run := range []validationRun{baseline, instrumented, baselineAgain, instrumentedAgain} {
		if run.ResultError != "" {
			return result
		}
	}
	type identity struct{ file, name, playwright string }
	outcomes := [4]map[identity][]string{}
	identities := map[identity]bool{}
	for i, run := range []validationRun{baseline, instrumented, baselineAgain, instrumentedAgain} {
		outcomes[i] = map[identity][]string{}
		for _, test := range run.Tests {
			key := identity{test.File, test.Name, playwrightLabel(test) + webdriverIOOutcome(test)}
			identities[key] = true
			outcomes[i][key] = append(outcomes[i][key], test.Status+"\n"+test.Failure+playwrightOutcome(test)+cypressOutcome(test)+cucumberOutcome(test)+webdriverIOAttemptOutcome(test))
		}
		for _, values := range outcomes[i] {
			slices.Sort(values)
		}
	}
	keys := slices.Collect(maps.Keys(identities))
	slices.SortFunc(keys, func(a, b identity) int {
		if c := strings.Compare(a.file, b.file); c != 0 {
			return c
		}
		if c := strings.Compare(a.name, b.name); c != 0 {
			return c
		}
		return strings.Compare(a.playwright, b.playwright)
	})
	var recurring, variable, unstable []string
	for _, key := range keys {
		a, b, c, d := outcomes[0][key], outcomes[1][key], outcomes[2][key], outcomes[3][key]
		if slices.Equal(a, b) && slices.Equal(a, c) && slices.Equal(a, d) {
			continue
		}
		file := key.file
		if relative, err := filepath.Rel(baseline.root, file); err == nil && filepath.IsLocal(relative) {
			file = relative
		}
		label := file + " › " + key.name + key.playwright
		reported := 0
		for _, values := range [][]string{a, b, c, d} {
			if len(values) > 0 {
				reported++
			}
		}
		if reported == 1 {
			// A name only one run reports: the test's identity changes
			// between runs, as when its description embeds generated data.
			unstable = append(unstable, label)
			variable = append(variable, "Varies between repeats: "+label+"; baseline: "+outcomeDescription(a)+" -> "+outcomeDescription(c)+"; instrumented: "+outcomeDescription(b)+" -> "+outcomeDescription(d))
			continue
		}
		if slices.Equal(a, c) && slices.Equal(b, d) {
			recurring = append(recurring, "Recurring difference in both pairs: "+label+"; baseline: "+outcomeDescription(a)+"; instrumented: "+outcomeDescription(b))
		} else {
			variable = append(variable, "Varies between repeats: "+label+"; baseline: "+outcomeDescription(a)+" -> "+outcomeDescription(c)+"; instrumented: "+outcomeDescription(b)+" -> "+outcomeDescription(d))
		}
	}
	// An exit code or suite-level failure can recur while unrelated tests vary.
	// A recurring test difference already explains a recurring exit code.
	process := [4][]string{processKeys(baseline), processKeys(instrumented), processKeys(baselineAgain), processKeys(instrumentedAgain)}
	if !slices.Equal(process[0], process[1]) && slices.Equal(process[0], process[2]) && slices.Equal(process[1], process[3]) && len(recurring) == 0 {
		recurring = append(recurring, "Recurring process difference in both pairs; baseline: "+processDescription(process[0])+"; instrumented: "+processDescription(process[1]))
	}
	// Keep exit codes and suite-level differences when no test identity explains
	// the varying run, rather than pretending a process failure is a test failure.
	// One-off names are unstable identities only when each is one example's
	// name in one of several runs; disjoint stable names stay varying.
	runsNamed := map[string]int{}
	for _, label := range unstable {
		runsNamed[generatedName(label)]++
	}
	variants := len(unstable) > 0 && !slices.ContainsFunc(unstable, func(label string) bool { return runsNamed[generatedName(label)] < 2 })
	if variants && len(unstable) == len(variable) && len(recurring) == 0 {
		// Every difference is a test only one run reported: the set of names
		// differs, not the outcomes. Each example is listed once, under the
		// name one run gave it.
		var examples []string
		names := map[string]int{}
		for _, label := range unstable {
			key := generatedName(label)
			if names[key] == 0 {
				examples = append(examples, label)
			}
			names[key]++
		}
		result.Code = "unstable_test_identities"
		result.Reason = fmt.Sprintf("%d %s (%d test names across the runs) %s reported by a single run: these test names change between runs, usually because a description the framework generates (such as a one-line example described by its matcher) embeds a temporary file name, a timestamp or another value created at run time. Every run then reports different tests, so outcomes cannot be compared, and Test Optimization cannot track these tests across CI runs either. Give each listed example a static description; ddtest does not rename tests.", len(examples), plural(len(examples), "example", "examples"), len(unstable), plural(len(examples), "is", "are"))
		result.Differences = nil
		for i, label := range examples {
			if i == maxUnstableExamples {
				result.Differences = append(result.Differences, fmt.Sprintf("... and %d more examples; every test name is in the configuration's native results", len(examples)-i))
				break
			}
			result.Differences = append(result.Differences, "Reported by a single run: "+label)
		}
		return result
	}
	if len(recurring)+len(variable) > 0 {
		result.Differences = append(recurring, variable...)
	}
	if len(recurring) > 0 {
		result.Status, result.Code = "suspected regression", "recurring_difference"
		result.Reason = "The same differences recur in both pairs while other outcomes varied between repeats."
	}
	result.Reason += fmt.Sprintf(" %d %s in both pairs; %d %s between repeats. Only two pairs were observed, so coincidental flakiness cannot be ruled out, and this does not establish a cause.", len(recurring), plural(len(recurring), "difference recurs", "differences recur"), len(variable), plural(len(variable), "varies", "vary"))
	return result
}

func processKeys(run validationRun) []string {
	keys := append([]string{fmt.Sprintf("exit=%d", run.ExitCode)}, run.SuiteErrors...)
	keys = append(keys, run.SuiteFailures...)
	for i := range keys {
		keys[i] = normalizeOwnedTemporaryPath(run, keys[i])
	}
	slices.Sort(keys)
	return keys
}

func processDescription(keys []string) string {
	return reportText(strings.Join(keys, ", "))
}

func outcomeDescription(values []string) string {
	if len(values) == 0 {
		return "not reported"
	}
	counts := map[string]int{}
	for _, value := range values {
		counts[value]++
	}
	var descriptions []string
	for _, value := range slices.Sorted(maps.Keys(counts)) {
		status, failure, _ := strings.Cut(value, "\n")
		if counts[value] > 1 {
			status += fmt.Sprintf(" x%d", counts[value])
		}
		if failure != "" {
			status += " (" + reportText(failure) + ")"
		}
		descriptions = append(descriptions, status)
	}
	return reportText(strings.Join(descriptions, ", "))
}

// maxUnstableExamples bounds the examples an unstable-identity verdict
// lists; more are counted.
const maxUnstableExamples = 50

// generatedValues are the parts of a test name a run generates: quoted
// strings, file paths holding digits, object addresses, timestamps and long
// numbers.
var generatedValues = regexp.MustCompile(`"[^"]*"|'[^']*'|/[^\s>"']*[0-9][^\s>"']*|0x[0-9a-fA-F]+|[0-9]{4}-[0-9]{2}-[0-9]{2}[T ][0-9:.]+|[0-9]{6,}`)

// generatedName is a test label without the values a run generates, so the
// names one example takes in different runs read the same. A description
// a framework truncated in the middle ("...") compares by what precedes the
// cut, which moves with the generated values.
func generatedName(label string) string {
	label, _, _ = strings.Cut(label, "...")
	return generatedValues.ReplaceAllString(label, "…")
}
