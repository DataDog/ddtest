package onboard

import (
	"fmt"
	"io"
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
