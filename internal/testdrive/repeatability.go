package testdrive

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

// Retain recurring per-test differences even when other tests change between
// repeats. This describes observations, not their cause; the verdict stays open.
func compareRepeatedJavaScript(baseline, instrumented, baselineAgain, instrumentedAgain validationRun) verdict {
	result := compareJest(baseline, instrumented)
	if slices.Equal(outcomeKeys(baseline), outcomeKeys(baselineAgain)) && slices.Equal(outcomeKeys(instrumented), outcomeKeys(instrumentedAgain)) {
		return result
	}
	result.Status = "inconclusive"
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
		return strings.Compare(a.name, b.name)
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
	// Keep exit codes and suite-level differences when no test identity explains
	// the varying run, rather than pretending a process failure is a test failure.
	if len(recurring)+len(variable) > 0 {
		result.Differences = append(recurring, variable...)
	}
	result.Reason += fmt.Sprintf(" %d test identities have recurring differences in both pairs; %d vary between repeats. Recurring differences still require investigation; this does not establish their cause.", len(recurring), len(variable))
	return result
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
