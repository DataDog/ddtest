package testdrive

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// Retain recurring per-test differences even when other tests change between
// repeats. Two pairs cannot rule out coincidental flakiness or establish a cause.
func compareRepeatedJavaScript(baseline, instrumented, baselineAgain, instrumentedAgain validationRun) verdict {
	result := compareJest(baseline, instrumented)
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
	type identity struct{ file, name string }
	outcomes := [4]map[identity][]string{}
	identities := map[identity]bool{}
	for i, run := range []validationRun{baseline, instrumented, baselineAgain, instrumentedAgain} {
		outcomes[i] = map[identity][]string{}
		for _, test := range run.Tests {
			key := identity{test.File, test.Name}
			identities[key] = true
			outcomes[i][key] = append(outcomes[i][key], test.Status+"\n"+test.Failure)
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
		return 0
	})
	var recurring, variable []string
	for _, key := range keys {
		a, b, c, d := outcomes[0][key], outcomes[1][key], outcomes[2][key], outcomes[3][key]
		if slices.Equal(a, b) && slices.Equal(a, c) && slices.Equal(a, d) {
			continue
		}
		file := key.file
		if relative, err := filepath.Rel(baseline.root, file); err == nil && filepath.IsLocal(relative) {
			file = relative
		}
		label := file + " › " + key.name
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
	if len(recurring)+len(variable) > 0 {
		result.Differences = append(recurring, variable...)
	}
	if len(recurring) > 0 {
		result.Status, result.Code = "suspected regression", "recurring_difference"
		result.Reason = "The same differences recur in both pairs while other outcomes varied between repeats."
	}
	result.Reason += fmt.Sprintf(" %d differences recur in both pairs; %d vary between repeats. Only two pairs were observed, so coincidental flakiness cannot be ruled out, and this does not establish a cause.", len(recurring), len(variable))
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
