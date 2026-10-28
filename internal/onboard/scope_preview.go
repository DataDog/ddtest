package onboard

import (
	"fmt"
	"io"
	"strings"
)

func WriteValidationScope(output io.Writer, scope ValidationScope) {
	_, _ = fmt.Fprintln(output, "\nRequired local validation configurations (every CI consumer must also be instrumented):")
	for _, entry := range scope.Commands {
		_, _ = fmt.Fprintf(output, "  - [%s] %s\n", entry.Directory, entry.Command)
		WriteLifecycleHooks(output, entry)
		if entry.CICoverageCommand != "" {
			_, _ = fmt.Fprintf(output, "      CI coverage command: %s\n      Apply the CI wrapper. Real source coverage is a separate check.\n", entry.CICoverageCommand)
		}
		if entry.Resolution != "" {
			_, _ = fmt.Fprintf(output, "      Resolved test command: %s\n", entry.Resolution)
		}
		for _, build := range entry.Prerequisites {
			_, _ = fmt.Fprintf(output, "      Preparation: [%s] %s\n", build.Directory, build.Command)
		}
		for _, setup := range entry.UnvalidatedSetup {
			_, _ = fmt.Fprintf(output, "      Unvalidated setup: %s\n", setup)
		}
		for _, note := range entry.StepScopeNotes {
			_, _ = fmt.Fprintf(output, "      Shared CI step: %s\n", note)
		}
		for _, location := range entry.Locations {
			_, _ = fmt.Fprintf(output, "      %s\n", location)
		}
	}
	_, _ = fmt.Fprintln(output, "--all replays listed preparation in separate temporary copies before each comparison, without instrumentation. --check-only executes no preparation. Unresolved setup stays unvalidated. Preserve CI commands.")
	for _, reason := range scope.Unresolved {
		_, _ = fmt.Fprintf(output, "  - Unresolved scope: %s\n", reason)
	}
	for _, reason := range scope.Review {
		_, _ = fmt.Fprintf(output, "  - Review separately: %s\n", reason)
	}
}

// writeAgentValidationScope is the scope a coding agent needs to apply CI
// setup: every configuration with its first CI location and every unresolved
// or review entry, each on one line. The full listing stays in
// onboard --instructions and in the validation report.
func writeAgentValidationScope(output io.Writer, scope ValidationScope) {
	_, _ = fmt.Fprintln(output, "\nRequired local validation configurations (every CI consumer must also be instrumented):")
	for _, entry := range scope.Commands {
		_, _ = fmt.Fprintf(output, "  - [%s] %s", entry.Directory, entry.Command)
		if len(entry.Locations) > 0 {
			_, _ = fmt.Fprintf(output, " — %s", OneLineExcerpt(entry.Locations[0]))
			if more := len(entry.Locations) - 1; more > 0 {
				_, _ = fmt.Fprintf(output, " (+%d more CI locations)", more)
			}
		}
		_, _ = fmt.Fprintln(output)
		for _, note := range entry.StepScopeNotes {
			_, _ = fmt.Fprintf(output, "      Shared CI step: %s\n", note)
		}
	}
	for _, reason := range scope.Unresolved {
		_, _ = fmt.Fprintf(output, "  - Unresolved scope: %s\n", OneLineExcerpt(reason))
	}
	for _, reason := range scope.Review {
		_, _ = fmt.Fprintf(output, "  - Review separately: %s\n", OneLineExcerpt(reason))
	}
}

// OneLineExcerpt shortens a diagnostic that embeds a multi-line CI script to
// its first script line and the first line of the reason that follows it.
func OneLineExcerpt(value string) string {
	first, rest, multiline := strings.Cut(strings.TrimRight(value, "\n"), "\n")
	if !multiline {
		return value
	}
	excerpt := first + " …"
	if _, reason, found := strings.Cut(rest, "): "); found {
		line, _, more := strings.Cut(reason, "\n")
		excerpt += "): " + line
		if more {
			excerpt += " …"
		}
	}
	return excerpt
}

// Any hook may dispatch an uploader through an alias or JavaScript. Warn for
// every retained hook rather than guessing from executable names.
func WriteLifecycleHooks(output io.Writer, entry TestCommand) {
	for _, hook := range entry.LifecycleHooks {
		_, _ = fmt.Fprintf(output, "      Lifecycle hook: %s\n", hook)
	}
	if len(entry.LifecycleHooks) > 0 {
		_, _ = fmt.Fprintln(output, "      Hooks execute locally in the disposable copy during both suite runs and may upload coverage or publish artifacts; review them before execution. Synthetic probes omit them.")
	}
}
