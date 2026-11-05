// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import "strings"

// processRetrySubtreeRunPattern narrows discovery to the managed root without
// admitting descendants that the caller's native -test.run filter excluded.
// Reuse testing's matcher grammar, including slash-separated alternatives,
// rather than interpreting the complete selector as one regular expression.
func processRetrySubtreeRunPattern(original, runRoot string) string {
	selection := newTestingSelectionFromPatterns(original, "", "")
	if selection == nil {
		// testing rejects invalid selectors before execution. Do not broaden one
		// if a direct caller nevertheless supplies it here.
		return "^$"
	}
	filters := []testingFilter{selection.filter}
	if alternatives, ok := selection.filter.(testingAlternationMatch); ok {
		filters = alternatives
	}
	segments := strings.Split(runRoot, "/")
	exact := processRetryExactRunPattern(runRoot)
	var patterns []string
	for _, filter := range filters {
		parts := filter.(testingSimpleMatch)
		if ok, _ := parts.matches(segments, selection.matchString); !ok {
			continue
		}
		if len(parts) <= len(segments) {
			return exact
		}
		patterns = append(patterns, exact+"/"+strings.Join(parts[len(segments):], "/"))
	}
	if len(patterns) == 0 {
		return "^$"
	}
	return strings.Join(patterns, "|")
}
